// 本文件确认角色权限检查失败时，不会因为操作者恰好是初始化管理员而改用旧规则放行。
package host

import (
	"context"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type failingConsoleAccess struct {
	manages bool
	err     error
}

func (p failingConsoleAccess) Manages(string, string) bool { return p.manages }
func (p failingConsoleAccess) Prepare(context.Context, *fasthttp.RequestCtx, identity.Principal) (schemas.BifrostHTTPMiddleware, error) {
	return nil, p.err
}

func TestRolePolicyFailureDoesNotFallBack(t *testing.T) {
	adapter, admin, _ := testAdapter(t)
	for _, tt := range []struct {
		name   string
		policy failingConsoleAccess
		status int
	}{
		{"unavailable", failingConsoleAccess{true, identity.ErrUnavailable}, 503},
		{"forbidden", failingConsoleAccess{true, identity.ErrForbidden}, 403},
		{"missing-wrapper", failingConsoleAccess{true, nil}, 503},
		{"not-integrated", failingConsoleAccess{false, identity.ErrUnavailable}, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adapter.access = tt.policy
			called := false
			handler := adapter.APIMiddleware()(func(c *fasthttp.RequestCtx) { called = true; c.SetStatusCode(200) })
			c := &fasthttp.RequestCtx{}
			c.Init(&fasthttp.Request{}, nil, nil)
			c.Request.SetRequestURI("/api/providers")
			c.Request.Header.SetCookie("ee_session", admin.Token)
			handler(c)
			if c.Response.StatusCode() != tt.status || called != (tt.status == 200) {
				t.Fatalf("status=%d called=%v", c.Response.StatusCode(), called)
			}
		})
	}
}
