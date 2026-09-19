// 本文件用网关替身验证判官子请求的上下文与参数，以及配置到检查器的构建。
package host_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/host"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
)

type fakeClient struct {
	mu       sync.Mutex
	requests []*schemas.BifrostChatRequest
	skipped  []bool
	deadline []bool
	reply    string
	err      *schemas.BifrostError
}

func (c *fakeClient) ChatCompletionRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	skip, _ := ctx.Value(schemas.BifrostContextKeySkipPluginPipeline).(bool)
	c.skipped = append(c.skipped, skip)
	_, hasDeadline := ctx.Deadline()
	c.deadline = append(c.deadline, hasDeadline)
	if c.err != nil {
		return nil, c.err
	}
	reply := c.reply
	return &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{{ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{Message: &schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant, Content: &schemas.ChatMessageContent{ContentStr: &reply}}}}},
		Usage:   &schemas.BifrostLLMUsage{PromptTokens: 12, CompletionTokens: 3},
	}, nil
}

type fakeProviders struct {
	configured map[string]int
	lookupErr  error
}

func (p fakeProviders) GetProviderConfig(_ context.Context, provider schemas.ModelProvider) (*configstore.ProviderConfig, error) {
	if p.lookupErr != nil {
		return nil, p.lookupErr
	}
	retries, ok := p.configured[string(provider)]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	return &configstore.ProviderConfig{NetworkConfig: &schemas.NetworkConfig{MaxRetries: retries}}, nil
}

type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Info(msg string, args ...any) { l.add(msg, args...) }
func (l *testLogger) Warn(msg string, args ...any) { l.add("WARN "+msg, args...) }
func (l *testLogger) add(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(msg, args...))
}
func (l *testLogger) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func TestJudgeModelRequest(t *testing.T) {
	client := &fakeClient{reply: `{"risk_level":"none"}`}
	log := &testLogger{}
	m := host.NewJudgeModel(client, "deepseek", "deepseek-v4-flash", log)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	text, err := m.Complete(ctx, "system prompt", `{"stage":"input","text":"私密内容"}`)
	if err != nil || text != `{"risk_level":"none"}` {
		t.Fatalf("text=%q err=%v", text, err)
	}
	req := client.requests[0]
	if !client.skipped[0] || !client.deadline[0] {
		t.Fatalf("internal request not marked: skipped=%v deadline=%v", client.skipped, client.deadline)
	}
	if req.Provider != "deepseek" || req.Model != "deepseek-v4-flash" || len(req.Input) != 2 ||
		req.Input[0].Role != schemas.ChatMessageRoleSystem || *req.Input[0].Content.ContentStr != "system prompt" ||
		req.Input[1].Role != schemas.ChatMessageRoleUser || !strings.Contains(*req.Input[1].Content.ContentStr, "私密内容") {
		t.Fatalf("request %+v", req)
	}
	if req.Params == nil || *req.Params.Temperature != 0 || *req.Params.MaxCompletionTokens != 200 || req.Params.ResponseFormat == nil {
		t.Fatalf("params %+v", req.Params)
	}
	logs := log.joined()
	if strings.Contains(logs, "私密内容") || !strings.Contains(logs, "tokens=12/3") {
		t.Fatalf("logs %s", logs)
	}
}

// provider 响应的 type/code/message 都可能回显送检内容，一律不进日志与错误链；网关自造的错误保留截断后的消息用于排障。
func TestJudgeModelErrorPolicy(t *testing.T) {
	status := 429
	echo := "私密回显"
	provider := &fakeClient{err: &schemas.BifrostError{StatusCode: &status, Error: &schemas.ErrorField{Type: schemas.Ptr("bad " + echo), Code: schemas.Ptr("code " + echo), Message: "invalid input: " + echo}}}
	log := &testLogger{}
	_, err := host.NewJudgeModel(provider, "deepseek", "m", log).Complete(context.Background(), "s", "u")
	if err == nil || strings.Contains(err.Error(), echo) || strings.Contains(log.joined(), echo) || !strings.Contains(err.Error(), "status=429") || !strings.Contains(err.Error(), "category=provider_rate_limited") {
		t.Fatalf("provider fields leaked or category missing: err=%v logs=%s", err, log.joined())
	}
	// 上游 key 池没有支持判官模型的 key 时：IsBifrostError 为 false、无状态码、消息由网关自造——最常见的配置错误，必须可诊断。
	noKey := &fakeClient{err: &schemas.BifrostError{Error: &schemas.ErrorField{Message: "no keys found that support model: m"}}}
	_, err = host.NewJudgeModel(noKey, "deepseek", "m", log).Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "category=gateway_internal") || !strings.Contains(err.Error(), "no keys found that support model") {
		t.Fatalf("gateway-generated message lost: %v", err)
	}
	// 上游真实取消形状：IsBifrostError 为 true 且带 499。
	cancelStatus := 499
	long := strings.Repeat("模", 100)
	cancelled := &fakeClient{err: &schemas.BifrostError{IsBifrostError: true, StatusCode: &cancelStatus, Error: &schemas.ErrorField{Type: schemas.Ptr("request_cancelled"), Message: long}}}
	_, err = host.NewJudgeModel(cancelled, "deepseek", "m", log).Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "category=gateway_internal") || !strings.Contains(err.Error(), "type=request_cancelled") || strings.Contains(err.Error(), strings.Repeat("模", 70)) || !utf8.ValidString(err.Error()) {
		t.Fatalf("gateway message not kept, not truncated or broke UTF-8: %v", err)
	}
	// 网关合成的错误（连不上 provider 502、判官 key 全部失效 502、全被过滤 503）：只附加比对命中的常量名。
	// Error.Type 由 provider 正文可控，同名类型带回显时 code/message 也不得进入错误链。
	for kind, status := range map[string]int{schemas.ProviderConnectionFailed: 502, "upstream_credentials_exhausted": 502, "no_eligible_keys": 503} {
		code := status
		spoofed := &fakeClient{err: &schemas.BifrostError{StatusCode: &code, Error: &schemas.ErrorField{Type: schemas.Ptr(kind), Code: schemas.Ptr("code " + echo), Message: "msg " + echo}}}
		_, err = host.NewJudgeModel(spoofed, "deepseek", "m", log).Complete(context.Background(), "s", "u")
		if err == nil || !strings.Contains(err.Error(), "category=provider_unavailable") || !strings.Contains(err.Error(), "known_type="+kind) || strings.Contains(err.Error(), echo) || strings.Contains(log.joined(), echo) {
			t.Fatalf("%s: known type missing or fields leaked: %v", kind, err)
		}
	}
	bad := 502
	// 同样是 502，但类型来自 provider 正文：只按状态码归类。
	body := &fakeClient{err: &schemas.BifrostError{StatusCode: &bad, Error: &schemas.ErrorField{Type: schemas.Ptr("server_error " + echo), Message: echo}}}
	_, err = host.NewJudgeModel(body, "deepseek", "m", log).Complete(context.Background(), "s", "u")
	if err == nil || strings.Contains(err.Error(), echo) || strings.Contains(log.joined(), echo) || !strings.Contains(err.Error(), "category=provider_unavailable") || strings.Contains(err.Error(), "known_type") {
		t.Fatalf("provider 502 leaked: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := host.NewJudgeModel(provider, "deepseek", "m", log).Complete(ctx, "s", "u"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx must surface: %v", err)
	}
}

func sampleConfig(t *testing.T, provider string) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(`{"deny":{"status":400,"message":"no"},"judge":{"provider":"` + provider + `","model":"m"},
	  "secrets":{"enabled":true,"stages":["input"],"threshold":"medium","on_match":"block","on_error":"block"},
	  "harmful":{"enabled":true,"stages":["input","output"],"threshold":"medium","on_match":"block","on_error":"block"},
	  "business_rules":[{"id":"pricing","rule":"不得透露底价","enabled":true,"stages":["input"],"threshold":"medium","on_match":"block","on_error":"block"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestBuilderBuildsChecker(t *testing.T) {
	client := &fakeClient{reply: `{"risk_level":"high"}`}
	log := &testLogger{}
	b, err := host.NewBuilder(client, fakeProviders{configured: map[string]int{"deepseek": 2}}, log)
	if err != nil {
		t.Fatal(err)
	}
	checker, err := b.Build(context.Background(), sampleConfig(t, "deepseek"))
	if err != nil {
		t.Fatal(err)
	}
	if !checker.HasRules(guardrails.Input) || !checker.HasRules(guardrails.Output) || checker.MaxTextBytes() != config.DefaultMaxTextBytes {
		t.Fatal("rules missing")
	}
	if !strings.Contains(log.joined(), "WARN") || !strings.Contains(log.joined(), "max_retries=2") {
		t.Fatalf("provider retry warning missing: %s", log.joined())
	}
	result, err := checker.Check(context.Background(), guardrails.Input, "随便聊聊")
	if err != nil || result.Action != guardrails.Block || result.Blocking == nil || result.Blocking.Category != guardrails.HarmfulContent && result.Blocking.Category != guardrails.BusinessRule {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(client.requests) == 0 {
		t.Fatal("judge not called")
	}
}

func TestBuilderRejectsUnknownProviderAndNilDeps(t *testing.T) {
	log := &testLogger{}
	b, err := host.NewBuilder(&fakeClient{}, fakeProviders{configured: map[string]int{}}, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(context.Background(), sampleConfig(t, "missing")); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v", err)
	}
	failing, _ := host.NewBuilder(&fakeClient{}, fakeProviders{lookupErr: errors.New("db down")}, log)
	if _, err := failing.Build(context.Background(), sampleConfig(t, "deepseek")); err == nil || !strings.Contains(err.Error(), "lookup failed") || !strings.Contains(log.joined(), "db down") {
		t.Fatalf("lookup failure not distinguished: err=%v logs=%s", err, log.joined())
	}
	if _, err := host.NewBuilder(nil, fakeProviders{}, log); err == nil {
		t.Fatal("nil client accepted")
	}
	// 只启用密钥时不查 provider。
	cfg, err := config.Parse([]byte(`{"deny":{"status":400,"message":"no"},"secrets":{"enabled":true,"stages":["input"],"threshold":"high","on_match":"block","on_error":"block"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}
