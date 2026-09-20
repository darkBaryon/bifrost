// Package plugin 将本地检测结果转换为 BF Chat 的放行或错误短路，不负责生产注册。
package plugin

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/maximhq/bifrost/core/schemas"
)

// Logger 是安全事件所需的宿主日志能力；不传入原文或检测器异常文本。
type Logger interface{ Info(string, ...any) }

// Options 由装配方明确选择客户端错误状态和拦截文案，没有生产默认值。
type Options struct {
	StatusCode  int
	DenyMessage string
}

// state 是检查器与拒绝策略的一份整体快照：两者来自同一份配置，必须一起替换、一起被请求引用。
type state struct {
	checker *guardrails.Checker
	options Options
}

// Plugin 把规则检查器接入 BF 前后置 Hook。实例不持有请求正文，可并发使用。
// 配置可通过 Swap 热替换；每个请求在前置 Hook 固定一份快照，后置 Hook 只用该快照。
type Plugin struct {
	current atomic.Pointer[state]
	logger  Logger
}

// snapshotKey 是请求级快照在 BifrostContext 中的私有键；值类型为 *state，其 checker 为 nil 表示当时未配置。
type snapshotKey struct{}

var _ schemas.LLMPlugin = (*Plugin)(nil)

// New 构造可注册的插件；checker 为 nil 表示未配置（全部透传，options 不参与），否则 options 须合法；调用方须提供可用的 logger 实例。
func New(checker *guardrails.Checker, options Options, logger Logger) (*Plugin, error) {
	if logger == nil {
		return nil, fmt.Errorf("guardrails plugin: logger is required")
	}
	st, err := newState(checker, options)
	if err != nil {
		return nil, err
	}
	p := &Plugin{logger: logger}
	p.current.Store(st)
	return p, nil
}

// newState 校验一份配置快照：未配置（checker 为 nil）时不需要拒绝策略，存零值；已配置时状态码与文案必须合法。
func newState(checker *guardrails.Checker, options Options) (*state, error) {
	if checker == nil {
		return &state{}, nil
	}
	if checker.MaxTextBytes() <= 0 {
		return nil, fmt.Errorf("guardrails plugin: checker is not initialised")
	}
	if options.StatusCode < guardrails.MinDenyStatus || options.StatusCode > guardrails.MaxDenyStatus || strings.TrimSpace(options.DenyMessage) == "" {
		return nil, fmt.Errorf("guardrails plugin: explicit error status and denial message are required")
	}
	return &state{checker: checker, options: options}, nil
}

// Swap 原子替换检查器与拒绝策略，只影响之后到达的请求；checker 为 nil 表示关闭检测（options 忽略）。options 非法时不替换并返回错误。
func (p *Plugin) Swap(checker *guardrails.Checker, options Options) error {
	st, err := newState(checker, options)
	if err != nil {
		return err
	}
	p.current.Store(st)
	return nil
}

// GetName 返回区别于上游商业插件的注册名称。
func (p *Plugin) GetName() string { return "ee-content-safety" }

// Cleanup 不关闭注入的检测器；它们的生命周期归装配方所有。
func (p *Plugin) Cleanup() error { return nil }

// PreRequestHook 不参与路由；安全阻断由支持短路的 PreLLMHook 执行。
func (p *Plugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error { return nil }

// PreLLMHook 固定本请求的快照后检查完整输入；配置输出规则时在调用模型前拒绝流式请求。
func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	st := p.snapshot(ctx)
	if st.checker == nil {
		return req, nil, nil
	}
	checker := st.checker
	input, output := checker.HasRules(guardrails.Input), checker.HasRules(guardrails.Output)
	if !input && !output {
		return req, nil, nil
	}
	reject := func(code errorCode) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
		return req, &schemas.LLMPluginShortCircuit{Error: p.buildDenialError(ctx, st.options, code)}, nil
	}
	if code := validateRequest(ctx, checker, req); code != "" {
		return reject(code)
	}
	if input {
		last, err := extractInputText(req.ChatRequest, checker.MaxTextBytes())
		if err != nil {
			return reject(mapCheckError(err, unsupportedContent))
		}
		texts := guardrails.Texts{Last: last}
		// 全部消息只在有规则需要（密钥）时才提取，判官类规则不因历史里的旧消息改判。
		if checker.NeedsAllInput() {
			all, err := extractAllInputText(req.ChatRequest, checker.MaxTextBytes())
			if err != nil {
				return reject(mapCheckError(err, unsupportedContent))
			}
			texts.All = &all
		}
		if code := p.checkTexts(ctx, checker, guardrails.Input, texts); code != "" {
			return reject(code)
		}
	}
	return req, nil, nil
}

// PostLLMHook 只检查完整 Chat 响应；上游失败原样保留，不把错误正文当模型文本检测。
// 只使用前置 Hook 留下的快照；快照缺失或未配置时直接透传，不回读当前配置，避免热更新改判进行中的请求。
func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, upstreamErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	st := existingSnapshot(ctx)
	if upstreamErr != nil || st == nil || st.checker == nil || !st.checker.HasRules(guardrails.Output) {
		return resp, upstreamErr, nil
	}
	texts, err := extractOutputTexts(resp, st.checker.MaxTextBytes())
	if err != nil {
		return nil, p.buildDenialError(ctx, st.options, mapCheckError(err, unsupportedContent)), nil
	}
	for _, text := range texts {
		if code := p.checkTexts(ctx, st.checker, guardrails.Output, guardrails.Texts{Last: text}); code != "" {
			return nil, p.buildDenialError(ctx, st.options, code), nil
		}
	}
	return resp, nil, nil
}

// snapshot 返回本请求的快照：已有则沿用（fallback 会重跑前置 Hook），否则读取当前配置并写入 ctx。
func (p *Plugin) snapshot(ctx *schemas.BifrostContext) *state {
	if ctx == nil {
		return p.current.Load()
	}
	if stored, ok := ctx.Value(snapshotKey{}).(*state); ok && stored != nil {
		return stored
	}
	st := p.current.Load()
	ctx.SetValue(snapshotKey{}, st)
	return st
}

func existingSnapshot(ctx *schemas.BifrostContext) *state {
	if ctx == nil {
		return nil
	}
	stored, _ := ctx.Value(snapshotKey{}).(*state)
	return stored
}

// validateRequest 检查请求结构、流式限制及请求选项；消息内容由文本提取阶段校验。
func validateRequest(ctx *schemas.BifrostContext, checker *guardrails.Checker, req *schemas.BifrostRequest) errorCode {
	if ctx == nil || req == nil || req.ChatRequest == nil || (req.RequestType != schemas.ChatCompletionRequest && req.RequestType != schemas.ChatCompletionStreamRequest) {
		return unsupportedContent
	}
	if checker.HasRules(guardrails.Output) && req.RequestType == schemas.ChatCompletionStreamRequest {
		return unsupportedOutputStream
	}
	if unsupportedRequest(ctx, req.ChatRequest) {
		return unsupportedContent
	}
	return ""
}

func (p *Plugin) checkTexts(ctx *schemas.BifrostContext, checker *guardrails.Checker, stage guardrails.Stage, texts guardrails.Texts) errorCode {
	if ctx == nil {
		return checkFailed
	}
	checkResult, err := checker.CheckTexts(ctx, stage, texts)
	for _, ruleEvaluation := range checkResult.RuleEvaluations {
		p.logger.Info("content safety: request=%q rule=%q detector=%q category=%s stage=%s outcome=%s action=%s failure=%s level=%d",
			requestID(ctx), ruleEvaluation.RuleID, ruleEvaluation.DetectorID, ruleEvaluation.Category,
			ruleEvaluation.Stage, ruleEvaluation.Outcome, ruleEvaluation.Action, ruleEvaluation.Failure, ruleEvaluation.HighestLevel)
	}
	if err != nil {
		return mapCheckError(err, checkFailed)
	}
	if checkResult.Action != guardrails.Block {
		return ""
	}
	return blockingCode(checkResult.Blocking)
}

// blockingCode 由决定拦截的规则评估选择客户端错误码：命中为拦截，检测失败按失败类别区分超限与其他故障。
func blockingCode(blocking *guardrails.RuleEvaluation) errorCode {
	if blocking == nil || blocking.Outcome != guardrails.Failed {
		return contentBlocked
	}
	if blocking.Failure == guardrails.TextTooLarge {
		return textTooLarge
	}
	return checkFailed
}
