// 本文件验证并行执行下的拦截取消、拦截来源、父取消与失败类别，不替代真实算法的效果测试。
package guardrails_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// 一条规则拦截后，仍在等待的规则被取消且不出现在结果里；Blocking 指向该规则。
func TestBlockCancelsPendingRules(t *testing.T) {
	started, released := make(chan struct{}), make(chan struct{})
	e := mustChecker(t, []guardrails.Rule{rule("slow"), rule("block")}, map[string]guardrails.Detector{
		"slow": detectorFunc(func(ctx context.Context, _ string) ([]guardrails.Finding, error) {
			close(started)
			<-ctx.Done()
			close(released)
			return nil, ctx.Err()
		}),
		"block": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			<-started
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
	})
	result, err := e.Check(context.Background(), guardrails.Input, "text")
	select {
	case <-released:
	default:
		t.Fatal("pending rule was not canceled before Check returned")
	}
	if err != nil || result.Action != guardrails.Block || len(result.RuleEvaluations) != 1 || result.RuleEvaluations[0].RuleID != "block" ||
		result.Blocking == nil || result.Blocking != &result.RuleEvaluations[0] {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// 已完成的观察规则在另一条规则拦截时保留；多条拦截取配置顺序最靠前的。
func TestCompletedObserveKeptAndFirstBlockWins(t *testing.T) {
	observed := make(chan struct{})
	observe, late, early := rule("observe"), rule("late"), rule("early")
	observe.OnMatch = guardrails.Observe
	e := mustChecker(t, []guardrails.Rule{observe, late, early}, map[string]guardrails.Detector{
		"observe": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			close(observed)
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
		"late": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			<-observed
			time.Sleep(20 * time.Millisecond)
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
		"early": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			<-observed
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
	})
	result, err := e.Check(context.Background(), guardrails.Input, "text")
	if err != nil || result.Action != guardrails.Block || result.Blocking == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.RuleEvaluations[0].RuleID != "observe" || result.RuleEvaluations[0].Action != guardrails.Observe {
		t.Fatalf("observe evaluation lost: %+v", result.RuleEvaluations)
	}
	for i := range result.RuleEvaluations {
		if result.RuleEvaluations[i].Action == guardrails.Block {
			if result.Blocking != &result.RuleEvaluations[i] {
				t.Fatalf("blocking must be the first block in configuration order: %+v", result)
			}
			break
		}
	}
}

// 父请求取消是框架错误：返回 error、Action 为 Block、Blocking 为 nil，已完成评估保留。
func TestParentCancelDuringParallelRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := rule("first"), rule("second")
	first.OnMatch = guardrails.Observe
	done := make(chan struct{})
	e := mustChecker(t, []guardrails.Rule{first, second}, map[string]guardrails.Detector{
		"first": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			close(done)
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
		"second": detectorFunc(func(c context.Context, _ string) ([]guardrails.Finding, error) {
			<-done
			cancel()
			<-c.Done()
			return nil, c.Err()
		}),
	})
	result, err := e.Check(ctx, guardrails.Input, "text")
	if !errors.Is(err, context.Canceled) || result.Action != guardrails.Block || result.Blocking != nil || len(result.RuleEvaluations) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// 检测器声明超限记为独立失败类别，按规则失败策略处置。
func TestDetectorTextTooLargeFailure(t *testing.T) {
	for _, action := range []guardrails.Action{guardrails.Allow, guardrails.Block} {
		r := rule("judge")
		r.OnError = action
		e := mustChecker(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"judge": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			return nil, guardrails.ErrDetectorTextTooLarge
		})})
		result, err := e.Check(context.Background(), guardrails.Input, "text")
		if err != nil || result.Action != action || len(result.RuleEvaluations) != 1 || result.RuleEvaluations[0].Failure != guardrails.TextTooLarge {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if action == guardrails.Block && (result.Blocking == nil || result.Blocking.Failure != guardrails.TextTooLarge) {
			t.Fatalf("blocking must carry the failure: %+v", result)
		}
	}
}

// 并发调用同一检查器时无竞态，且每次结果独立。
func TestParallelChecksAreIndependent(t *testing.T) {
	e := mustChecker(t, []guardrails.Rule{rule("a"), rule("b")}, map[string]guardrails.Detector{
		"a": detectorFunc(func(_ context.Context, text string) ([]guardrails.Finding, error) {
			if text == "unsafe" {
				return []guardrails.Finding{{Level: guardrails.High}}, nil
			}
			return nil, nil
		}),
		"b": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { return nil, nil }),
	})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(unsafe bool) {
			defer wg.Done()
			text := "safe"
			if unsafe {
				text = "unsafe"
			}
			result, err := e.Check(context.Background(), guardrails.Input, text)
			if err != nil || (result.Action == guardrails.Block) != unsafe {
				t.Errorf("unsafe=%v result=%+v err=%v", unsafe, result, err)
			}
		}(i%2 == 0)
	}
	wg.Wait()
}
