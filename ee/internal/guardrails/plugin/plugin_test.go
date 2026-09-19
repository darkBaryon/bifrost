// 本文件用测试检测器验证 BF Hook 的阻断、观察、失败和流式准入，不提供生产检测算法。
package plugin_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	safety "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
	"github.com/maximhq/bifrost/core/schemas"
)

type detectorFunc func(context.Context, string) ([]guardrails.Finding, error)

func (f detectorFunc) Detect(ctx context.Context, text string) ([]guardrails.Finding, error) {
	return f(ctx, text)
}

type testLogger struct {
	mu      sync.Mutex
	entries []string
}

func (l *testLogger) Info(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, fmt.Sprintf(format, args...))
}

func testRule(stage guardrails.Stage) guardrails.Rule {
	return guardrails.Rule{ID: "rule", DetectorID: "local", Category: guardrails.BusinessRule, Stage: stage, Threshold: guardrails.High, OnMatch: guardrails.Block, OnError: guardrails.Block, Timeout: time.Second}
}
func matchingDetector(_ context.Context, text string) ([]guardrails.Finding, error) {
	if strings.Contains(text, "unsafe") {
		return []guardrails.Finding{{Level: guardrails.High}}, nil
	}
	return nil, nil
}

// testOptions 是本包测试夹具统一使用的拒绝策略，newPlugin 与断言共用。
var testOptions = safety.Options{StatusCode: 400, DenyMessage: "请求已拦截"}

func newPlugin(t *testing.T, rules []guardrails.Rule, d detectorFunc) (*safety.Plugin, *testLogger) {
	t.Helper()
	e, err := guardrails.New(rules, map[string]guardrails.Detector{"local": d}, 512)
	if err != nil {
		t.Fatal(err)
	}
	log := &testLogger{}
	p, err := safety.New(e, testOptions, log)
	if err != nil {
		t.Fatal(err)
	}
	return p, log
}
func testContext() *schemas.BifrostContext {
	return schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
}

// primed 让前置 Hook 在 ctx 上留下检查器快照；后置 Hook 只认快照，直接调用会透传。
func primed(t *testing.T, p *safety.Plugin, ctx *schemas.BifrostContext) *schemas.BifrostContext {
	t.Helper()
	if _, short, err := p.PreLLMHook(ctx, request("safe")); err != nil || short != nil {
		t.Fatalf("priming request rejected: short=%+v err=%v", short, err)
	}
	return ctx
}
func message(text string) schemas.ChatMessage {
	return schemas.ChatMessage{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: schemas.Ptr(text)}}
}
func request(text string) *schemas.BifrostRequest {
	return &schemas.BifrostRequest{RequestType: schemas.ChatCompletionRequest, ChatRequest: &schemas.BifrostChatRequest{Provider: schemas.OpenAI, Model: "test-model", Input: []schemas.ChatMessage{message(text)}}}
}
func response(texts ...string) *schemas.BifrostResponse {
	r := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{}}
	for i, text := range texts {
		m := message(text)
		m.Role = schemas.ChatMessageRoleAssistant
		r.ChatResponse.Choices = append(r.ChatResponse.Choices, schemas.BifrostResponseChoice{Index: i, ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{Message: &m}})
	}
	return r
}
func requireCode(t *testing.T, err *schemas.BifrostError, code string) {
	t.Helper()
	if err == nil || err.Error == nil || err.Error.Code == nil || *err.Error.Code != code || err.AllowFallbacks == nil || *err.AllowFallbacks || !err.IsBifrostError {
		t.Fatalf("wanted %s, got %+v", code, err)
	}
}

func TestInputAndOutputBlocking(t *testing.T) {
	input, log := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, matchingDetector)
	req := request("private unsafe text")
	got, short, err := input.PreLLMHook(testContext(), req)
	if err != nil || got != req || short == nil {
		t.Fatalf("short=%+v err=%v", short, err)
	}
	requireCode(t, short.Error, "content_safety_blocked")
	if strings.Contains(strings.Join(log.entries, " "), "private unsafe text") {
		t.Fatal("logged input")
	}
	if !strings.Contains(strings.Join(log.entries, " "), "category=business_rule") {
		t.Fatal("missing business category in safety log")
	}
	output, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	resp := response("safe", "unsafe second choice")
	gotResp, blocked, err := output.PostLLMHook(primed(t, output, testContext()), resp, nil)
	if err != nil || gotResp != nil {
		t.Fatalf("response leaked: %+v err=%v", gotResp, err)
	}
	requireCode(t, blocked, "content_safety_blocked")
	if *resp.ChatResponse.Choices[1].Message.Content.ContentStr != "unsafe second choice" {
		t.Fatal("mutated caller response")
	}
}

func TestAllowObserveAndEmptyPolicy(t *testing.T) {
	r := testRule(guardrails.Input)
	r.OnMatch = guardrails.Observe
	p, log := newPlugin(t, []guardrails.Rule{r}, matchingDetector)
	req := request("unsafe")
	got, short, err := p.PreLLMHook(testContext(), req)
	if err != nil || short != nil || got != req || !strings.Contains(strings.Join(log.entries, " "), "action=observe") {
		t.Fatalf("short=%+v err=%v log=%v", short, err, log.entries)
	}
	empty, _ := newPlugin(t, nil, nil)
	if got, short, err := empty.PreLLMHook(nil, nil); got != nil || short != nil || err != nil {
		t.Fatal("empty policy did not pass through")
	}
	out, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	resp := response("safe")
	if got, bErr, err := out.PostLLMHook(testContext(), resp, nil); got != resp || bErr != nil || err != nil {
		t.Fatal("safe response changed")
	}
}

func TestFailurePolicyAndPrivacy(t *testing.T) {
	for _, action := range []guardrails.Action{guardrails.Allow, guardrails.Block} {
		r := testRule(guardrails.Input)
		r.OnError = action
		p, log := newPlugin(t, []guardrails.Rule{r}, func(context.Context, string) ([]guardrails.Finding, error) {
			return nil, errors.New("sensitive detector error")
		})
		_, short, err := p.PreLLMHook(testContext(), request("private text"))
		if err != nil {
			t.Fatal(err)
		}
		if action == guardrails.Block {
			if short == nil {
				t.Fatal("failure allowed")
			}
			requireCode(t, short.Error, "content_safety_check_failed")
		} else if short != nil {
			t.Fatal("explicit allow blocked")
		}
		logs := strings.Join(log.entries, " ")
		if !strings.Contains(logs, "outcome=failed") || strings.Contains(logs, "sensitive") || strings.Contains(logs, "private text") {
			t.Fatal(logs)
		}
	}
}

func TestStreamingAdmission(t *testing.T) {
	for _, stage := range []guardrails.Stage{guardrails.Input, guardrails.Output} {
		p, _ := newPlugin(t, []guardrails.Rule{testRule(stage)}, matchingDetector)
		req := request("safe")
		req.RequestType = schemas.ChatCompletionStreamRequest
		_, short, err := p.PreLLMHook(testContext(), req)
		if err != nil {
			t.Fatal(err)
		}
		if stage == guardrails.Output {
			if short == nil {
				t.Fatal("output stream allowed")
			}
			requireCode(t, short.Error, "content_safety_output_stream_unsupported")
		} else {
			if short != nil {
				t.Fatal("input-only stream blocked")
			}
			chunk := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Object: "chat.completion.chunk"}}
			if got, bErr, err := p.PostLLMHook(testContext(), chunk, nil); got != chunk || bErr != nil || err != nil {
				t.Fatal("input-only stream inspected output")
			}
		}
	}
}

func TestUpstreamErrorAndCancellation(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	upstream := &schemas.BifrostError{Error: &schemas.ErrorField{Message: "provider failed"}}
	if got, bErr, err := p.PostLLMHook(testContext(), nil, upstream); got != nil || bErr != upstream || err != nil {
		t.Fatal("upstream error changed")
	}
	ctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	primed(t, p, ctx)
	cancel()
	_, bErr, err := p.PostLLMHook(ctx, response("safe"), nil)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, bErr, "content_safety_canceled")
}

func TestPluginConstruction(t *testing.T) {
	e, err := guardrails.New(nil, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	log := &testLogger{}
	for _, options := range []safety.Options{{}, {StatusCode: 200, DenyMessage: "denied"}, {StatusCode: 600, DenyMessage: "denied"}, {StatusCode: 400}} {
		if _, err := safety.New(e, options, log); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	options := safety.Options{StatusCode: 400, DenyMessage: "denied"}
	// 未配置时允许以 nil 检查器启动，两个 Hook 都透传。
	disabled, err := safety.New(nil, options, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testContext()
	if _, short, err := disabled.PreLLMHook(ctx, request("unsafe")); err != nil || short != nil {
		t.Fatal("nil checker did not pass input through")
	}
	resp := response("unsafe")
	if got, bErr, err := disabled.PostLLMHook(ctx, resp, nil); got != resp || bErr != nil || err != nil {
		t.Fatal("nil checker did not pass output through")
	}
	if _, err := safety.New(e, options, nil); err == nil {
		t.Fatal("nil logger accepted")
	}
	p, _ := newPlugin(t, nil, nil)
	if p.GetName() != "ee-content-safety" || p.Cleanup() != nil || p.PreRequestHook(nil, nil) != nil {
		t.Fatal("base plugin contract")
	}
}
