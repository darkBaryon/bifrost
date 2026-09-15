// Package plugin 将本地检测结果转换为 BF Chat 的放行或错误短路，不负责生产注册。
package plugin

import (
	"context"
	"errors"
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

// Plugin 把规则引擎接入 BF 前后置 Hook。实例不持有请求正文，可并发使用。
type Plugin struct {
	engine  *guardrails.Engine
	options Options
	logger  Logger
}

var _ schemas.LLMPlugin = (*Plugin)(nil)

// HTTP 错误响应范围；本轮不支持用成功状态码伪装成正常模型回答。
const minErrorStatus, maxErrorStatus = 400, 599

// New 构造可注册的插件；调用方须提供可用的 logger 实例，并负责真实检测器与生产装配。
func New(engine *guardrails.Engine, options Options, logger Logger) (*Plugin, error) {
	if engine == nil || engine.MaxTextBytes() <= 0 || logger == nil {
		return nil, fmt.Errorf("guardrails plugin: engine and logger are required")
	}
	if options.StatusCode < minErrorStatus || options.StatusCode > maxErrorStatus || strings.TrimSpace(options.DenyMessage) == "" {
		return nil, fmt.Errorf("guardrails plugin: explicit error status and denial message are required")
	}
	return &Plugin{engine: engine, options: options, logger: logger}, nil
}

// GetName 返回区别于上游商业插件的注册名称。
func (p *Plugin) GetName() string { return "ee-content-safety" }

// Cleanup 不关闭注入的检测器；它们的生命周期归装配方所有。
func (p *Plugin) Cleanup() error { return nil }

// PreRequestHook 不参与路由；安全阻断由支持短路的 PreLLMHook 执行。
func (p *Plugin) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error { return nil }

// PreLLMHook 检查完整输入；配置输出规则时在调用模型前拒绝流式请求。
func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	input, output := p.engine.HasRules(guardrails.Input), p.engine.HasRules(guardrails.Output)
	if !input && !output {
		return req, nil, nil
	}
	reject := func(code guardrails.ErrorCode) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
		return req, &schemas.LLMPluginShortCircuit{Error: p.reject(ctx, code)}, nil
	}
	if ctx == nil || req == nil || req.ChatRequest == nil || (req.RequestType != schemas.ChatCompletionRequest && req.RequestType != schemas.ChatCompletionStreamRequest) {
		return reject(guardrails.UnsupportedContent)
	}
	if output && req.RequestType == schemas.ChatCompletionStreamRequest {
		return reject(guardrails.UnsupportedOutputStream)
	}
	if unsupportedRequest(ctx, req.ChatRequest) {
		return reject(guardrails.UnsupportedContent)
	}
	if input {
		text, err := inputText(req.ChatRequest, p.engine.MaxTextBytes())
		if err != nil {
			return reject(errorCode(err, guardrails.UnsupportedContent))
		}
		if code := p.check(ctx, guardrails.Input, text); code != "" {
			return reject(code)
		}
	}
	return req, nil, nil
}

// PostLLMHook 只检查完整 Chat 响应；上游失败原样保留，不把错误正文当模型文本检测。
func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, upstreamErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if upstreamErr != nil || !p.engine.HasRules(guardrails.Output) {
		return resp, upstreamErr, nil
	}
	texts, err := outputTexts(resp, p.engine.MaxTextBytes())
	if err != nil {
		return nil, p.reject(ctx, errorCode(err, guardrails.UnsupportedContent)), nil
	}
	for _, text := range texts {
		if code := p.check(ctx, guardrails.Output, text); code != "" {
			return nil, p.reject(ctx, code), nil
		}
	}
	return resp, nil, nil
}

func (p *Plugin) check(ctx *schemas.BifrostContext, stage guardrails.Stage, text string) guardrails.ErrorCode {
	if ctx == nil {
		return guardrails.CheckFailed
	}
	result, err := p.engine.Check(ctx, stage, text)
	for _, event := range result.Events {
		p.logger.Info("content safety: request=%q rule=%q detector=%q category=%s stage=%s outcome=%s action=%s failure=%s level=%d", requestID(ctx), event.RuleID, event.DetectorID, event.Category, event.Stage, event.Outcome, event.Action, event.Failure, event.HighestLevel)
	}
	if err != nil {
		return errorCode(err, guardrails.CheckFailed)
	}
	if result.Action == guardrails.Block {
		if len(result.Events) > 0 && result.Events[len(result.Events)-1].Outcome == guardrails.Failed {
			return guardrails.CheckFailed
		}
		return guardrails.ContentBlocked
	}
	return ""
}

func (p *Plugin) reject(ctx *schemas.BifrostContext, code guardrails.ErrorCode) *schemas.BifrostError {
	message := "内容安全检查未完成"
	switch code {
	case guardrails.ContentBlocked:
		message = p.options.DenyMessage
	case guardrails.UnsupportedContent:
		message = "内容安全框架当前仅支持规范化的纯文本 Chat 内容"
	case guardrails.UnsupportedOutputStream:
		message = "开启输出检查时暂不支持流式请求，请使用非流式请求"
	case guardrails.TextTooLarge:
		message = "内容超过安全检查长度上限"
	case guardrails.CheckCanceled:
		message = "内容安全检查已取消"
	}
	p.logger.Info("content safety: request=%q rejected code=%s", requestID(ctx), code)
	return &schemas.BifrostError{IsBifrostError: true, StatusCode: schemas.Ptr(p.options.StatusCode), AllowFallbacks: schemas.Ptr(false),
		Error: &schemas.ErrorField{Type: schemas.Ptr(string(code)), Code: schemas.Ptr(string(code)), Message: message}}
}

func errorCode(err error, fallback guardrails.ErrorCode) guardrails.ErrorCode {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return guardrails.CheckCanceled
	}
	if errors.Is(err, guardrails.ErrTextTooLarge) {
		return guardrails.TextTooLarge
	}
	return fallback
}

func requestID(ctx *schemas.BifrostContext) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(schemas.BifrostContextKeyRequestID).(string)
	return id
}
