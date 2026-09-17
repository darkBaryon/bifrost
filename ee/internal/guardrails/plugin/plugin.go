// Package plugin 将本地检测结果转换为 BF Chat 的放行或错误短路，不负责生产注册。
package plugin

import (
	"fmt"
	"strings"

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

// Plugin 把规则检查器接入 BF 前后置 Hook。实例不持有请求正文，可并发使用。
type Plugin struct {
	checker *guardrails.Checker
	options Options
	logger  Logger
}

var _ schemas.LLMPlugin = (*Plugin)(nil)

// New 构造可注册的插件；调用方须提供可用的 logger 实例，并负责真实检测器与生产装配。
func New(checker *guardrails.Checker, options Options, logger Logger) (*Plugin, error) {
	if checker == nil || checker.MaxTextBytes() <= 0 || logger == nil {
		return nil, fmt.Errorf("guardrails plugin: checker and logger are required")
	}
	if options.StatusCode < minErrorStatus || options.StatusCode > maxErrorStatus || strings.TrimSpace(options.DenyMessage) == "" {
		return nil, fmt.Errorf("guardrails plugin: explicit error status and denial message are required")
	}
	return &Plugin{checker: checker, options: options, logger: logger}, nil
}

// GetName 返回区别于上游商业插件的注册名称。
func (p *Plugin) GetName() string { return "ee-content-safety" }

// Cleanup 不关闭注入的检测器；它们的生命周期归装配方所有。
func (p *Plugin) Cleanup() error { return nil }

// PreRequestHook 不参与路由；安全阻断由支持短路的 PreLLMHook 执行。
func (p *Plugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error { return nil }

// PreLLMHook 检查完整输入；配置输出规则时在调用模型前拒绝流式请求。
func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	input, output := p.checker.HasRules(guardrails.Input), p.checker.HasRules(guardrails.Output)
	if !input && !output {
		return req, nil, nil
	}
	reject := func(code errorCode) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
		return req, &schemas.LLMPluginShortCircuit{Error: p.buildDenialError(ctx, code)}, nil
	}
	if code := p.validateRequest(ctx, req); code != "" {
		return reject(code)
	}
	if input {
		text, err := extractInputText(req.ChatRequest, p.checker.MaxTextBytes())
		if err != nil {
			return reject(mapCheckError(err, unsupportedContent))
		}
		if code := p.checkText(ctx, guardrails.Input, text); code != "" {
			return reject(code)
		}
	}
	return req, nil, nil
}

// PostLLMHook 只检查完整 Chat 响应；上游失败原样保留，不把错误正文当模型文本检测。
func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, upstreamErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if upstreamErr != nil || !p.checker.HasRules(guardrails.Output) {
		return resp, upstreamErr, nil
	}
	texts, err := extractOutputTexts(resp, p.checker.MaxTextBytes())
	if err != nil {
		return nil, p.buildDenialError(ctx, mapCheckError(err, unsupportedContent)), nil
	}
	for _, text := range texts {
		if code := p.checkText(ctx, guardrails.Output, text); code != "" {
			return nil, p.buildDenialError(ctx, code), nil
		}
	}
	return resp, nil, nil
}

// validateRequest 检查请求结构、流式限制及请求选项；消息内容由文本提取阶段校验。
func (p *Plugin) validateRequest(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) errorCode {
	if ctx == nil || req == nil || req.ChatRequest == nil || (req.RequestType != schemas.ChatCompletionRequest && req.RequestType != schemas.ChatCompletionStreamRequest) {
		return unsupportedContent
	}
	if p.checker.HasRules(guardrails.Output) && req.RequestType == schemas.ChatCompletionStreamRequest {
		return unsupportedOutputStream
	}
	if unsupportedRequest(ctx, req.ChatRequest) {
		return unsupportedContent
	}
	return ""
}

func (p *Plugin) checkText(ctx *schemas.BifrostContext, stage guardrails.Stage, text string) errorCode {
	if ctx == nil {
		return checkFailed
	}
	checkResult, err := p.checker.Check(ctx, stage, text)
	for _, ruleEvaluation := range checkResult.RuleEvaluations {
		p.logger.Info("content safety: request=%q rule=%q detector=%q category=%s stage=%s outcome=%s action=%s failure=%s level=%d",
			requestID(ctx), ruleEvaluation.RuleID, ruleEvaluation.DetectorID, ruleEvaluation.Category,
			ruleEvaluation.Stage, ruleEvaluation.Outcome, ruleEvaluation.Action, ruleEvaluation.Failure, ruleEvaluation.HighestLevel)
	}
	if err != nil {
		return mapCheckError(err, checkFailed)
	}
	if checkResult.Action == guardrails.Block {
		if len(checkResult.RuleEvaluations) > 0 && checkResult.RuleEvaluations[len(checkResult.RuleEvaluations)-1].Outcome == guardrails.Failed {
			return checkFailed
		}
		return contentBlocked
	}
	return ""
}
