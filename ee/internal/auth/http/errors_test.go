// 本文件验证认证错误的安全输出和登录限流窗口。
package authhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/valyala/fasthttp"
)

func TestErrorResponseContract(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{auth.ErrInvalid, 400, "invalid_input"},
		{auth.ErrUnauthorized, 401, "unauthorized"},
		{auth.ErrForbidden, 403, "forbidden"},
		{auth.ErrNotFound, 404, "not_found"},
		{auth.ErrConflict, 409, "conflict"},
		{auth.ErrLimited, 429, "rate_limited"},
		{fmt.Errorf("private context: %w", auth.ErrForbidden), 403, "forbidden"},
		{session.ErrUnauthorized, 401, "unauthorized"},
		{session.ErrInvalid, 400, "invalid_input"},
		{session.ErrUnavailable, 503, "unavailable"},
		{httpx.ErrInvalid, 400, "invalid_input"},
		{auth.Error("private database details"), 503, "unavailable"},
		{errors.New("unauthorized"), 503, "unavailable"},
		{errors.New("private database details"), 503, "unavailable"},
	} {
		t.Run(fmt.Sprintf("%T/%s", tc.err, tc.err), func(t *testing.T) {
			var c fasthttp.RequestCtx
			Error(&c, tc.err)
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
				t.Fatal(err)
			}
			if c.Response.StatusCode() != tc.status || body.Error.Code != tc.code || body.Error.Message != tc.code {
				t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
			}
			retry := string(c.Response.Header.Peek("Retry-After"))
			if tc.status == 429 && retry != "60" {
				t.Fatalf("retry=%q", retry)
			}
			if tc.status != 429 && retry != "" {
				t.Fatalf("unexpected retry=%q", retry)
			}
		})
	}
}
