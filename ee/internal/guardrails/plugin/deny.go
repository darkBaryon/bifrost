// 本文件将安全检查结果映射为客户端错误，并构造禁止模型回退的拒绝响应。
package plugin

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/maximhq/bifrost/core/schemas"
)

// errorCode 是入口返回的安全错误类别，避免用普通插件 error 冒充阻断。
type errorCode string

// 安全拦截与无法完成检查使用不同错误码。
const (
	contentBlocked          errorCode = "content_safety_blocked"
	checkFailed             errorCode = "content_safety_check_failed"
	unsupportedContent      errorCode = "content_safety_unsupported_content"
	unsupportedOutputStream errorCode = "content_safety_output_stream_unsupported"
	textTooLarge            errorCode = "content_safety_text_too_large"
	checkCanceled           errorCode = "content_safety_canceled"
)

// buildDenialError 用请求快照里的拒绝策略构造客户端错误；状态码范围见 guardrails.MinDenyStatus。
func (p *Plugin) buildDenialError(ctx *schemas.BifrostContext, options Options, code errorCode) *schemas.BifrostError {
	message := "内容安全检查未完成"
	switch code {
	case contentBlocked:
		message = options.DenyMessage
	case unsupportedContent:
		message = "内容安全框架当前仅支持规范化的纯文本 Chat 内容"
	case unsupportedOutputStream:
		message = "开启输出检查时暂不支持流式请求，请使用非流式请求"
	case textTooLarge:
		message = "内容超过安全检查长度上限"
	case checkCanceled:
		message = "内容安全检查已取消"
	}
	p.logger.Info("content safety: request=%q rejected code=%s", requestID(ctx), code)
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     schemas.Ptr(options.StatusCode),
		AllowFallbacks: schemas.Ptr(false),
		Error: &schemas.ErrorField{
			Type: schemas.Ptr(string(code)), Code: schemas.Ptr(string(code)), Message: message,
		},
	}
}

func mapCheckError(err error, fallback errorCode) errorCode {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return checkCanceled
	}
	if errors.Is(err, guardrails.ErrTextTooLarge) {
		return textTooLarge
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
