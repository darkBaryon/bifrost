// 本文件用同一真实HTTP合同验证SQLite与隔离PostgreSQL。
package usagehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	usagestore "github.com/darkBaryon/bifrost/ee/internal/usage/persistence"
	"github.com/fasthttp/router"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testLog struct{}

func (testLog) Error(string, ...any) {}

func testDatabase(t *testing.T) func() *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "templates.db") + "?_busy_timeout=1000&_journal_mode=WAL"
	dialect := func() gorm.Dialector { return sqlite.Open(dsn) }
	switch os.Getenv("USAGE_TEST_DATABASE") {
	case "", "sqlite":
	case "postgres":
		dsn = os.Getenv("USAGE_TEST_POSTGRES_DSN")
		require.NotEmpty(t, dsn, "显式PG测试不能跳过或回退SQLite")
		require.Equal(t, "1", os.Getenv("USAGE_TEST_POSTGRES_ISOLATED"))
		admin, e := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, e)
		schema := fmt.Sprintf("usage_%d", time.Now().UnixNano())
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			conn, _ := admin.DB()
			require.NoError(t, conn.Close())
		})
		if strings.Contains(dsn, "?") {
			dsn += "&search_path=" + schema
		} else {
			dsn += " search_path=" + schema
		}
		dialect = func() gorm.Dialector { return postgres.Open(dsn) }
	default:
		t.Fatal("unknown database mode")
	}
	return func() *gorm.DB {
		db, e := gorm.Open(dialect(), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, e)
		conn, e := db.DB()
		require.NoError(t, e)
		t.Cleanup(func() { _ = conn.Close() })
		return db
	}
}

func TestTemplatesContract(t *testing.T) {
	connect := testDatabase(t)
	db := connect()
	ctx := context.Background()
	require.NoError(t, authstore.MigrateIdentity(testLog{})(ctx, db))
	require.NoError(t, rbacstore.MigrateRBAC(testLog{})(ctx, db))
	require.NoError(t, usagestore.MigrateTemplates(testLog{})(ctx, db))
	store := authstore.NewStore(db, testLog{}, authstore.WithPolicyFactory(func(tx *gorm.DB, v identity.Queries) (identity.AccountPolicy, error) {
		return policy.NewPolicy(rbac.New(rbacstore.Bind(tx, v))), nil
	}))
	roles := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	svc, e := identity.New(store, hasher.Bcrypt{}, identity.Options{SetupToken: "setup", InitialPassword: "123456", SessionTTL: 24 * time.Hour}, policy.NewPolicy(roles))
	require.NoError(t, e)
	const password = "Admin-password-1"
	_, e = svc.Account.Initialize(ctx, "setup", "admin", password)
	require.NoError(t, e)
	admin, e := svc.Session.Login(ctx, "admin", password, "admin")
	require.NoError(t, e)
	origin, e := authhttp.NewHandler(svc, "http://localhost")
	require.NoError(t, e)
	handler := NewHandler(usagestore.NewStore(store.ReadWithin, store.Within), svc.Session, origin)
	routes := router.New()
	handler.RegisterRoutes(routes)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, e)
	var active sync.WaitGroup
	server := &fasthttp.Server{DisableKeepalive: true, Handler: func(c *fasthttp.RequestCtx) {
		active.Add(1)
		defer active.Done()
		routes.Handler(c)
	}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		active.Wait()
		// 关闭监听器而不是调用 fasthttp Shutdown：fasthttp 的 Shutdown 会
		// 并发修改 RequestCtx 的取消状态，database/sql 仍可能在读取该状态。
		// 这里已经等所有 Handler 返回，直接关闭监听器即可无竞态地结束 Serve。
		require.NoError(t, listener.Close())
		<-done
	})
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	call := func(action, body, token string, headers ...string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest("POST", "http://"+listener.Addr().String()+"/api/usage/"+action, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		req.AddCookie(&http.Cookie{Name: authhttp.CookieName, Value: token})
		for i := 0; i < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
		return resp.StatusCode, data
	}
	expect := func(action, body, token string, status int, headers ...string) []byte {
		t.Helper()
		actual, data := call(action, body, token, headers...)
		require.Equal(t, status, actual, "%s", data)
		return data
	}
	const valid = `{"name":" 开发人员 ","config":{"max_cost_usd":100,"allowed_models":["openrouter/anthropic/claude","openai/*","*"]}}`
	create := func() saved {
		var value saved
		require.NoError(t, json.Unmarshal(expect("create-template", valid, admin.Token, 201), &value))
		require.True(t, identity.ValidAccountID(value.TemplateID))
		require.EqualValues(t, 1, value.Version)
		return value
	}
	readPage := func(body string) page {
		var p page
		require.NoError(t, json.Unmarshal(expect("list-templates", body, admin.Token, 200), &p))
		return p
	}
	require.Equal(t, page{Templates: []templateDTO{}}, readPage("{}"))
	first, second := create(), create()
	require.NotEqual(t, first.TemplateID, second.TemplateID)
	require.NoError(t, usagestore.MigrateTemplates(testLog{})(ctx, db))
	p := readPage(`{"limit":1}`)
	require.Len(t, p.Templates, 1)
	require.Equal(t, "开发人员", p.Templates[0].Name)
	require.Equal(t, "1M", string(p.Templates[0].Config.ResetDuration))
	require.Equal(t, p.Templates[0].ID, p.NextAfter)
	next := readPage(fmt.Sprintf(`{"after":%q,"limit":1}`, p.NextAfter))
	require.Len(t, next.Templates, 1)
	require.Greater(t, next.Templates[0].ID, p.NextAfter)
	require.Empty(t, next.NextAfter)
	update := fmt.Sprintf(`{"template_id":%q,"expected_version":1,"name":"更新","description":"说明","config":{"max_cost_usd":200,"reset_duration":"1w","allowed_models":["p/m"],"rate_limit":{"request_max":2,"window_seconds":60}}}`, first.TemplateID)
	var updated saved
	require.NoError(t, json.Unmarshal(expect("update-template", update, admin.Token, 200), &updated))
	require.EqualValues(t, 2, updated.Version)
	expect("update-template", update, admin.Token, 409)
	remove := fmt.Sprintf(`{"template_id":%q,"expected_version":1}`, first.TemplateID)
	expect("delete-template", remove, admin.Token, 409)
	// 同一版本并发写入，只能一个成功；相同内容成功写入也递增版本。
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			status, _ := call("update-template", strings.Replace(update, `"expected_version":1`, `"expected_version":2`, 1), admin.Token)
			statuses <- status
		})
	}
	wg.Wait()
	require.ElementsMatch(t, []int{200, 409}, []int{<-statuses, <-statuses})
	// 重建连接和Store后读取，不能依赖处理器缓存。
	rebound := authstore.NewStore(connect(), testLog{})
	rows, _, e := usagestore.NewStore(rebound.ReadWithin, rebound.Within).List(ctx, admin.Principal, "", 20)
	require.NoError(t, e)
	require.Len(t, rows, 2)
	for _, row := range rows {
		if row.ID == first.TemplateID {
			require.EqualValues(t, 3, row.Version)
			require.Equal(t, "说明", row.Description)
			require.Equal(t, 200.0, row.Config.MaxCostUSD)
			require.EqualValues(t, 2, row.Config.RateLimit.RequestMax)
		}
	}
	for _, body := range []string{
		`{}`, `null`, valid + "{}", strings.Replace(valid, `"name":`, `"Name":`, 1),
		strings.Replace(valid, `"name":`, `"name":"duplicate","name":`, 1),
		strings.Replace(valid, "100", "0", 1), strings.Replace(valid, "100", "1e999", 1),
		strings.Replace(valid, "100", "null", 1), strings.Replace(valid, "100", `100,"unknown":1`, 1),
		strings.Replace(valid, `"allowed_models":`, `"reset_duration":"","allowed_models":`, 1),
		strings.Replace(valid, `"allowed_models":`, `"rate_limit":null,"allowed_models":`, 1),
		strings.Replace(valid, "openai/*", "openai/m*", 1),
		strings.Replace(valid, `"allowed_models":`, `"rate_limit":{"token_max":0,"request_max":1,"window_seconds":60},"allowed_models":`, 1),
		strings.Replace(valid, "开发人员", strings.Repeat("长", 101), 1),
	} {
		expect("create-template", body, admin.Token, 400)
	}
	expect("list-templates", `{"limit":101}`, admin.Token, 400)
	expect("list-templates", `{"limit":null}`, admin.Token, 400)
	expect("list-templates", `{"after":"not-uuid"}`, admin.Token, 400)
	expect("create-template", valid, admin.Token, 400, "Content-Type", "text/plain")
	expect("create-template", valid, admin.Token, 403, "Origin", "https://other.example")
	expect("create-template", valid, admin.Token, 401, "Authorization", "Bearer test")
	expect("list-templates", "{}", "", 401)
	// Manage不隐含View，撤权后旧会话与旧Principal均不能写入。
	account, e := svc.Account.CreateAccount(ctx, admin.Principal, "member", "")
	require.NoError(t, e)
	member, e := svc.Session.Login(ctx, "member", "123456", "member")
	require.NoError(t, e)
	expect("create-template", valid, member.Token, 403)
	require.NoError(t, svc.Password.ChangePassword(ctx, member.Principal, "123456", password))
	member, e = svc.Session.Login(ctx, "member", password, "member")
	require.NoError(t, e)
	expect("create-template", valid, member.Token, 403)
	role, e := roles.CreateRole(ctx, policy.Subject(admin.Principal), rbac.RoleInput{Name: "quota managers", PermissionCodes: []rbac.Permission{rbac.UsageManage}})
	require.NoError(t, e)
	_, e = roles.SetAccountRoles(ctx, policy.Subject(admin.Principal), account.ID, []rbac.RoleID{role.ID})
	require.NoError(t, e)
	expect("create-template", valid, member.Token, 201)
	expect("list-templates", "{}", member.Token, 403)
	_, e = roles.SetAccountRoles(ctx, policy.Subject(admin.Principal), account.ID, nil)
	require.NoError(t, e)
	expect("create-template", valid, member.Token, 403)
	_, e = handler.store.Create(ctx, member.Principal, rows[0])
	require.ErrorIs(t, e, identity.ErrForbidden)
	expect("delete-template", strings.Replace(remove, `"expected_version":1`, `"expected_version":3`, 1), admin.Token, 204)
	expect("delete-template", remove, admin.Token, 404)
	expect("update-template", update, admin.Token, 404)
	require.False(t, handler.OwnsRoute("GET", "/api/usage/list-templates"))
	require.False(t, handler.OwnsRoute("POST", "/api/usage/list-templates/"))
	// 实际表故障必须503且不泄漏SQL或驱动原文。
	require.NoError(t, db.Exec("DROP TABLE ee_usage_templates").Error)
	data := expect("list-templates", "{}", admin.Token, 503)
	require.NotContains(t, string(data), "ee_usage_templates")
	require.NotContains(t, string(data), "SELECT")
}
