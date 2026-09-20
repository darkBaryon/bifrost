// 本文件验证配置接口的鉴权、校验、版本冲突、热替换与锁定。
package guardrailshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	safetyplugin "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
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
	mu      sync.Mutex
	swaps   []*guardrails.Checker
	options []safetyplugin.Options
	// gate 非 nil 时，只有第一次 Swap 先发出 entered 再等待 gate，把"已写库、未发布"这一刻固定住；之后的 Swap 不阻塞。
	entered chan struct{}
	gate    chan struct{}
	gated   bool
	fail    error
}

func (s *fakeSwapper) Swap(c *guardrails.Checker, o safetyplugin.Options) error {
	s.mu.Lock()
	first := s.gate != nil && !s.gated
	if first {
		s.gated = true
	}
	s.mu.Unlock()
	if first {
		s.entered <- struct{}{}
		<-s.gate
	}
	if s.fail != nil {
		return s.fail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.swaps = append(s.swaps, c)
	s.options = append(s.options, o)
	return nil
}

type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Error(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}

const validConfig = `{"deny":{"status":451,"message":"自定义拒绝"},"secrets":{"enabled":true,"stages":["input"],"threshold":"high","on_match":"block","on_error":"block"}}`

func setup(t *testing.T, builder Builder, locked Locked) (*router.Router, *fakeSwapper, *testLogger) {
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
	log := &testLogger{}
	h, err := NewHandler(persistence.NewStore(db), builder, swapper, log, locked)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	am := &upstream.AuthMiddleware{}
	if err := h.RegisterRoutes(r, am.APIMiddleware()); err != nil {
		t.Fatal(err)
	}
	return r, swapper, log
}

// tryCall 发一次请求并解析 JSON；不调用 t.Fatalf，可在工作 goroutine 中使用。
func tryCall(r *router.Router, path, body string) (int, map[string]any, error) {
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
			return ctx.Response.StatusCode(), nil, fmt.Errorf("body %s: %w", ctx.Response.Body(), err)
		}
	}
	return ctx.Response.StatusCode(), out, nil
}

func call(t *testing.T, r *router.Router, path, body string) (int, map[string]any) {
	t.Helper()
	status, out, err := tryCall(r, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, out
}

func TestLifecycle(t *testing.T) {
	r, swapper, _ := setup(t, fakeBuilder{}, nil)
	status, body := call(t, r, "/api/guardrails/get", "")
	if status != 200 || body["config"] != nil || body["version"].(float64) != 0 {
		t.Fatalf("empty get: %d %v", status, body)
	}
	status, body = call(t, r, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`)
	if status != 200 || body["version"].(float64) != 1 || len(swapper.swaps) != 1 || swapper.swaps[0] == nil {
		t.Fatalf("update: %d %v swaps=%d", status, body, len(swapper.swaps))
	}
	if swapper.options[0] != (safetyplugin.Options{StatusCode: 451, DenyMessage: "自定义拒绝"}) {
		t.Fatalf("deny options not published with the checker: %+v", swapper.options[0])
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
	if status != 200 || body["version"].(float64) != 0 || len(swapper.swaps) != 2 || swapper.swaps[1] != nil || swapper.options[1] != (safetyplugin.Options{}) {
		t.Fatalf("reset: %d %v swaps=%v", status, body, swapper.swaps)
	}
}

func TestRejectionsDoNotSwap(t *testing.T) {
	r, swapper, _ := setup(t, fakeBuilder{}, nil)
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
	failing, swapper, log := setup(t, fakeBuilder{fail: errors.New("judge provider \"x\" is not configured")}, nil)
	if status, body := call(t, failing, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`); status != fasthttp.StatusBadRequest || !strings.Contains(body["error"].(map[string]any)["message"].(string), "not configured") {
		t.Fatalf("build failure: %d %v", status, body)
	}
	if status, body := call(t, failing, "/api/guardrails/get", ""); status != 200 || body["config"] != nil {
		t.Fatal("failed build must not persist")
	}
	if len(swapper.swaps) != 0 {
		t.Fatal("failed build swapped the checker")
	}
	if len(log.lines) != 1 || !strings.Contains(log.lines[0], "not configured") {
		t.Fatalf("build failure not logged: %v", log.lines)
	}
}

// 存储故障：对外通用错误，服务端日志有操作与原因。
func TestStorageFailureIsLogged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "broken.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// 未迁移的库：表不存在，读写都会失败。
	swapper, log := &fakeSwapper{}, &testLogger{}
	h, err := NewHandler(persistence.NewStore(db), fakeBuilder{}, swapper, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	broken := router.New()
	am := &upstream.AuthMiddleware{}
	if err := h.RegisterRoutes(broken, am.APIMiddleware()); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(t, broken, "/api/guardrails/get", ""); status != fasthttp.StatusInternalServerError {
		t.Fatalf("get on broken store: %d", status)
	}
	if status, _ := call(t, broken, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`); status != fasthttp.StatusInternalServerError {
		t.Fatalf("update on broken store: %d", status)
	}
	if len(swapper.swaps) != 0 {
		t.Fatal("storage failure swapped the checker")
	}
	joined := strings.Join(log.lines, "\n")
	if !strings.Contains(joined, "storage read failed") || !strings.Contains(joined, "storage update failed") || !strings.Contains(joined, "no such table") {
		t.Fatalf("storage failures not diagnosable: %v", log.lines)
	}
}

// reset 发布失败与 update 一样报 500 并记日志。
func TestResetSwapFailureIsReported(t *testing.T) {
	r, swapper, log := setup(t, fakeBuilder{}, nil)
	swapper.fail = errors.New("swap broken")
	if status, _ := call(t, r, "/api/guardrails/reset", ""); status != fasthttp.StatusInternalServerError || !strings.Contains(strings.Join(log.lines, ""), "swap failed after reset") {
		t.Fatalf("reset swap failure not reported: %d %v", status, log.lines)
	}
}

type outcome struct {
	status int
	err    error
}

// update 与 reset 从写库到发布共用一把锁：第一方停在"已写库、未发布"时，第二方不得已经改动库；
// 第一方发布后第二方才执行，最终库中状态与最后一次发布一致。去掉 Handler 的锁，第二方会在第一方阻塞期间写库，本用例失败。
func TestUpdateAndResetAreSerialized(t *testing.T) {
	const (
		holdWindow     = 200 * time.Millisecond // 第一方持锁期间观察"预期不发生"的窗口
		barrierTimeout = 5 * time.Second        // 合法阻塞的请求最长允许多久返回
	)
	updateBody := `{"config":` + validConfig + `,"version":0}`
	for _, first := range []string{"update", "reset"} {
		t.Run(first+" first", func(t *testing.T) {
			r, swapper, _ := setup(t, fakeBuilder{}, nil)
			// 默认 update 先行、reset 第二；reset 先行时先播种并整体对调角色。
			firstPath, firstBody := "/api/guardrails/update", updateBody
			second, secondPath, secondBody := "reset", "/api/guardrails/reset", ""
			if first == "reset" {
				if status, _ := call(t, r, "/api/guardrails/update", updateBody); status != 200 {
					t.Fatal("seed update failed")
				}
				firstPath, firstBody = "/api/guardrails/reset", ""
				second, secondPath, secondBody = "update", "/api/guardrails/update", updateBody
			}
			swapper.entered, swapper.gate = make(chan struct{}, 1), make(chan struct{})
			run := func(path, body string) chan outcome {
				done := make(chan outcome, 1)
				go func() {
					status, _, err := tryCall(r, path, body)
					done <- outcome{status, err}
				}()
				return done
			}
			firstDone := run(firstPath, firstBody)
			select {
			case <-swapper.entered: // 第一方已写库，停在发布前
			case <-time.After(barrierTimeout):
				t.Fatal("first request never reached publish")
			}
			secondDone := run(secondPath, secondBody)
			select {
			case o := <-secondDone:
				t.Fatalf("second request (%s) completed while the first held the persist-and-publish lock: %+v", second, o)
			case <-time.After(holdWindow):
			}
			// 第一方仍持锁：库里必须还是第一方写入后的状态，第二方不得已经写库。
			_, body := call(t, r, "/api/guardrails/get", "")
			if configured := body["config"] != nil; configured != (first == "update") {
				t.Fatalf("second request (%s) wrote to the store while the first held the lock: %v", second, body)
			}
			swapper.gate <- struct{}{} // 放行第一方发布
			for _, done := range []chan outcome{firstDone, secondDone} {
				select {
				case o := <-done:
					if o.err != nil || o.status != 200 {
						t.Fatalf("request outcome %+v", o)
					}
				case <-time.After(barrierTimeout):
					t.Fatal("request did not finish after the lock was released")
				}
			}
			_, body = call(t, r, "/api/guardrails/get", "")
			swapper.mu.Lock()
			defer swapper.mu.Unlock()
			last := swapper.swaps[len(swapper.swaps)-1]
			if first == "update" { // 顺序 update → reset：最终未配置且最后发布为 nil
				if body["config"] != nil || last != nil || len(swapper.swaps) != 2 {
					t.Fatalf("expected reset to win: body=%v swaps=%d last=%v", body, len(swapper.swaps), last)
				}
			} else { // 顺序 reset → update：最终已配置且最后发布为新检查器
				if body["config"] == nil || body["version"].(float64) != 1 || last == nil {
					t.Fatalf("expected update to win: body=%v last=%v", body, last)
				}
			}
		})
	}
}

func TestLockedUnderFakeMode(t *testing.T) {
	r, swapper, _ := setup(t, fakeBuilder{}, func() bool { return true })
	if status, _ := call(t, r, "/api/guardrails/update", `{"config":`+validConfig+`,"version":0}`); status != fasthttp.StatusConflict || len(swapper.swaps) != 0 {
		t.Fatalf("locked update: %d", status)
	}
	if status, _ := call(t, r, "/api/guardrails/get", ""); status != 200 {
		t.Fatalf("locked get: %d", status)
	}
}

func TestRequiresAuthAndDeps(t *testing.T) {
	if _, err := NewHandler(nil, fakeBuilder{}, &fakeSwapper{}, &testLogger{}, nil); err == nil {
		t.Fatal("nil store accepted")
	}
	if _, err := NewHandler(persistence.NewStore(nil), fakeBuilder{}, &fakeSwapper{}, nil, nil); err == nil {
		t.Fatal("nil logger accepted")
	}
	h, _ := NewHandler(persistence.NewStore(nil), fakeBuilder{}, &fakeSwapper{}, &testLogger{}, nil)
	if err := h.RegisterRoutes(router.New(), nil); err == nil {
		t.Fatal("nil auth accepted")
	}
}
