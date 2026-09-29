package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

type consoleTestConfigStore struct{ configstore.ConfigStore }

func (consoleTestConfigStore) GetAuthConfig(context.Context) (*configstore.AuthConfig, error) {
	return &configstore.AuthConfig{IsEnabled: true}, nil
}

func (consoleTestConfigStore) GetClientConfig(context.Context) (*configstore.ClientConfig, error) {
	return &configstore.ClientConfig{}, nil
}

func consoleDefaultAuth(t *testing.T) *handlers.AuthMiddleware {
	t.Helper()
	auth, err := handlers.InitAuthMiddleware(consoleTestConfigStore{}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func consoleTrace(name string, calls *[]string) schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			*calls = append(*calls, name+" before")
			next(ctx)
			*calls = append(*calls, name+" after")
		}
	}
}

func consoleRequest(r *router.Router, path string) *fasthttp.RequestCtx {
	ctx := new(fasthttp.RequestCtx)
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(path)
	r.Handler(ctx)
	return ctx
}

func TestPrepareConsoleRoutesCallbackConfiguresIndependentExtensions(t *testing.T) {
	var calls []string
	first, second := consoleTrace("first", &calls), consoleTrace("second", &calls)
	source := []schemas.BifrostHTTPMiddleware{first, second}
	registered := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &BifrostHTTPServer{Router: router.New(), AuthMiddleware: consoleDefaultAuth(t)}
	s.BeforeAPIRoutes = func(got context.Context, server *BifrostHTTPServer) error {
		if got != ctx || server != s {
			t.Fatal("callback did not receive the current context and server")
		}
		server.ConsoleMiddlewares = source
		server.RegisterConsoleSessionRoutes = func(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
			registered = true
			r.GET("/custom-session", lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
				calls = append(calls, "handler")
				c.SetStatusCode(fasthttp.StatusNoContent)
			}, m...))
		}
		return nil
	}
	m, err := s.prepareConsoleRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || len(source) != 2 {
		t.Fatalf("middleware lengths = %d/%d, want 2/2", len(m), len(source))
	}
	// The returned chain must not retain the embedder's mutable backing array.
	source[0] = consoleTrace("changed source", &calls)
	s.registerConsoleSessionRoutes(m...)
	response := consoleRequest(s.Router, "/custom-session")
	want := []string{"first before", "second before", "handler", "second after", "first after"}
	if !registered || response.Response.StatusCode() != fasthttp.StatusNoContent || !slices.Equal(calls, want) {
		t.Fatalf("registered=%v status=%d calls=%v, want %v", registered, response.Response.StatusCode(), calls, want)
	}
	// Mutating the returned chain must likewise leave the configured chain intact.
	source[0] = first
	m[1] = consoleTrace("changed result", &calls)
	calls = nil
	lib.ChainMiddlewares(func(*fasthttp.RequestCtx) { calls = append(calls, "handler") }, source...)(new(fasthttp.RequestCtx))
	if !slices.Equal(calls, want) {
		t.Fatalf("source chain was changed: calls=%v, want %v", calls, want)
	}
}

func TestPrepareConsoleRoutesStopsOnCallbackError(t *testing.T) {
	wantErr := errors.New("console setup failed")
	s := &BifrostHTTPServer{
		AuthMiddleware:  consoleDefaultAuth(t),
		BeforeAPIRoutes: func(context.Context, *BifrostHTTPServer) error { return wantErr },
	}
	m, err := s.prepareConsoleRoutes(context.Background())
	if len(m) != 0 || !errors.Is(err, wantErr) {
		t.Fatalf("middleware=%v error=%v, want no middleware and wrapped %v", m, err, wantErr)
	}
}

func TestConsoleOptionsRejectInvalidMiddlewares(t *testing.T) {
	for _, name := range []string{"empty chain", "nil middleware"} {
		t.Run(name, func(t *testing.T) {
			beforeCalls := 0
			s := &BifrostHTTPServer{
				BeforeAPIRoutes: func(context.Context, *BifrostHTTPServer) error {
					beforeCalls++
					return nil
				},
			}
			pass := schemas.BifrostHTTPMiddleware(func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next })
			switch name {
			case "empty chain":
				s.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{}
			case "nil middleware":
				s.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{pass, nil}
			}
			if _, err := s.prepareConsoleRoutes(context.Background()); err == nil {
				t.Fatal("invalid options were accepted")
			}
			// The public registration entry must reject the same options before
			// dereferencing Config or constructing any handlers.
			if err := s.RegisterAPIRoutes(context.Background(), nil); err == nil {
				t.Fatal("RegisterAPIRoutes accepted invalid options")
			}
			if beforeCalls != 0 {
				t.Fatalf("invalid options performed initialization: before=%d", beforeCalls)
			}
		})
	}
}

func TestPrepareConsoleRoutesValidatesCallbackChanges(t *testing.T) {
	for _, name := range []string{"empty chain", "nil middleware"} {
		t.Run(name, func(t *testing.T) {
			beforeCalled := false
			s := &BifrostHTTPServer{}
			s.BeforeAPIRoutes = func(_ context.Context, server *BifrostHTTPServer) error {
				beforeCalled = true
				switch name {
				case "empty chain":
					server.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{}
				case "nil middleware":
					server.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{nil}
				}
				return nil
			}
			if _, err := s.prepareConsoleRoutes(context.Background()); err == nil {
				t.Fatal("callback introduced invalid options without an error")
			}
			if !beforeCalled {
				t.Fatal("preparation did not run the callback")
			}
		})
	}
}

func TestPrepareConsoleRoutesDefaultAuth(t *testing.T) {
	auth := consoleDefaultAuth(t)
	for _, tc := range []struct {
		name       string
		auth       *handlers.AuthMiddleware
		enterprise bool
		custom     bool
		wantCount  int
		wantStatus int
	}{
		{name: "OSS auth", auth: auth, wantCount: 1, wantStatus: fasthttp.StatusUnauthorized},
		{name: "OSS without auth store", wantStatus: fasthttp.StatusNoContent},
		{name: "enterprise keeps existing behavior", auth: auth, enterprise: true, wantStatus: fasthttp.StatusNoContent},
		{name: "enterprise honors explicit middleware", auth: auth, enterprise: true, custom: true, wantCount: 1, wantStatus: fasthttp.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.enterprise {
				ctx = context.WithValue(ctx, schemas.BifrostContextKeyIsEnterprise, true)
			}
			s := &BifrostHTTPServer{AuthMiddleware: tc.auth, Router: router.New()}
			customCalled := false
			if tc.custom {
				s.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
					return func(c *fasthttp.RequestCtx) {
						customCalled = true
						next(c)
					}
				}}
			}
			m, err := s.prepareConsoleRoutes(ctx)
			if err != nil || len(m) != tc.wantCount {
				t.Fatalf("middleware count=%d error=%v, want %d", len(m), err, tc.wantCount)
			}
			s.Router.GET("/api/console-test", lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
				c.SetStatusCode(fasthttp.StatusNoContent)
			}, m...))
			if c := consoleRequest(s.Router, "/api/console-test"); c.Response.StatusCode() != tc.wantStatus {
				t.Fatalf("status=%d, want %d", c.Response.StatusCode(), tc.wantStatus)
			}
			if customCalled != tc.custom {
				t.Fatalf("explicit middleware called=%v, want %v", customCalled, tc.custom)
			}
		})
	}
}

func TestConsoleExtensionsCanBeOverriddenSeparately(t *testing.T) {
	for _, override := range []string{"middleware", "session routes"} {
		t.Run(override, func(t *testing.T) {
			s := &BifrostHTTPServer{
				Router:         router.New(),
				Config:         &lib.Config{ConfigStore: consoleTestConfigStore{}},
				AuthMiddleware: consoleDefaultAuth(t),
			}
			wantAPIStatus := fasthttp.StatusUnauthorized
			wantSessionBody := "custom session"
			if override == "middleware" {
				s.ConsoleMiddlewares = []schemas.BifrostHTTPMiddleware{func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
					return next
				}}
				wantAPIStatus = fasthttp.StatusNoContent
				wantSessionBody = `"auth_type":"password"`
			} else {
				s.RegisterConsoleSessionRoutes = func(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
					r.GET("/api/session/is-auth-enabled", lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
						c.SetBodyString("custom session")
					}, m...))
				}
			}
			m, err := s.prepareConsoleRoutes(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			s.registerConsoleSessionRoutes(m...)
			s.Router.GET("/api/console-test", lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
				c.SetStatusCode(fasthttp.StatusNoContent)
			}, m...))
			if c := consoleRequest(s.Router, "/api/console-test"); c.Response.StatusCode() != wantAPIStatus {
				t.Fatalf("API status=%d, want %d", c.Response.StatusCode(), wantAPIStatus)
			}
			c := consoleRequest(s.Router, "/api/session/is-auth-enabled")
			if c.Response.StatusCode() != fasthttp.StatusOK || !strings.Contains(string(c.Response.Body()), wantSessionBody) {
				t.Fatalf("session status=%d body=%s, want 200 containing %s", c.Response.StatusCode(), c.Response.Body(), wantSessionBody)
			}
		})
	}
}

func TestRegisterConsoleSessionRoutesSelectsOneOwner(t *testing.T) {
	const statusPath = "/api/session/is-auth-enabled"
	for _, owner := range []string{"custom", "OSS"} {
		t.Run(owner, func(t *testing.T) {
			var calls []string
			s := &BifrostHTTPServer{Router: router.New(), Config: &lib.Config{}}
			register := func(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
				r.GET(statusPath, lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
					calls = append(calls, owner)
					c.SetBodyString(owner)
				}, m...))
				r.GET("/"+owner+"-session", lib.ChainMiddlewares(func(c *fasthttp.RequestCtx) {
					c.SetBodyString(owner)
				}, m...))
			}
			if owner == "custom" {
				s.RegisterConsoleSessionRoutes = register
			}
			if _, err := s.prepareConsoleRoutes(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.registerConsoleSessionRoutes(consoleTrace("outer", &calls), consoleTrace("inner", &calls))
			c := consoleRequest(s.Router, statusPath)
			if c.Response.StatusCode() != fasthttp.StatusOK {
				t.Fatalf("session status=%d", c.Response.StatusCode())
			}
			want := []string{"outer before", "inner before"}
			if owner == "OSS" {
				if !strings.Contains(string(c.Response.Body()), `"auth_type":"none"`) {
					t.Fatalf("OSS session response=%s", c.Response.Body())
				}
			} else {
				want = append(want, owner)
				if string(c.Response.Body()) != owner {
					t.Fatalf("session response=%s, want %s", c.Response.Body(), owner)
				}
			}
			want = append(want, "inner after", "outer after")
			if !slices.Equal(calls, want) {
				t.Fatalf("session middleware calls=%v, want %v", calls, want)
			}
			wantStatus := fasthttp.StatusNotFound
			if owner == "custom" {
				wantStatus = fasthttp.StatusOK
			}
			if c := consoleRequest(s.Router, "/custom-session"); c.Response.StatusCode() != wantStatus {
				t.Fatalf("custom route status=%d, want %d", c.Response.StatusCode(), wantStatus)
			}
			// This sibling route is registered only by the real OSS handler.
			login := new(fasthttp.RequestCtx)
			login.Request.Header.SetMethod(fasthttp.MethodPost)
			login.Request.SetRequestURI("/api/session/login")
			s.Router.Handler(login)
			wantStatus = fasthttp.StatusNotFound
			if owner == "OSS" {
				wantStatus = fasthttp.StatusForbidden // ConfigStore is deliberately disabled.
			}
			if login.Response.StatusCode() != wantStatus {
				t.Fatalf("OSS login route status=%d, want %d", login.Response.StatusCode(), wantStatus)
			}
		})
	}
}

func TestRegisterConsoleSessionRoutesDoesNotExposeCallerSlice(t *testing.T) {
	var calls []string
	m := []schemas.BifrostHTTPMiddleware{consoleTrace("caller", &calls)}
	s := &BifrostHTTPServer{
		Router: router.New(),
		RegisterConsoleSessionRoutes: func(_ *router.Router, received ...schemas.BifrostHTTPMiddleware) {
			received[0] = consoleTrace("overwritten", &calls)
		},
	}
	s.registerConsoleSessionRoutes(m...)
	lib.ChainMiddlewares(func(*fasthttp.RequestCtx) { calls = append(calls, "handler") }, m...)(new(fasthttp.RequestCtx))
	want := []string{"caller before", "handler", "caller after"}
	if !slices.Equal(calls, want) {
		t.Fatalf("registrar changed the caller's middleware: calls=%v, want %v", calls, want)
	}
}
