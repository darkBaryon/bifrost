// 本文件用真实SQLite身份和角色服务确认基础状态不扩大完整配置权限。
package host

import (
	"context"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

func TestConsoleBootstrapWithRealPermissions(t *testing.T) {
	ctx := context.Background()
	a, chief, svc := newHostFixture(t, "chief", "https://console.example.test")
	permissions := svc.Permissions
	role, err := permissions.CreateRole(ctx, rbacpolicy.Subject(chief.Principal), rbac.RoleInput{Name: "routing only", PermissionCodes: []rbac.Permission{rbac.RoutingRulesView}})
	if err != nil {
		t.Fatal(err)
	}
	http := a.http
	a.host = &server.BifrostHTTPServer{Config: &lib.Config{}}
	access := svc.Access
	routes := router.New()
	registerIdentityRoutes(t, a, routes)
	a.RegisterConsoleRoutes(routes)
	rbachttp.NewHandler(permissions, svc.Auth, http).RegisterRoutes(routes)
	routes.GET("/api/config", a.APIMiddleware()(func(*fasthttp.RequestCtx) { t.Error("full config reached without Settings.View") }))
	if err := access.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	handler := access.RootGuard(routes.Handler)
	for _, tc := range []struct {
		name, token, origin string
		headers             map[string]string
		status              int
	}{
		{name: "origin precedes authentication", status: 403},
		{name: "anonymous", origin: http.Origin(), status: 401},
		{name: "authorization header", token: chief.Token, origin: http.Origin(), headers: map[string]string{"Authorization": "Bearer old-token"}, status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := consoleRequest(handler, "POST", "/api/permissions/me", "{}", tc.token, tc.origin, tc.headers)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("independent RBAC authentication=%d want=%d", c.Response.StatusCode(), tc.status)
			}
		})
	}
	for _, roleIDs := range [][]rbac.RoleID{nil, {role.ID}} {
		name := "no-role"
		if len(roleIDs) > 0 {
			name = "routing-only"
		}
		account, err := svc.Account.CreateAccount(ctx, chief.Principal, name, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = permissions.SetAccountRoles(ctx, rbacpolicy.Subject(chief.Principal), account.ID, roleIDs); err != nil {
			t.Fatal(err)
		}
		member, err := svc.Auth.Login(ctx, name, "123456", "peer")
		if err != nil {
			t.Fatal(err)
		}
		// 尚未改密的有效会话也能读基础状态；普通管理接口仍执行原限制。
		c := consoleRequest(handler, "POST", "/api/console/bootstrap", "{}", member.Token, http.Origin(), nil)
		assertConsoleStatus(t, c, 200)
		if err = svc.Password.ChangePassword(ctx, member.Principal, "123456", "Viewer-password-2"); err != nil {
			t.Fatal(err)
		}
		member, err = svc.Auth.Login(ctx, name, "Viewer-password-2", "peer")
		if err != nil {
			t.Fatal(err)
		}
		actual, err := permissions.Me(ctx, rbacpolicy.Subject(member.Principal))
		if err != nil {
			t.Fatal(err)
		}
		if len(actual.Permissions) != len(roleIDs) {
			t.Fatalf("unexpected fixture permissions: %v", actual.Permissions)
		}
		if len(roleIDs) > 0 && actual.Permissions[0].Code != rbac.RoutingRulesView {
			t.Fatal("fixture granted wrong permission")
		}
		c = consoleRequest(handler, "POST", "/api/console/bootstrap", "{}", member.Token, http.Origin(), nil)
		assertConsoleStatus(t, c, 200)
		c = consoleRequest(handler, "GET", "/api/config", "", member.Token, "", nil)
		if c.Response.StatusCode() != 403 {
			t.Fatalf("%s config=%d", name, c.Response.StatusCode())
		}
		// 原角色自管路由仍已注册，并按已有会话/角色协议可访问。
		c = consoleRequest(handler, "POST", "/api/permissions/me", "{}", member.Token, http.Origin(), nil)
		if c.Response.StatusCode() != 200 {
			t.Fatalf("RBAC route lost: %d %s", c.Response.StatusCode(), c.Response.Body())
		}
		c = consoleRequest(handler, "POST", "/api/roles/list", "{}", member.Token, http.Origin(), nil)
		if c.Response.StatusCode() != 403 {
			t.Fatalf("RBAC management permission bypassed: %d %s", c.Response.StatusCode(), c.Response.Body())
		}
	}
}
