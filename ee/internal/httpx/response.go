// 本文件输出统一 JSON 响应，只允许显式转换过的协议错误码进入响应。
package httpx

import (
	"encoding/json"
	"errors"

	"github.com/valyala/fasthttp"
)

// ErrorCode 是 HTTP 边界允许公开的错误码；业务错误由各模块的 HTTP 入口显式转换。
type ErrorCode string

// HTTP 控制台协议已有的错误码；未知值一律按 unavailable 输出。
const (
	ErrInvalid      ErrorCode = "invalid_input"
	ErrUnauthorized ErrorCode = "unauthorized"
	ErrForbidden    ErrorCode = "forbidden"
	ErrNotFound     ErrorCode = "not_found"
	ErrConflict     ErrorCode = "conflict"
	ErrLimited      ErrorCode = "rate_limited"
	ErrUnavailable  ErrorCode = "unavailable"
)

// Error 返回可公开的协议错误码。
func (e ErrorCode) Error() string { return string(e) }

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// JSON 输出 JSON 响应；序列化失败时返回固定的 503 错误体，不暴露内部细节。
func JSON(c *fasthttp.RequestCtx, code int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		code = fasthttp.StatusServiceUnavailable
		body = []byte(`{"error":{"code":"unavailable"}}`)
	}
	c.SetStatusCode(code)
	c.SetContentType("application/json")
	c.Response.SetBody(body)
}

// WriteError 仅识别 ErrorCode 类型及其包装，其他错误或未知错误码统一返回 503 unavailable。
// 业务限流的 Retry-After 由所属模块设置，本函数不推断重试时间。
func WriteError(c *fasthttp.RequestCtx, err error) {
	var code ErrorCode
	errors.As(err, &code)
	status := fasthttp.StatusServiceUnavailable
	switch code {
	case ErrInvalid:
		status = fasthttp.StatusBadRequest
	case ErrUnauthorized:
		status = fasthttp.StatusUnauthorized
	case ErrForbidden:
		status = fasthttp.StatusForbidden
	case ErrNotFound:
		status = fasthttp.StatusNotFound
	case ErrConflict:
		status = fasthttp.StatusConflict
	case ErrLimited:
		status = fasthttp.StatusTooManyRequests
	default:
		code = ErrUnavailable
	}
	JSON(c, status, errorResponse{Error: errorDetail{Code: code.Error(), Message: code.Error()}})
}
