// 本文件装配真实 SQLite 账号、认证、会话和角色服务，供宿主接入的协议与授权测试共用。
package host

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/auth/hasher"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/auth/http"
	authstore "github.com/darkBaryon/bifrost/ee/internal/auth/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachost "github.com/darkBaryon/bifrost/ee/internal/rbac/host"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	sessionstore "github.com/darkBaryon/bifrost/ee/internal/session/persistence"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// quietLogger 丢弃装配诊断；需要验证日志的测试单独注入记录器。
type quietLogger struct{}

func (quietLogger) Error(string, ...any) {}
func (quietLogger) Info(string, ...any)  {}
func (quietLogger) Warn(string, ...any)  {}

type hostFixture struct {
	Auth        *auth.Service
	Account     *identity.AccountService
	Password    *identity.PasswordService
	Tickets     *Tickets
	Permissions *rbac.Service
	Access      *rbachost.Adapter
	DB          *gorm.DB
}

func testAdapter(t *testing.T) (*AuthAdapter, auth.LoginResult, *hostFixture) {
	t.Helper()
	return newHostFixture(t, "admin", "https://app.example.test")
}

func newHostFixture(t *testing.T, username, origin string) (*AuthAdapter, auth.LoginResult, *hostFixture) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")+"?_busy_timeout=1000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	for _, migration := range []func(context.Context, *gorm.DB) error{persistence.MigrateIdentity(quietLogger{}), rbacstore.MigrateRBAC(quietLogger{})} {
		if err := migration(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	store := persistence.NewStore(db, quietLogger{}, persistence.WithPolicyFactory(func(tx *gorm.DB, view identity.Queries) (identity.AccountPolicy, error) {
		return rbacpolicy.NewPolicy(rbac.New(rbacstore.Bind(tx, view))), nil
	}))
	permissions := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	passwords := auth.NewPasswords(hasher.Bcrypt{})
	sessions, err := session.New(sessionstore.NewStore(db), session.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := identity.New(store, passwords, identity.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: session.DefaultTTL}, rbacpolicy.NewPolicy(permissions), sessions)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.New(db, authstore.NewStore(db, quietLogger{}), sessions, passwords, quietLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Account.Initialize(ctx, "setup", username, "Admin-password-1"); err != nil {
		t.Fatal(err)
	}
	admin, err := authentication.Login(ctx, username, "Admin-password-1", "peer")
	if err != nil {
		t.Fatal(err)
	}
	tickets := NewTickets(db, authentication, sessions, permissions, quietLogger{})
	browser, err := authhttp.NewBrowser(origin)
	if err != nil {
		t.Fatal(err)
	}
	accountHandler, err := identityhttp.NewHandler(accounts, authentication, browser)
	if err != nil {
		t.Fatal(err)
	}
	authenticationHandler, err := authhttp.NewHandler(authentication, tickets, func(ctx context.Context) (bool, error) {
		state, err := accounts.Account.State(ctx)
		return state.Initialized, err
	}, browser)
	if err != nil {
		t.Fatal(err)
	}
	h := &testHTTPRoutes{Browser: browser, accounts: accountHandler, authentication: authenticationHandler}
	access := rbachost.NewAdapter(permissions, &lib.Config{}, quietLogger{})
	adapter := NewAuthAdapter(nil, authentication, tickets, h, access)
	return adapter, admin, &hostFixture{Auth: authentication, Account: accounts.Account, Password: accounts.Password,
		Tickets: tickets, Permissions: permissions, Access: access, DB: db}
}

func verifyHostRoutes(t *testing.T, a *AuthAdapter, routes *router.Router) {
	t.Helper()
	access, ok := a.access.(*rbachost.Adapter)
	if !ok {
		t.Fatal("路由核对必须使用真实 RBAC 接入组件")
	}
	if err := access.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
}

// testHTTPRoutes 仅供测试组合已独立注册的账号与认证接口，并共享浏览器策略。
type testHTTPRoutes struct {
	*authhttp.Browser
	accounts       *identityhttp.Handler
	authentication *authhttp.Handler
}

func (h *testHTTPRoutes) register(r *router.Router) {
	h.accounts.RegisterRoutes(r)
	h.authentication.RegisterRoutes(r)
	h.authentication.RegisterSessionRoutes(r)
}
