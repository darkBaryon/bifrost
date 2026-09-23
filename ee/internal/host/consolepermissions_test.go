// 本文件用真实SQLite身份和角色服务确认基础状态不扩大完整配置权限。
package host

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachost "github.com/darkBaryon/bifrost/ee/internal/rbac/host"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConsoleBootstrapWithRealPermissions(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "console.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	for _, migration := range []func(context.Context, *gorm.DB) error{persistence.MigrateIdentity(quietLogger{}), rbacstore.MigrateRBAC(quietLogger{})} {
		if err := migration(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	store := persistence.NewStore(db, quietLogger{}, persistence.WithPolicyFactory(func(tx *gorm.DB, view identity.Queries) (identity.AccountPolicy, error) {
		return rbacpolicy.NewPolicy(rbac.New(rbacstore.Bind(tx, view))), nil
	}))
	permissions := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	svc, err := identity.New(store, hasher.Bcrypt{}, identity.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: time.Hour}, rbacpolicy.NewPolicy(permissions))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Account.Initialize(ctx, "setup", "chief", "Admin-password-1"); err != nil {
		t.Fatal(err)
	}
	chief, err := svc.Session.Login(ctx, "chief", "Admin-password-1", "peer")
	if err != nil {
		t.Fatal(err)
	}
	role, err := permissions.CreateRole(ctx, rbacpolicy.Subject(chief.Principal), rbac.RoleInput{Name: "routing only", PermissionCodes: []rbac.Permission{rbac.RoutingRulesView}})
	if err != nil {
		t.Fatal(err)
	}
	http, err := identityhttp.NewHandler(svc, "https://console.example.test")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{}
	access := rbachost.NewAdapter(permissions, cfg, nil)
	a := NewAuthAdapter(&server.BifrostHTTPServer{Config: cfg}, svc.Session, http, WithConsoleAccess(access), WithAdditionalRoutes(rbachttp.NewHandler(permissions, svc.Session, http)))
	routes := router.New()
	a.RegisterSessionRoutes(routes, a.APIMiddleware())
	routes.GET("/api/config", a.APIMiddleware()(func(*fasthttp.RequestCtx) { t.Error("full config reached without Settings.View") }))
	if err := access.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	handler := access.RootGuard(routes.Handler)
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
		member, err := svc.Session.Login(ctx, name, "123456", "peer")
		if err != nil {
			t.Fatal(err)
		}
		// 尚未改密的有效会话也能读基础状态；普通管理接口仍执行原限制。
		c := consoleRequest(handler, "POST", "/api/console/bootstrap", "{}", member.Token, http.Origin(), nil)
		assertConsoleStatus(t, c, 200)
		if err = svc.Password.ChangePassword(ctx, member.Principal, "123456", "Viewer-password-2"); err != nil {
			t.Fatal(err)
		}
		member, err = svc.Session.Login(ctx, name, "Viewer-password-2", "peer")
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
	}
}
