// 本文件验证启动时路由对账和未知API、方法、规范路径的拒绝。
package host

import (
	"crypto/sha256"
	"fmt"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"sort"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestRouteCoverageRejectsNewEndpoints(t *testing.T) {
	r := router.New()
	r.GET("/api/providers", func(*fasthttp.RequestCtx) {})
	a := &Adapter{}
	if e := a.VerifyRoutes(r); e != nil {
		t.Fatal(e)
	}
	r.GET("/api/new-feature", func(*fasthttp.RequestCtx) {})
	if e := a.VerifyRoutes(r); e == nil {
		t.Fatal("unknown API accepted")
	}
}

func TestRootGuardRejectsAPIFallbackAndAliases(t *testing.T) {
	r := router.New()
	r.GET("/api/providers", func(c *fasthttp.RequestCtx) { c.SetStatusCode(200) })
	r.GET("/{filepath:*}", func(c *fasthttp.RequestCtx) { c.SetStatusCode(200) })
	a := &Adapter{}
	if e := a.VerifyRoutes(r); e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/providers", 200}, {"GET", "/api/unknown", 404}, {"POST", "/api/providers", 405},
		{"GET", "/api/Providers", 404}, {"GET", "/api/providers/", 400}, {"GET", "/api//providers", 400},
		{"GET", "/api/%2fproviders", 400}, {"GET", "/api/%252fproviders", 400}, {"GET", "/api/../api/providers", 400},
		{"OPTIONS", "/api/unknown", 404},
	} {
		c := requestCtx(tt.method, tt.path, "")
		a.RootGuard(r.Handler)(c)
		if c.Response.StatusCode() != tt.status {
			t.Errorf("%s %s: %d want %d", tt.method, tt.path, c.Response.StatusCode(), tt.status)
		}
	}
}

func TestOptionalHandlerRegistrationProfiles(t *testing.T) {
	type registrar interface {
		RegisterRoutes(*router.Router, ...schemas.BifrostHTTPMiddleware)
	}
	profiles := []struct {
		name    string
		handler registrar
		count   int
		path    string
	}{
		{"logging", &handlers.LoggingHandler{}, 39, "/api/logs"},
		{"governance", &handlers.GovernanceHandler{}, 34, "/api/governance/virtual-keys"},
		{"prompts", &handlers.PromptsHandler{}, 21, "/api/prompt-repo/folders"},
		{"skills", &handlers.SkillsHandler{}, 11, "/api/skills"},
		{"plugins", &handlers.PluginsHandler{}, 7, "/api/plugins/builtins"},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				routes := router.New()
				if enabled {
					profile.handler.RegisterRoutes(routes)
				}
				adapter := NewAdapter(nil, nil, nil)
				if err := adapter.VerifyRoutes(routes); err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, paths := range routes.List() {
					count += len(paths)
				}
				want := 0
				if enabled {
					want = profile.count
				}
				if count != want {
					t.Fatalf("enabled=%v Router.List=%d want=%d", enabled, count, want)
				}
				c := requestCtx("GET", profile.path, "")
				// RootGuard alone must reject absent routes, and reach the owning middleware for present routes.
				adapter.RootGuard(func(c *fasthttp.RequestCtx) { c.SetStatusCode(204) })(c)
				status := 404
				if enabled {
					status = 204
				}
				if c.Response.StatusCode() != status {
					t.Fatalf("enabled=%v status=%d", enabled, c.Response.StatusCode())
				}
				t.Logf("enabled=%v actual Router.List entries=%d", enabled, count)
			}
		})
	}
}

// 摘要来自固定完整候选b38493b07，按完整范围独立计算；防止整理格式时遗漏或改错规则。
func TestRouteSourceDigest(t *testing.T) {
	var rows []string
	for _, r := range manifest() {
		var codes []string
		for _, p := range r.Permissions {
			codes = append(codes, string(p))
		}
		rows = append(rows, fmt.Sprintf("%s %s|%s|%s|%s|%s", r.Method, r.Pattern, r.Kind, strings.Join(codes, ","), r.Guard, r.Projection))
	}
	sort.Strings(rows)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(rows, "\n")+"\n")))
	if len(rows) != 817 || digest != "b5c8bb41457e87e0717855df78067515e578c4315c7a429e69105ec6679e596d" {
		t.Fatalf("route source drift: count=%d digest=%s", len(rows), digest)
	}
}
func TestInvalidRouteRules(t *testing.T) {
	for _, raw := range []string{
		"", "GET /api/providers", "@ managed - none provider-safe\nGET /api/providers",
		"@ unknown - none protocol\nGET /api/providers", "@ managed Missing.View none provider-safe\nGET /api/providers",
		"@ managed ModelProvider.View future provider-safe\nGET /api/providers",
		"@ managed ModelProvider.View none future\nGET /api/providers",
		"@ legacy-admin ModelProvider.View none protocol\nGET /api/providers",
		"@ public - none protocol\nTRACE /api/providers", "@ public - none protocol\nGET /same\nGET /same",
	} {
		if _, err := decodeRouteManifest([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func TestAllPhasesUseRoleAuthorization(t *testing.T) {
	routes := router.New()
	paths := []string{"/api/providers", "/api/logs", "/api/plugins", "/api/config", "/api/webhooks", "/api/notifications", "/ws"}
	for _, p := range paths {
		routes.GET(p, func(*fasthttp.RequestCtx) {})
	}
	adapter := NewAdapter(nil, nil, nil)
	if err := adapter.VerifyRoutes(routes); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if entry, ok := adapter.match("GET", p); !ok || entry.Kind != kindManaged {
			t.Fatalf("wrong phase: %s", p)
		}
	}
}
