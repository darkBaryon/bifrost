// 本文件逐个验证全部208个管理接口：最小权限可访问，缺少任一权限或数据库故障时不得执行原handler。
package host

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

type routeRepository struct {
	codes   []rbac.Permission
	failure error
}

func (s *routeRepository) Read(_ context.Context, fn func(rbac.Queries) error) error {
	if s.failure != nil {
		return s.failure
	}
	return fn(routeView{codes: s.codes})
}

func (*routeRepository) Write(context.Context, func(rbac.Tx) error) error { return rbac.ErrUnavailable }

type routeView struct {
	rbac.Queries
	codes []rbac.Permission
}

func (v routeView) Revalidate(rbac.Subject) (rbac.AccountInfo, error) { return v.Account("member") }

func (routeView) Account(string) (rbac.AccountInfo, error) {
	return rbac.AccountInfo{ID: "member", Active: true}, nil
}

func (v routeView) AccountRoles(string) ([]rbac.Role, error) {
	return []rbac.Role{{ID: 4, PermissionCodes: v.codes}}, nil
}

func TestEveryManagedRouteAuthorization(t *testing.T) {
	placeholders := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	for _, entry := range manifest() {
		if entry.Kind != kindManaged {
			continue
		}
		key := entry.Method + " " + entry.Pattern
		wanted := entry.Permissions
		checked++
		t.Run(key, func(t *testing.T) {
			repository := &routeRepository{codes: rbac.Permissions()}
			adapter := NewAdapter(rbac.New(repository), nil, nil)
			routes := router.New()
			called := 0
			routes.Handle(entry.Method, entry.Pattern, func(c *fasthttp.RequestCtx) {
				wrap, e := adapter.Prepare(context.Background(), c, identity.Principal{AccountID: "member"})
				if e != nil {
					if errors.Is(e, identity.ErrForbidden) {
						c.SetStatusCode(403)
					} else {
						c.SetStatusCode(503)
					}
					return
				}
				wrap(func(c *fasthttp.RequestCtx) { called++; c.SetStatusCode(204) })(c)
			})
			if e := adapter.VerifyRoutes(routes); e != nil {
				t.Fatal(e)
			}
			handler := adapter.RootGuard(routes.Handler)
			run := func(wantStatus int) {
				t.Helper()
				called = 0
				c := requestCtx(entry.Method, placeholders.ReplaceAllString(entry.Pattern, "example"), `{}`)
				handler(c)
				if c.Response.StatusCode() != wantStatus {
					t.Fatalf("status=%d want=%d body=%s", c.Response.StatusCode(), wantStatus, c.Response.Body())
				}
				if wantStatus == 204 && called != 1 || wantStatus != 204 && called != 0 {
					t.Fatal("authorization did not precede handler", called)
				}
			}
			// 普通入口仅授予合同最小集合；敏感操作的附加权限留给包装器测试。
			repository.codes = append([]rbac.Permission(nil), wanted...)
			minimal := requestCtx(entry.Method, placeholders.ReplaceAllString(entry.Pattern, "example"), `{}`)
			wrap, err := adapter.Prepare(context.Background(), minimal, identity.Principal{AccountID: "member"})
			if err != nil || wrap == nil {
				t.Fatalf("minimal ordinary permissions rejected: %v", err)
			}
			repository.codes = rbac.Permissions()
			run(204)
			for _, missing := range wanted {
				repository.codes = nil
				for _, code := range rbac.Permissions() {
					if code != missing {
						repository.codes = append(repository.codes, code)
					}
				}
				run(403)
			}
			repository.codes = rbac.Permissions()
			repository.failure = errors.New("driver-private-failure")
			run(503)
		})
	}
	if checked != 208 {
		t.Fatalf("managed coverage %d want %d", checked, 208)
	}
	t.Logf("verified %d managed method/pattern entries against the full phase-2/3/4 rules; pinned source comparison is in TestRouteSourceDigest", checked)
}

func TestKnownProtocolAndSelfManagedRoutesRemainOwned(t *testing.T) {
	placeholders := regexp.MustCompile(`\{[^}]+\}`)
	count := 0
	for _, entry := range manifest() {
		if entry.Kind == kindManaged {
			continue
		}
		t.Run(entry.Method+" "+entry.Pattern, func(t *testing.T) {
			routes := router.New()
			called := false
			routes.Handle(entry.Method, entry.Pattern, func(c *fasthttp.RequestCtx) { called = true; c.SetStatusCode(204) })
			adapter := NewAdapter(nil, nil, nil)
			if e := adapter.VerifyRoutes(routes); e != nil {
				t.Fatal(e)
			}
			c := requestCtx(entry.Method, placeholders.ReplaceAllString(entry.Pattern, "example"), "")
			c.Request.Header.Set("Authorization", "Bearer protocol-owned-token")
			adapter.RootGuard(routes.Handler)(c)
			if !called || c.Response.StatusCode() != 204 {
				t.Fatalf("root guard took over %s route", entry.Kind)
			}
		})
		count++
	}
	t.Logf("verified %d non-managed route registrations retain their owning handlers; provider execution is outside this test", count)
}

// /metrics使用Prometheus文本协议；仍要检查权限，但不能把响应当成JSON。
func TestMetricsAuthorizationPreservesText(t *testing.T) {
	repository := &routeRepository{codes: []rbac.Permission{rbac.LogsView}}
	adapter := NewAdapter(rbac.New(repository), nil, nil)
	routes := router.New()
	const body = "# HELP rbac_probe test metric\n# TYPE rbac_probe gauge\nrbac_probe 1\n"
	calls := 0
	routes.GET("/metrics", func(c *fasthttp.RequestCtx) {
		wrap, err := adapter.Prepare(context.Background(), c, identity.Principal{AccountID: "member"})
		if err != nil {
			if !errors.Is(err, identity.ErrForbidden) {
				t.Fatal(err)
			}
			c.SetStatusCode(403)
			return
		}
		wrap(func(c *fasthttp.RequestCtx) {
			calls++
			c.Response.Header.SetContentType("text/plain; version=0.0.4; charset=utf-8")
			c.Response.SetBodyString(body)
		})(c)
	})
	if err := adapter.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	c := requestCtx("GET", "/metrics", "")
	routes.Handler(c)
	if c.Response.StatusCode() != 200 || string(c.Response.Body()) != body || string(c.Response.Header.ContentType()) != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("metrics response changed: %s", c.Response.String())
	}
	repository.codes = nil
	c = requestCtx("GET", "/metrics", "")
	routes.Handler(c)
	if c.Response.StatusCode() != 403 || calls != 1 {
		t.Fatalf("revoked metrics reached handler: status=%d calls=%d", c.Response.StatusCode(), calls)
	}
}
