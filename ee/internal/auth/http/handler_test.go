// 本文件验证认证路由归属及新旧请求协议，不从生产路由表生成预期。
package authhttp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestAuthenticationAndLegacyRegistration(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name      string
		register  func(*router.Router)
		post, get []string
	}{
		{"authentication", func(r *router.Router) { h.RegisterRoutes(r) },
			[]string{"/api/identity/status", "/api/identity/login", "/api/identity/logout", "/api/identity/me", "/api/identity/ws-ticket"}, nil},
		{"legacy", func(r *router.Router) { h.RegisterSessionRoutes(r) },
			[]string{"/api/session/login", "/api/session/logout", "/api/session/ws-ticket"}, []string{"/api/session/is-auth-enabled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := router.New()
			tc.register(r)
			actual := r.List()
			for method, want := range map[string][]string{fasthttp.MethodPost: tc.post, fasthttp.MethodGet: tc.get} {
				got := slices.Clone(actual[method])
				slices.Sort(got)
				slices.Sort(want)
				if !slices.Equal(got, want) {
					t.Fatalf("%s routes=%v want=%v", method, got, want)
				}
				delete(actual, method)
			}
			if len(actual) != 0 {
				t.Fatalf("unexpected methods: %v", actual)
			}
		})
	}
}

type unusedTickets struct{}

func (unusedTickets) IssueTicket(context.Context, session.Principal) (string, error) {
	return "", errors.New("unexpected ticket request")
}

func TestHandlerRequiresExplicitDependencies(t *testing.T) {
	browser, err := NewBrowser("https://console.example")
	if err != nil {
		t.Fatal(err)
	}
	authentication := &auth.Service{}
	initialized := func(context.Context) (bool, error) { return true, nil }
	for _, tc := range []struct {
		name           string
		authentication *auth.Service
		tickets        TicketIssuer
		initialized    func(context.Context) (bool, error)
		browser        *Browser
	}{
		{"authentication", nil, unusedTickets{}, initialized, browser},
		{"tickets", authentication, nil, initialized, browser},
		{"initialization", authentication, unusedTickets{}, nil, browser},
		{"browser", authentication, unusedTickets{}, initialized, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewHandler(tc.authentication, tc.tickets, tc.initialized, tc.browser); !errors.Is(err, auth.ErrInvalid) {
				t.Fatalf("missing dependency: %v", err)
			}
		})
	}
}

func TestAnonymousEndpointCompatibility(t *testing.T) {
	browser, err := NewBrowser("https://console.example")
	if err != nil {
		t.Fatal(err)
	}
	var operation string
	h, err := NewHandler(&auth.Service{}, unusedTickets{}, func(ctx context.Context) (bool, error) {
		operation, _ = database.Operation(ctx)
		return true, nil
	}, browser)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	h.RegisterRoutes(r)
	h.RegisterSessionRoutes(r)
	for _, tc := range []struct {
		name, method, path, body, origin string
		status                           int
	}{
		{"new status", "POST", "/api/identity/status", "{}", "https://console.example", 200},
		{"new status requires JSON", "POST", "/api/identity/status", "", "https://console.example", 400},
		{"legacy GET status", "GET", "/api/session/is-auth-enabled", "", "", 200},
		{"new logout requires JSON", "POST", "/api/identity/logout", "", "https://console.example", 400},
		{"legacy logout empty", "POST", "/api/session/logout", "", "https://console.example", 200},
		{"legacy login requires JSON", "POST", "/api/session/login", "", "https://console.example", 400},
		{"unknown login field", "POST", "/api/identity/login", `{"username":"","password":"","extra":true}`, "https://console.example", 400},
		{"cross origin", "POST", "/api/identity/status", "{}", "https://evil.example", 403},
		{"missing origin", "POST", "/api/identity/status", "{}", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation = ""
			var c fasthttp.RequestCtx
			c.Request.Header.SetMethod(tc.method)
			c.Request.SetRequestURI(tc.path)
			if tc.body != "" {
				c.Request.Header.SetContentType("application/json")
				c.Request.SetBodyString(tc.body)
			}
			if tc.origin != "" {
				c.Request.Header.Set("Origin", tc.origin)
			}
			r.Handler(&c)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
			}
			if string(c.Response.Header.Peek("Cache-Control")) != "no-store" {
				t.Fatal("missing cache rule")
			}
			if len(c.Response.Header.Peek("X-Request-ID")) == 0 {
				t.Fatal("missing diagnostic ID")
			}
			if operation != "" && operation != "identity.status" {
				t.Fatalf("operation=%s", operation)
			}
			if tc.status == 200 && (tc.path == "/api/identity/status" || tc.method == "GET") {
				var status statusResponse
				if err := json.Unmarshal(c.Response.Body(), &status); err != nil {
					t.Fatal(err)
				}
				if !status.Initialized || status.AuthType != "password" || !status.IsAuthEnabled || status.HasValidToken || status.HasValidSession || status.MustChangePassword {
					t.Fatalf("unexpected anonymous status: %+v", status)
				}
			}
		})
	}
}

func TestProtectedEndpointsRejectAuthorizationWithoutCookie(t *testing.T) {
	browser, _ := NewBrowser("https://console.example")
	h, err := NewHandler(&auth.Service{}, unusedTickets{}, func(context.Context) (bool, error) { return true, nil }, browser)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	h.RegisterRoutes(r)
	h.RegisterSessionRoutes(r)
	for _, path := range []string{"/api/identity/me", "/api/identity/ws-ticket", "/api/session/ws-ticket"} {
		t.Run(path, func(t *testing.T) {
			var c fasthttp.RequestCtx
			c.Request.Header.SetMethod("POST")
			c.Request.SetRequestURI(path)
			c.Request.Header.Set("Origin", "https://console.example")
			c.Request.Header.Set("Authorization", "Bearer legacy-token")
			c.Request.Header.SetContentType("application/json")
			c.Request.SetBodyString("{}")
			r.Handler(&c)
			if c.Response.StatusCode() != 401 {
				t.Fatalf("status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
			}
		})
	}
}
