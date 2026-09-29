// 本文件验证对外错误契约，保证未知错误与序列化故障不泄露内部信息。
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestWriteErrorContract(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid", ErrInvalid, 400, "invalid_input"},
		{"unauthorized", ErrUnauthorized, 401, "unauthorized"},
		{"forbidden", ErrForbidden, 403, "forbidden"},
		{"not found", ErrNotFound, 404, "not_found"},
		{"conflict", ErrConflict, 409, "conflict"},
		{"limited", ErrLimited, 429, "rate_limited"},
		{"unavailable", ErrUnavailable, 503, "unavailable"},
		{"wrapped code", fmt.Errorf("private context: %w", ErrForbidden), 403, "forbidden"},
		{"unknown code", ErrorCode("private database details"), 503, "unavailable"},
		{"same string different error", errors.New("unauthorized"), 503, "unavailable"},
		{"unrecognized failure", errors.New("private database details"), 503, "unavailable"},
		{"nil", nil, 503, "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c fasthttp.RequestCtx
			WriteError(&c, tt.err)
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
				t.Fatal(err)
			}
			if c.Response.StatusCode() != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.code {
				t.Fatal("error contract changed", c.Response.StatusCode(), string(c.Response.Body()))
			}
			if string(c.Response.Header.ContentType()) != "application/json" {
				t.Fatal("unexpected response media type", string(c.Response.Header.ContentType()))
			}
			if retry := string(c.Response.Header.Peek("Retry-After")); retry != "" {
				t.Fatal("common protocol must not infer a business retry window", retry)
			}
		})
	}
}

func TestWriteErrorPreservesBusinessRetryWindow(t *testing.T) {
	var c fasthttp.RequestCtx
	c.Response.Header.Set("Retry-After", "17")
	WriteError(&c, ErrLimited)
	if retry := string(c.Response.Header.Peek("Retry-After")); retry != "17" {
		t.Fatal("business retry header changed", retry)
	}
}

func TestJSONFailureDoesNotExposeValue(t *testing.T) {
	var c fasthttp.RequestCtx
	JSON(&c, fasthttp.StatusOK, func() {})
	if c.Response.StatusCode() != 503 || string(c.Response.Body()) != `{"error":{"code":"unavailable"}}` {
		t.Fatal("unsafe serialization fallback", string(c.Response.Body()))
	}
}
