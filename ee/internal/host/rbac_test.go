// 本文件验证：角色权限存储失败时返回503，不能改用临时令牌继续访问。
package host

import (
	"testing"

	authhttp "github.com/darkBaryon/bifrost/ee/internal/auth/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestPermissionStorageFailureDoesNotFallBackToTemporaryToken(t *testing.T) {
	original, admin, svc := testAdapter(t)
	adapter := NewAuthAdapter(nil, svc.Auth, svc.Tickets, original.http, failingConsoleAccess{err: identity.ErrUnavailable})
	routes := router.New()
	called := false
	routes.GET("/api/oauth/per-user/flows/{id}", adapter.APIMiddleware()(func(c *fasthttp.RequestCtx) {
		called = true
		c.SetStatusCode(fasthttp.StatusNoContent)
	}))
	verifyHostRoutes(t, original, routes)
	c := &fasthttp.RequestCtx{}
	c.Init(&fasthttp.Request{}, nil, nil)
	c.Request.SetRequestURI("/api/oauth/per-user/flows/example")
	c.Request.Header.SetCookie(authhttp.CookieName, admin.Token)
	c.Request.Header.Set("X-Bifrost-Temp-Token", "invalid")
	routes.Handler(c)
	if c.Response.StatusCode() != fasthttp.StatusServiceUnavailable || called {
		t.Fatal("account query failure fell back to temporary authentication", c.Response.StatusCode(), called)
	}
}
