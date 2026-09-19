// 本文件验证检查器热替换与请求级快照：后置 Hook 只认前置 Hook 留下的快照。
package plugin_test

import (
	"context"
	"sync"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	safety "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
	"github.com/maximhq/bifrost/core/schemas"
)

var testOptions = safety.Options{StatusCode: 400, DenyMessage: "请求已拦截"}

func outputChecker(t *testing.T) *guardrails.Checker {
	t.Helper()
	e, err := guardrails.New([]guardrails.Rule{testRule(guardrails.Output)}, map[string]guardrails.Detector{"local": detectorFunc(matchingDetector)}, 512)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// 前置 Hook 之后换入新检查器，进行中的请求仍用旧快照；新请求用新检查器。
func TestSwapAfterPreKeepsSnapshot(t *testing.T) {
	p, _ := newPlugin(t, nil, nil)
	ctx := primed(t, p, testContext())
	p.Swap(outputChecker(t), testOptions)
	resp := response("unsafe")
	if got, bErr, err := p.PostLLMHook(ctx, resp, nil); got != resp || bErr != nil || err != nil {
		t.Fatal("in-flight request was re-judged by the swapped checker")
	}
	got, bErr, _ := p.PostLLMHook(primed(t, p, testContext()), response("unsafe"), nil)
	if got != nil {
		t.Fatal("new request did not use the swapped checker")
	}
	requireCode(t, bErr, "content_safety_blocked")
}

// 未配置（nil 快照）期间换入含输出规则的配置，进行中的流式请求继续透传，不撞不支持错误。
func TestNilSnapshotStaysPassThroughAfterSwap(t *testing.T) {
	p, _ := newPlugin(t, nil, nil)
	ctx := testContext()
	req := request("safe")
	req.RequestType = schemas.ChatCompletionStreamRequest
	if _, short, err := p.PreLLMHook(ctx, req); err != nil || short != nil {
		t.Fatal("disabled plugin rejected a stream")
	}
	p.Swap(outputChecker(t), testOptions)
	chunk := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{Object: "chat.completion.chunk"}}
	if got, bErr, err := p.PostLLMHook(ctx, chunk, nil); got != chunk || bErr != nil || err != nil {
		t.Fatal("stream admitted without output rules was inspected after swap")
	}
}

// 后置 Hook 没有快照（前置 Hook 未运行）时透传，不回读当前检查器。
func TestPostWithoutSnapshotPassesThrough(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	resp := response("unsafe")
	if got, bErr, err := p.PostLLMHook(testContext(), resp, nil); got != resp || bErr != nil || err != nil {
		t.Fatal("post hook consulted the live checker without a snapshot")
	}
}

// fallback 会重跑前置 Hook；同一请求的快照不被覆盖。
func TestFallbackRerunKeepsFirstSnapshot(t *testing.T) {
	p, _ := newPlugin(t, nil, nil)
	ctx := primed(t, p, testContext())
	p.Swap(outputChecker(t), testOptions)
	primed(t, p, ctx)
	resp := response("unsafe")
	if got, bErr, err := p.PostLLMHook(ctx, resp, nil); got != resp || bErr != nil || err != nil {
		t.Fatal("fallback rerun replaced the request snapshot")
	}
}

// 拦截来源的三种错误码：命中、检测器超限、其他失败。
func TestBlockingErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detector detectorFunc
		code     string
	}{
		{"matched", matchingDetector, "content_safety_blocked"},
		{"too large", func(context.Context, string) ([]guardrails.Finding, error) {
			return nil, guardrails.ErrDetectorTextTooLarge
		}, "content_safety_text_too_large"},
		{"failed", func(context.Context, string) ([]guardrails.Finding, error) { return nil, context.DeadlineExceeded }, "content_safety_check_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, tc.detector)
			_, short, err := p.PreLLMHook(testContext(), request("unsafe"))
			if err != nil || short == nil {
				t.Fatalf("short=%+v err=%v", short, err)
			}
			requireCode(t, short.Error, tc.code)
		})
	}
}

// Swap 与并发请求无竞态。
func TestSwapUnderConcurrentRequests(t *testing.T) {
	p, _ := newPlugin(t, nil, nil)
	checker := outputChecker(t)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(swap bool) {
			defer wg.Done()
			if swap {
				p.Swap(checker, testOptions)
				return
			}
			ctx := primed(t, p, testContext())
			p.PostLLMHook(ctx, response("unsafe"), nil)
		}(i%4 == 0)
	}
	wg.Wait()
}

// 拒绝策略与检查器一起热替换：Swap 后新请求用新状态码与文案，进行中的请求仍用旧快照；非法选项不替换。
func TestSwapReplacesDenyOptions(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, matchingDetector)
	inflight := primed(t, p, testContext())
	if err := p.Swap(outputChecker(t), safety.Options{StatusCode: 451, DenyMessage: "新文案"}); err != nil {
		t.Fatal(err)
	}
	_, _, _ = inflight, p, t
	fresh := primed(t, p, testContext())
	got, bErr, _ := p.PostLLMHook(fresh, response("unsafe"), nil)
	if got != nil || bErr == nil || *bErr.StatusCode != 451 || bErr.Error.Message != "新文案" {
		t.Fatalf("new options not applied: %+v", bErr)
	}
	if err := p.Swap(outputChecker(t), safety.Options{StatusCode: 200, DenyMessage: "x"}); err == nil {
		t.Fatal("invalid options accepted by Swap")
	}
	if _, bErr, _ := p.PostLLMHook(primed(t, p, testContext()), response("unsafe"), nil); bErr == nil || *bErr.StatusCode != 451 {
		t.Fatal("failed swap changed the running options")
	}
}
