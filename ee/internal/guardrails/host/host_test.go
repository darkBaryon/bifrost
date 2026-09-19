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

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/host"
	"github.com/maximhq/bifrost/core/schemas"
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

type fakeProviders struct{ configured map[string]int }

func (p fakeProviders) GetProviderConfig(_ context.Context, provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	retries, ok := p.configured[string(provider)]
	if !ok {
		return nil, errors.New("not found")
	}
	return &schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{MaxRetries: retries}}, nil
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

func TestJudgeModelErrorsHideBody(t *testing.T) {
	status := 429
	client := &fakeClient{err: &schemas.BifrostError{StatusCode: &status, Error: &schemas.ErrorField{Type: schemas.Ptr("rate_limit"), Code: schemas.Ptr("429"), Message: "provider said: 私密回显"}}}
	log := &testLogger{}
	_, err := host.NewJudgeModel(client, "deepseek", "m", log).Complete(context.Background(), "s", "u")
	if err == nil || strings.Contains(err.Error(), "私密回显") || !strings.Contains(err.Error(), "status=429") || strings.Contains(log.joined(), "私密回显") {
		t.Fatalf("err=%v logs=%s", err, log.joined())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := host.NewJudgeModel(client, "deepseek", "m", log).Complete(ctx, "s", "u"); !errors.Is(err, context.Canceled) {
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
