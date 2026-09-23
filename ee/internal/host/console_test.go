// 本文件验证基础状态接口不依赖设置权限，且不会放宽完整配置的权限。
package host

import (
	"context"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

func TestConsoleBootstrapWithoutSettingsPermission(t *testing.T) {
	adapter, admin, services := testAdapter(t)
	adapter.host = &server.BifrostHTTPServer{Config: &lib.Config{EnvLabel: "test"}}
	adapter.access = failingConsoleAccess{identity.ErrForbidden}
	ctx := context.Background()
	if _, err := services.Account.CreateAccount(ctx, admin.Principal, "viewer", "Viewer"); err != nil {
		t.Fatal(err)
	}
	viewer, err := services.Session.Login(ctx, "viewer", "123456", "peer")
	if err != nil {
		t.Fatal(err)
	}
	routes := router.New()
	adapter.RegisterSessionRoutes(routes, adapter.APIMiddleware())
	routes.GET("/api/config", adapter.APIMiddleware()(func(c *fasthttp.RequestCtx) { t.Error("full config reached without permission") }))
	c := request(routes, "POST", "/api/console/bootstrap", "{}", viewer.Token, adapter.http.Origin(), "")
	if c.Response.StatusCode() != 200 {
		t.Fatalf("session without Settings.View must read bootstrap: got %d, body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	c = request(routes, "GET", "/api/config", "", viewer.Token, "", "")
	if c.Response.StatusCode() != 403 {
		t.Fatalf("config status=%d", c.Response.StatusCode())
	}
}

func TestConsoleBootstrapMiddlewareOwnershipIsExact(t *testing.T) {
	a, _, _ := testAdapter(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/console/bootstrap"},
		{"PUT", "/api/console/bootstrap"},
		{"POST", "/api/console/bootstrap/"},
		{"POST", "/api/Console/bootstrap"},
		{"POST", "/api/console/bootstrap/extra"},
		{"POST", "/api/console/bootstraps"},
	} {
		called := false
		handler := a.APIMiddleware()(func(*fasthttp.RequestCtx) { called = true })
		c := consoleRequest(handler, tc.method, tc.path, "{}", "", a.http.Origin(), nil)
		if called || c.Response.StatusCode() != 401 {
			t.Fatalf("auth bypass: %s %s status=%d", tc.method, tc.path, c.Response.StatusCode())
		}
	}
}
