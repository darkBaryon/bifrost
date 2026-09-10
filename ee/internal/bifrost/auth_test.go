// 本文件验证真实路由链的身份旁路、来源校验与配置回送兼容。
package bifrost

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/fasthttp/router"
	core "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/temptoken"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testAdapter(t *testing.T) (*AuthAdapter, identity.IssuedSession) {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	ctx := context.Background()
	if e = persistence.MigrateIdentity(ctx, db); e != nil {
		t.Fatal(e)
	}
	s, e := identity.NewService(persistence.NewStore(db), persistence.Passwords{}, identity.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: 24 * time.Hour}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Initialize(ctx, "setup", "admin", "Admin-password-1"); e != nil {
		t.Fatal(e)
	}
	v, e := s.Login(ctx, "admin", "Admin-password-1", "peer")
	if e != nil {
		t.Fatal(e)
	}
	h, e := identityhttp.NewHandler(s, "https://app.example.test")
	if e != nil {
		t.Fatal(e)
	}
	return &AuthAdapter{HTTP: h}, v
}
func request(r *router.Router, method, path, body, token, origin, auth string) *fasthttp.RequestCtx {
	req := &fasthttp.Request{}
	req.SetRequestURI(path)
	req.Header.SetMethod(method)
	req.Header.SetContentType("application/json")
	req.SetBodyString(body)
	if token != "" {
		req.Header.SetCookie(identityhttp.CookieName, token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	c := &fasthttp.RequestCtx{}
	c.Init(req, nil, nil)
	r.Handler(c)
	return c
}
func TestAdminBoundary(t *testing.T) {
	a, v := testAdapter(t)
	r := router.New()
	m := a.APIMiddleware()
	a.RegisterSessionRoutes(r, m)
	r.POST("/api/protected", m(func(c *fasthttp.RequestCtx) {
		if c.UserValue(schemas.IsLocalAdminContextKey) != true {
			t.Error("chief compatibility context absent")
		}
		c.SetStatusCode(204)
	}))
	for _, tt := range []struct {
		token, origin, auth string
		code                int
	}{{"", a.HTTP.Origin, "", 401}, {v.Token, "", "", 403}, {v.Token, "https://evil.example", "", 403}, {v.Token, a.HTTP.Origin, "Basic dGVzdA==", 401}, {v.Token, a.HTTP.Origin, "", 204}} {
		c := request(r, "POST", "/api/protected", "{}", tt.token, tt.origin, tt.auth)
		if c.Response.StatusCode() != tt.code {
			t.Fatalf("status=%d expected=%d", c.Response.StatusCode(), tt.code)
		}
	}
	c := request(r, "POST", "/api/identity/create", "{}", v.Token, a.HTTP.Origin, "")
	if c.Response.StatusCode() != 404 {
		t.Fatal("unknown route exposed")
	}
	c = request(r, "POST", "/api/accounts/create", `{"username":"alice","role":"admin"}`, v.Token, a.HTTP.Origin, "")
	if c.Response.StatusCode() != 400 {
		t.Fatal("accepted role injection")
	}
	if _, e := a.HTTP.Service.CreateAccount(context.Background(), v.Principal, "alice", ""); e != nil {
		t.Fatal(e)
	}
	member, e := a.HTTP.Service.Login(context.Background(), "alice", "123456", "peer")
	if e != nil {
		t.Fatal(e)
	}
	c = request(r, "POST", "/api/protected", "{}", member.Token, a.HTTP.Origin, "")
	if c.Response.StatusCode() != 403 {
		t.Fatal("member elevated")
	}
	c = request(r, "POST", "/api/session/logout", "", v.Token, a.HTTP.Origin, "")
	if c.Response.StatusCode() != 200 {
		t.Fatal("legacy empty logout failed")
	}
	if _, e = a.HTTP.Service.Authenticate(context.Background(), v.Token); e != identity.ErrUnauthorized {
		t.Fatal("logout failed to revoke")
	}
}
func TestConfigurationProjection(t *testing.T) {
	a, v := testAdapter(t)
	r := router.New()
	m := a.APIMiddleware()
	called := false
	r.GET("/api/config", m(func(c *fasthttp.RequestCtx) {
		identityhttp.JSON(c, 200, map[string]any{"auth_config": map[string]any{"is_enabled": false}, "client_config": map[string]any{"whitelisted_routes": []string{"/api/*"}}})
	}))
	r.PUT("/api/config", m(func(c *fasthttp.RequestCtx) {
		called = true
		var q map[string]any
		json.Unmarshal(c.PostBody(), &q)
		if _, ok := q["auth_config"]; ok {
			t.Error("legacy auth was forwarded")
		}
		c.SetStatusCode(200)
	}))
	c := request(r, "GET", "/api/config", "", v.Token, "", "")
	if c.Response.StatusCode() != 200 {
		t.Fatal("config read")
	}
	body := string(c.Response.Body())
	c = request(r, "PUT", "/api/config", body, v.Token, a.HTTP.Origin, "")
	if c.Response.StatusCode() != 200 || !called {
		t.Fatal("same projection rejected")
	}
	for _, body := range []string{`{"auth_config":{"is_enabled":false},"client_config":{}}`, `{"client_config":{"whitelisted_routes":["*"]}}`, `{"AUTH_CONFIG":{"is_enabled":false}}`, `{"client_config":{"Whitelisted_Routes":["*"]}}`, `{"auth_config":{},"auth_config":null}`} {
		called = false
		c = request(r, "PUT", "/api/config", body, v.Token, a.HTTP.Origin, "")
		if c.Response.StatusCode() < 400 || called {
			t.Fatal("configuration bypass/partial write")
		}
	}
}
func TestCookieAndStrictJSON(t *testing.T) {
	a, _ := testAdapter(t)
	r := router.New()
	a.RegisterSessionRoutes(r, a.APIMiddleware())
	c := request(r, "POST", "/api/identity/login", `{"username":"admin","password":"Admin-password-1"}`, "", a.HTTP.Origin, "")
	if c.Response.StatusCode() != 200 {
		t.Fatal("login failed", c.Response.StatusCode())
	}
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey(identityhttp.CookieName)
	if !c.Response.Header.Cookie(cookie) || !cookie.HTTPOnly() || !cookie.Secure() || cookie.SameSite() != fasthttp.CookieSameSiteLaxMode {
		t.Fatal("unsafe cookie")
	}
	for _, body := range []string{`{"username":"admin","username":"other","password":"bad"}`, `{} {}`, `null`, `[]`} {
		c = request(r, "POST", "/api/identity/login", body, "", a.HTTP.Origin, "")
		if c.Response.StatusCode() != 400 {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestOriginConflictAndTicketRedaction(t *testing.T) {
	a, v := testAdapter(t)
	for _, tc := range []struct {
		origin, referer string
		ok              bool
	}{
		{a.HTTP.Origin, "https://evil.test/page", false}, {"https://evil.test", a.HTTP.Origin + "/page", false}, {"", a.HTTP.Origin + "/page", true}, {"", "", false},
	} {
		var c fasthttp.RequestCtx
		c.Init(&fasthttp.Request{}, nil, nil)
		c.Request.Header.Set("Origin", tc.origin)
		c.Request.Header.Set("Referer", tc.referer)
		if a.HTTP.SameOrigin(&c) != tc.ok {
			t.Fatal("ambiguous origin accepted")
		}
	}
	r := router.New()
	m := a.APIMiddleware()
	r.GET("/ws", m(func(c *fasthttp.RequestCtx) { c.SetStatusCode(204) }))
	ticket, e := a.HTTP.Service.IssueTicket(context.Background(), v.Principal)
	if e != nil {
		t.Fatal(e)
	}
	c := request(r, "GET", "/ws?ticket="+ticket, "", "", a.HTTP.Origin, "")
	if c.Response.StatusCode() != 204 {
		t.Fatal("ticket rejected")
	}
	if c.QueryArgs().Has("ticket") {
		t.Fatal("access log would include ticket")
	}
	c = request(r, "GET", "/ws?token=legacy-secret&ticket="+ticket, "", "", "https://evil.test", "")
	if c.QueryArgs().Has("token") || c.QueryArgs().Has("ticket") {
		t.Fatal("rejected handshake leaks URL credentials")
	}
}

func TestTemporaryTokenScopes(t *testing.T) {
	a, admin := testAdapter(t)
	ctx := context.Background()
	store, e := configstore.NewConfigStore(ctx, &configstore.Config{Enabled: true, Type: configstore.ConfigStoreTypeSQLite, Config: &configstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "host.db")}}, core.NewDefaultLogger(schemas.LogLevelError))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close(ctx)
	cfg := &configstore.ClientConfig{MCPEnableTempTokenAuth: true}
	if e = store.UpdateClientConfig(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	tokens := temptoken.NewService(store, temptoken.NewRegistry())
	if e = handlers.RegisterTempTokenScopes(tokens); e != nil {
		t.Fatal(e)
	}
	a.host = &server.BifrostHTTPServer{Config: &lib.Config{ConfigStore: store}, TempTokens: tokens}
	if _, e = a.HTTP.Service.CreateAccount(ctx, admin.Principal, "member", ""); e != nil {
		t.Fatal(e)
	}
	member, e := a.HTTP.Service.Login(ctx, "member", "123456", "peer")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		scope, path string
		methods     []string
	}{
		{temptoken.MCPAuthScopeName, "/api/oauth/per-user/flows/flow1", []string{"GET"}},
		{temptoken.MCPAuthScopeName, "/api/oauth/per-user/flows/flow1/start", []string{"GET"}},
		{temptoken.MCPHeadersAuthScopeName, "/api/mcp/per-user-headers/flows/flow1", []string{"GET", "PUT"}},
		{temptoken.OAuth2ConsentScopeName, "/api/oauth2/consent/flows/flow1", []string{"GET", "PUT"}},
	} {
		token, e := tokens.Mint(ctx, tc.scope, "flow1", time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		for _, method := range tc.methods {
			for _, cookie := range []string{member.Token, "expired-cookie"} {
				var c fasthttp.RequestCtx
				c.Init(&fasthttp.Request{}, nil, nil)
				c.Request.SetRequestURI(tc.path)
				c.Request.Header.SetMethod(method)
				c.Request.Header.SetCookie(identityhttp.CookieName, cookie)
				c.Request.Header.Set("X-Bifrost-Temp-Token", token)
				a.APIMiddleware()(func(c *fasthttp.RequestCtx) {
					if c.UserValue(schemas.IsLocalAdminContextKey) == true || c.UserValue(principalKey{}) != nil {
						t.Error("temp token inherited chief")
					}
					if c.UserValue(schemas.BifrostContextKeyTempTokenResourceID) != "flow1" {
						t.Error("flow binding missing")
					}
					c.SetStatusCode(204)
				})(&c)
				if c.Response.StatusCode() != 204 {
					t.Fatalf("scope %s method %s rejected", tc.scope, method)
				}
			}
		}
		for _, path := range []string{"/api/config", tc.path + "/near-miss", "/api/oauth/per-user/flows/another"} {
			var c fasthttp.RequestCtx
			c.Init(&fasthttp.Request{}, nil, nil)
			c.Request.SetRequestURI(path)
			c.Request.Header.SetMethod("POST")
			c.Request.Header.Set("X-Bifrost-Temp-Token", token)
			a.APIMiddleware()(func(c *fasthttp.RequestCtx) { t.Error("scope escaped") })(&c)
			if c.Response.StatusCode() != 401 {
				t.Fatal("invalid scope was not rejected")
			}
		}
	}
	cfg.MCPEnableTempTokenAuth = false
	store.UpdateClientConfig(ctx, cfg)
	token, e := tokens.Mint(ctx, temptoken.MCPAuthScopeName, "flow1", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	var c fasthttp.RequestCtx
	c.Init(&fasthttp.Request{}, nil, nil)
	c.Request.SetRequestURI("/api/oauth/per-user/flows/flow1")
	c.Request.Header.SetMethod("GET")
	c.Request.Header.Set("X-Bifrost-Temp-Token", token)
	a.APIMiddleware()(func(c *fasthttp.RequestCtx) { t.Error("disabled temp auth accepted") })(&c)
	if c.Response.StatusCode() != 401 {
		t.Fatal("temp auth flag ignored")
	}
}
