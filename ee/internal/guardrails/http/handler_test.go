// 本文件验证配置接口的鉴权、校验、版本冲突、热替换与锁定。
package guardrailshttp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	"github.com/fasthttp/router"
	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeBuilder struct{ fail error }

func (b fakeBuilder) Build(_ context.Context, cfg config.Config) (*guardrails.Checker, error) {
	if b.fail != nil {
		return nil, b.fail
	}
	return guardrails.New(nil, nil, cfg.MaxTextBytes)
}

type fakeSwapper struct {
	mu    sync.Mutex
	swaps []*guardrails.Checker
}

func (s *fakeSwapper) Swap(c *guardrails.Checker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.swaps = append(s.swaps, c)
}

const validConfig = `{"deny":{"status":400,"message":"no"},"secrets":{"enabled":true,"stages":["input"],"threshold":"high","on_match":"block","on_error":"block"}}`

func setup(t *testing.T, builder Builder, locked Locked) (*router.Router, *fakeSwapper) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	if err := persistence.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	swapper := &fakeSwapper{}
	h, err := NewHandler(persistence.NewStore(db), builder, swapper, locked)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	am := &upstream.AuthMiddleware{}
	if err := h.RegisterRoutes(r, am.APIMiddleware()); err != nil {
		t.Fatal(err)
	}
	return r, swapper
}

func call(t *testing.T, r *router.Router, path, body string) (int, map[string]any) {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	var out map[string]any
	if len(ctx.Response.Body()) > 0 {
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatalf("body %s", ctx.Response.Body())
		}
	}
	return ctx.Response.StatusCode(), out
}

func TestLifecycle(t *testing.T) {
	r, swapper := setup(t, fakeBuilder{}, nil)
	status, body := call(t, r, "/api/guardrails/get", "")
	if status != 200 || body["config"] != nil || body["version"].(float64) != 0 {
		t.Fatalf("empty get: %d %v", status, body)
	}
	status, body = call(t, r, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`)
	if status != 200 || body["version"].(float64) != 1 || len(swapper.swaps) != 1 || swapper.swaps[0] == nil {
		t.Fatalf("update: %d %v swaps=%d", status, body, len(swapper.swaps))
	}
	status, _ = call(t, r, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`)
	if status != fasthttp.StatusConflict || len(swapper.swaps) != 1 {
		t.Fatalf("stale version: %d swaps=%d", status, len(swapper.swaps))
	}
	status, body = call(t, r, "/api/guardrails/get", "")
	if status != 200 || body["version"].(float64) != 1 || body["config"].(map[string]any)["deny"] == nil {
		t.Fatalf("get: %d %v", status, body)
	}
	status, body = call(t, r, "/api/guardrails/reset", "")
	if status != 200 || body["version"].(float64) != 0 || len(swapper.swaps) != 2 || swapper.swaps[1] != nil {
		t.Fatalf("reset: %d %v swaps=%v", status, body, swapper.swaps)
	}
}

func TestRejectionsDoNotSwap(t *testing.T) {
	r, swapper := setup(t, fakeBuilder{}, nil)
	for name, body := range map[string]string{
		"not json":        "{",
		"unknown field":   `{"config":` + validConfig + `,"version":0,"x":1}`,
		"missing config":  `{"version":0}`,
		"invalid config":  `{"config":{"deny":{"status":200,"message":"x"}},"version":0}`,
		"negative":        `{"config":` + validConfig + `,"version":-1}`,
		"trailing object": `{"config":` + validConfig + `,"version":0}{}`,
		"too large":       `{"config":` + validConfig + `,"version":0,"pad":"` + strings.Repeat("x", maxBodyBytes) + `"}`,
	} {
		if status, _ := call(t, r, "/api/guardrails/update", body); status != fasthttp.StatusBadRequest {
			t.Fatalf("%s: status %d", name, status)
		}
	}
	if len(swapper.swaps) != 0 {
		t.Fatal("rejected update swapped the checker")
	}
	failing, swapper := setup(t, fakeBuilder{fail: errors.New("judge provider \"x\" is not configured")}, nil)
	if status, body := call(t, failing, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`); status != fasthttp.StatusBadRequest || !strings.Contains(body["error"].(map[string]any)["message"].(string), "not configured") {
		t.Fatalf("build failure: %d %v", status, body)
	}
	if status, body := call(t, failing, "/api/guardrails/get", ""); status != 200 || body["config"] != nil {
		t.Fatal("failed build must not persist")
	}
	if len(swapper.swaps) != 0 {
		t.Fatal("failed build swapped the checker")
	}
}

func TestLockedUnderFakeMode(t *testing.T) {
	r, swapper := setup(t, fakeBuilder{}, func() bool { return true })
	if status, _ := call(t, r, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`); status != fasthttp.StatusConflict || len(swapper.swaps) != 0 {
		t.Fatalf("locked update: %d", status)
	}
	if status, _ := call(t, r, "/api/guardrails/get", ""); status != 200 {
		t.Fatalf("locked get: %d", status)
	}
}

func TestRequiresAuthAndDeps(t *testing.T) {
	if _, err := NewHandler(nil, fakeBuilder{}, &fakeSwapper{}, nil); err == nil {
		t.Fatal("nil store accepted")
	}
	h, _ := NewHandler(persistence.NewStore(nil), fakeBuilder{}, &fakeSwapper{}, nil)
	if err := h.RegisterRoutes(router.New(), nil); err == nil {
		t.Fatal("nil auth accepted")
	}
}
