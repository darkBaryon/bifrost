// 本文件固定基础状态的来源、会话和严格空对象协议，所有结果均禁止缓存。
package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

func consoleRequest(handler fasthttp.RequestHandler, method, path, body, token, origin string, headers map[string]string) *fasthttp.RequestCtx {
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
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	c := &fasthttp.RequestCtx{}
	c.Init(req, nil, nil)
	handler(c)
	return c
}

func assertConsoleStatus(t *testing.T, c *fasthttp.RequestCtx, status int) {
	t.Helper()
	if c.Response.StatusCode() != status {
		t.Fatalf("status=%d want=%d body=%s", c.Response.StatusCode(), status, c.Response.Body())
	}
	if string(c.Response.Header.Peek("Cache-Control")) != "no-store" {
		t.Fatal("response can be cached")
	}
	id := string(c.Response.Header.Peek("X-Request-ID"))
	if id == "" || id != string(c.Request.Header.Peek("X-Request-ID")) {
		t.Fatal("missing diagnostic correlation")
	}
	if status >= 400 {
		code := map[int]string{400: "invalid_input", 401: "unauthorized", 403: "forbidden", 503: "unavailable"}[status]
		if !strings.Contains(string(c.Response.Body()), `"code":"`+code+`"`) {
			t.Fatalf("unexpected identity error: %s", c.Response.Body())
		}
	}
}

func TestConsoleBootstrapProtocol(t *testing.T) {
	a, admin, _ := testAdapter(t)
	a.host = &server.BifrostHTTPServer{Config: &lib.Config{}}
	r := router.New()
	a.RegisterSessionRoutes(r, a.APIMiddleware())
	for _, tc := range []struct {
		name, body, token, origin string
		headers                   map[string]string
		status                    int
	}{
		{name: "valid", body: "{}", token: admin.Token, origin: a.http.Origin(), status: 200},
		{name: "referer fallback", body: "{}", token: admin.Token, headers: map[string]string{"Referer": a.http.Origin() + "/workspace"}, status: 200},
		{name: "anonymous", body: "{}", origin: a.http.Origin(), status: 401},
		{name: "invalid session", body: "{}", token: strings.Repeat("x", 43), origin: a.http.Origin(), status: 401},
		{name: "missing origin precedes auth", body: "{}", status: 403},
		{name: "wrong origin", body: "{}", token: admin.Token, origin: "https://evil.test", status: 403},
		{name: "conflicting referer", body: "{}", token: admin.Token, origin: a.http.Origin(), headers: map[string]string{"Referer": "https://evil.test/path"}, status: 403},
		{name: "conflicting origin", body: "{}", token: admin.Token, origin: "https://evil.test", headers: map[string]string{"Referer": a.http.Origin() + "/path"}, status: 403},
		{name: "authorization despite session", body: "{}", token: admin.Token, origin: a.http.Origin(), headers: map[string]string{"Authorization": "Bearer virtual-key"}, status: 401},
		{name: "basic authentication", body: "{}", origin: a.http.Origin(), headers: map[string]string{"Authorization": "Basic old-admin"}, status: 401},
		{name: "temporary token", body: "{}", origin: a.http.Origin(), headers: map[string]string{"X-Bifrost-Temp-Token": "temporary"}, status: 401},
		{name: "virtual key", body: "{}", origin: a.http.Origin(), headers: map[string]string{"x-bf-vk": "virtual-key"}, status: 401},
		{name: "old cookie", body: "{}", origin: a.http.Origin(), headers: map[string]string{"Cookie": "token=legacy"}, status: 401},
		{name: "auth precedes decode", body: "null", origin: a.http.Origin(), status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertConsoleStatus(t, consoleRequest(r.Handler, "POST", "/api/console/bootstrap", tc.body, tc.token, tc.origin, tc.headers), tc.status)
		})
	}
	for _, tc := range []struct{ name, body, contentType string }{
		{"empty", "", "application/json"},
		{"whitespace", "  ", "application/json"},
		{"null", "null", "application/json"},
		{"array", "[]", "application/json"},
		{"scalar", `"text"`, "application/json"},
		{"unknown", `{"extra":true}`, "application/json"},
		{"duplicate", `{"extra":1,"extra":2}`, "application/json"},
		{"multiple", `{} {}`, "application/json"},
		{"trailing", `{} junk`, "application/json"},
		{"malformed", `{`, "application/json"},
		{"utf8", "{\"" + string([]byte{0xff}) + "\":0}", "application/json"},
		{"oversized", "{}" + strings.Repeat(" ", identityhttp.MaxBodyBytes-1), "application/json"},
		{"wrong media", `{}`, "text/plain"},
		{"missing media", `{}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", tc.body, admin.Token, a.http.Origin(), map[string]string{"Content-Type": tc.contentType})
			assertConsoleStatus(t, c, 400)
		})
	}
	c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}"+strings.Repeat(" ", identityhttp.MaxBodyBytes-2), admin.Token, a.http.Origin(), map[string]string{"Content-Type": "application/json; charset=utf-8"})
	assertConsoleStatus(t, c, 200)
}

type unavailableConsoleSession struct{ consoleSessions }

func (unavailableConsoleSession) Authenticate(context.Context, string) (identity.Principal, error) {
	return identity.Principal{}, errors.New("database-password-secret")
}

func TestConsoleBootstrapUnavailable(t *testing.T) {
	a, admin, _ := testAdapter(t)
	r := router.New()
	a.RegisterSessionRoutes(r, a.APIMiddleware())
	for _, h := range []*server.BifrostHTTPServer{nil, {}} {
		a.host = h
		c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}", admin.Token, a.http.Origin(), nil)
		assertConsoleStatus(t, c, 503)
	}
	a.host = &server.BifrostHTTPServer{Config: &lib.Config{}}
	a.service = unavailableConsoleSession{}
	c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}", admin.Token, a.http.Origin(), nil)
	assertConsoleStatus(t, c, 503)
	if strings.Contains(string(c.Response.Body()), "secret") {
		t.Fatal("storage error leaked")
	}
}

func TestConsoleBootstrapRevokedAndDisabledSessions(t *testing.T) {
	a, admin, svc := testAdapter(t)
	a.host = &server.BifrostHTTPServer{Config: &lib.Config{}}
	r := router.New()
	a.RegisterSessionRoutes(r, a.APIMiddleware())
	ctx := context.Background()
	for _, name := range []string{"revoked", "disabled"} {
		account, err := svc.Account.CreateAccount(ctx, admin.Principal, name, name)
		if err != nil {
			t.Fatal(err)
		}
		member, err := svc.Session.Login(ctx, name, "123456", "peer")
		if err != nil {
			t.Fatal(err)
		}
		c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}", member.Token, a.http.Origin(), nil)
		assertConsoleStatus(t, c, 200)
		if name == "revoked" {
			err = svc.Session.Logout(ctx, member.Token)
		} else {
			_, err = svc.Account.SetAccountStatus(ctx, admin.Principal, account.ID, identity.StatusDisabled)
		}
		if err != nil {
			t.Fatal(err)
		}
		c = consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}", member.Token, a.http.Origin(), nil)
		assertConsoleStatus(t, c, 401)
	}
}
