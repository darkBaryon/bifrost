// 本文件验证：角色权限存储失败时返回503，不能改用临时令牌继续访问。
package host

import (
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestPermissionStorageFailureDoesNotFallBackToTemporaryToken(t *testing.T) {
	original, admin, svc := testAdapter(t)
	adapter := NewAuthAdapter(nil, svc.Session, original.http, WithConsoleAccess(failingConsoleAccess{err: identity.ErrUnavailable}))
	routes := router.New()
	called := false
	routes.GET("/api/oauth/per-user/flows/example", adapter.APIMiddleware()(func(c *fasthttp.RequestCtx) {
		called = true
		c.SetStatusCode(fasthttp.StatusNoContent)
	}))
	c := &fasthttp.RequestCtx{}
	c.Init(&fasthttp.Request{}, nil, nil)
	c.Request.SetRequestURI("/api/oauth/per-user/flows/example")
	c.Request.Header.SetCookie(identityhttp.CookieName, admin.Token)
	c.Request.Header.Set("X-Bifrost-Temp-Token", "invalid")
	routes.Handler(c)
	if c.Response.StatusCode() != fasthttp.StatusServiceUnavailable || called {
		t.Fatal("account query failure fell back to temporary authentication", c.Response.StatusCode(), called)
	}
}
