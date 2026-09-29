// 本文件验证HTTP边界与对外契约，不从生产路由表生成预期值。
package identityhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestAccountRoutesExcludeAuthentication(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name     string
		register func(*router.Router)
		post     []string
		get      []string
	}{
		{
			name:     "identity",
			register: func(r *router.Router) { h.RegisterRoutes(r) },
			post: []string{
				"/api/identity/initialize",
				"/api/identity/change-password", "/api/identity/password-events",
				"/api/accounts/create", "/api/accounts/delete", "/api/accounts/list", "/api/accounts/set-status", "/api/accounts/reset-password",
			},
		},
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
				t.Fatalf("unexpected route methods: %v", actual)
			}
		})
	}
}

func TestErrorResponseContract(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{identity.ErrInvalid, 400, "invalid_input"},
		{identity.ErrUnauthorized, 401, "unauthorized"},
		{identity.ErrForbidden, 403, "forbidden"},
		{identity.ErrNotFound, 404, "not_found"},
		{identity.ErrConflict, 409, "conflict"},
		{identity.ErrLimited, 429, "rate_limited"},
		{auth.ErrUnauthorized, 401, "unauthorized"},
		{auth.ErrLimited, 429, "rate_limited"},
		{fmt.Errorf("private context: %w", auth.ErrForbidden), 403, "forbidden"},
		{session.ErrUnauthorized, 401, "unauthorized"},
		{session.ErrInvalid, 400, "invalid_input"},
		{session.ErrUnavailable, 503, "unavailable"},
		{auth.Error("private database details"), 503, "unavailable"},
		{errors.New("unauthorized"), 503, "unavailable"},
		{errors.New("private database details"), 503, "unavailable"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			var c fasthttp.RequestCtx
			writeError(&c, tt.err)
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
				t.Fatal(err)
			}
			if c.Response.StatusCode() != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.code {
				t.Fatal("error contract changed", c.Response.StatusCode(), string(c.Response.Body()))
			}
			retry := string(c.Response.Header.Peek("Retry-After"))
			if tt.status == 429 && retry != "60" {
				t.Fatal("retry window contract changed", retry)
			}
			if tt.status != 429 && retry != "" {
				t.Fatal("unexpected retry header")
			}
		})
	}
}
