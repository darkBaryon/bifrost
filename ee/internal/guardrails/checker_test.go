// 本文件验证规则顺序、风险阈值及失败和取消语义，不替代真实算法的效果测试。
package guardrails_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

type detectorFunc func(context.Context, string) ([]guardrails.Finding, error)

func (f detectorFunc) Detect(ctx context.Context, text string) ([]guardrails.Finding, error) {
	return f(ctx, text)
}

func rule(id string) guardrails.Rule {
	return guardrails.Rule{ID: id, DetectorID: id, Category: guardrails.BusinessRule, Stage: guardrails.Input,
		Threshold: guardrails.Medium, OnMatch: guardrails.Block, OnError: guardrails.Block, Timeout: time.Second}
}

func mustChecker(t *testing.T, rules []guardrails.Rule, detectors map[string]guardrails.Detector) *guardrails.Checker {
	t.Helper()
	e, err := guardrails.New(rules, detectors, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRuleActionsAndThreshold(t *testing.T) {
	for _, tc := range []struct {
		name    string
		level   guardrails.Level
		action  guardrails.Action
		want    guardrails.Action
		outcome guardrails.Outcome
	}{
		{"below threshold", guardrails.Low, guardrails.Block, guardrails.Allow, guardrails.Passed},
		{"at threshold", guardrails.Medium, guardrails.Block, guardrails.Block, guardrails.Matched},
		{"observe", guardrails.High, guardrails.Observe, guardrails.Allow, guardrails.Matched},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rule("one")
			r.OnMatch = tc.action
			e := mustChecker(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
				return []guardrails.Finding{{Level: tc.level}}, nil
			})})
			result, err := e.Check(context.Background(), guardrails.Input, "text")
			if err != nil || result.Action != tc.want || len(result.RuleEvaluations) != 1 || result.RuleEvaluations[0].Outcome != tc.outcome {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

// 同阶段规则并行执行，结果按配置顺序；其他阶段的规则不执行。
func TestParallelStagesAndOrder(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]bool{}
	detectors := map[string]guardrails.Detector{}
	for _, id := range []string{"observe", "second", "output"} {
		detectors[id] = detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			mu.Lock()
			calls[id] = true
			mu.Unlock()
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		})
	}
	rules := []guardrails.Rule{rule("observe"), rule("output"), rule("second")}
	rules[0].OnMatch = guardrails.Observe
	rules[1].Stage = guardrails.Output
	rules[2].OnMatch = guardrails.Observe
	e := mustChecker(t, rules, detectors)
	result, err := e.Check(context.Background(), guardrails.Input, "")
	if err != nil || result.Action != guardrails.Allow || result.Blocking != nil || len(result.RuleEvaluations) != 2 ||
		result.RuleEvaluations[0].RuleID != "observe" || result.RuleEvaluations[1].RuleID != "second" || calls["output"] {
		t.Fatalf("calls=%v result=%+v err=%v", calls, result, err)
	}
	if !e.HasRules(guardrails.Output) || e.MaxTextBytes() != 1024 {
		t.Fatal("configuration lost")
	}
}

func TestDetectorFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		detector detectorFunc
		failure  guardrails.Failure
	}{
		{"error", func(context.Context, string) ([]guardrails.Finding, error) {
			return nil, errors.New("private user text")
		}, guardrails.DetectorError},
		{"malformed", func(context.Context, string) ([]guardrails.Finding, error) {
			return []guardrails.Finding{{Level: 0}}, nil
		}, guardrails.InvalidResult},
		{"timeout", func(ctx context.Context, _ string) ([]guardrails.Finding, error) { <-ctx.Done(); return nil, nil }, guardrails.DetectorTimeout},
	} {
		for _, action := range []guardrails.Action{guardrails.Allow, guardrails.Block} {
			t.Run(tc.name+string(action), func(t *testing.T) {
				r := rule("one")
				r.OnError = action
				r.Timeout = time.Millisecond
				e := mustChecker(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": tc.detector})
				result, err := e.Check(context.Background(), guardrails.Input, "text")
				if err != nil || result.Action != action || len(result.RuleEvaluations) != 1 || result.RuleEvaluations[0].Failure != tc.failure || result.RuleEvaluations[0].Outcome != guardrails.Failed {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestAllowFailureContinuesToBlockingRule(t *testing.T) {
	r := rule("broken")
	r.OnError = guardrails.Allow
	e := mustChecker(t, []guardrails.Rule{r, rule("block")}, map[string]guardrails.Detector{
		"broken": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { return nil, errors.New("failed") }),
		"block": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
	})
	result, err := e.Check(context.Background(), guardrails.Input, "text")
	if err != nil || result.Action != guardrails.Block || len(result.RuleEvaluations) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCancellationIsNotFailOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := rule("one")
	r.OnError = guardrails.Allow
	e := mustChecker(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { cancel(); return nil, nil })})
	result, err := e.Check(ctx, guardrails.Input, "text")
	if !errors.Is(err, context.Canceled) || result.Action != guardrails.Block {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestInputLimitsAndEmptyPolicy(t *testing.T) {
	e := mustChecker(t, nil, nil)
	for _, tc := range []struct {
		stage guardrails.Stage
		text  string
		want  error
	}{
		{guardrails.Input, strings.Repeat("x", 1025), guardrails.ErrTextTooLarge},
		{guardrails.Input, string([]byte{0xff}), guardrails.ErrInvalidInput},
		{"unknown", "text", guardrails.ErrInvalidInput},
	} {
		result, err := e.Check(context.Background(), tc.stage, tc.text)
		if !errors.Is(err, tc.want) || result.Action != guardrails.Block {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	result, err := e.Check(context.Background(), guardrails.Input, "")
	if err != nil || result.Action != guardrails.Allow || len(result.RuleEvaluations) != 0 || e.HasRules(guardrails.Input) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestConfigurationSnapshotAndConcurrentChecks(t *testing.T) {
	d := detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
		return []guardrails.Finding{{Level: guardrails.High}}, nil
	})
	rules := []guardrails.Rule{rule("one")}
	detectors := map[string]guardrails.Detector{"one": d}
	e := mustChecker(t, rules, detectors)
	rules[0].OnMatch = guardrails.Observe
	delete(detectors, "one")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Check(context.Background(), guardrails.Input, "text")
			if err != nil || r.Action != guardrails.Block {
				t.Errorf("result=%+v err=%v", r, err)
			}
		}()
	}
	wg.Wait()
}

func TestInvalidRules(t *testing.T) {
	d := detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { return nil, nil })
	for _, tc := range []struct {
		name   string
		change func(*guardrails.Rule)
	}{
		{"id", func(r *guardrails.Rule) { r.ID = "" }},
		{"detector", func(r *guardrails.Rule) { r.DetectorID = "missing" }},
		{"stage", func(r *guardrails.Rule) { r.Stage = "" }},
		{"category", func(r *guardrails.Rule) { r.Category = "D2" }},
		{"threshold", func(r *guardrails.Rule) { r.Threshold = 0 }},
		{"match", func(r *guardrails.Rule) { r.OnMatch = guardrails.Allow }},
		{"failure", func(r *guardrails.Rule) { r.OnError = "" }},
		{"timeout", func(r *guardrails.Rule) { r.Timeout = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rule("one")
			tc.change(&r)
			if _, err := guardrails.New([]guardrails.Rule{r}, map[string]guardrails.Detector{"one": d}, 100); err == nil {
				t.Fatal("accepted invalid rule")
			}
		})
	}
	if _, err := guardrails.New([]guardrails.Rule{rule("one"), rule("one")}, map[string]guardrails.Detector{"one": d}, 100); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := guardrails.New(nil, nil, 0); err == nil {
		t.Fatal("missing byte limit accepted")
	}
	var missing detectorFunc
	if _, err := guardrails.New([]guardrails.Rule{rule("one")}, map[string]guardrails.Detector{"one": missing}, 100); err == nil {
		t.Fatal("typed nil detector accepted")
	}
}

func TestUninitializedCheckerAndContext(t *testing.T) {
	for _, e := range []*guardrails.Checker{nil, {}} {
		result, err := e.Check(context.Background(), guardrails.Input, "")
		if !errors.Is(err, guardrails.ErrInvalidInput) || result.Action != guardrails.Block {
			t.Fatalf("uninitialized result=%+v err=%v", result, err)
		}
	}
	e := mustChecker(t, nil, nil)
	if _, err := e.Check(nil, guardrails.Input, ""); !errors.Is(err, guardrails.ErrInvalidInput) {
		t.Fatalf("nil context: %v", err)
	}
	result, err := e.Check(context.Background(), guardrails.Input, strings.Repeat("x", 1024))
	if err != nil || result.Action != guardrails.Allow {
		t.Fatalf("exact limit result=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = e.Check(ctx, guardrails.Input, "")
	if !errors.Is(err, context.Canceled) || result.Action != guardrails.Block {
		t.Fatalf("canceled empty policy result=%+v err=%v", result, err)
	}
}

// 超限和取消必须先于线性 UTF-8 扫描。
func TestCheckErrorPrecedenceAndRuleEvaluations(t *testing.T) {
	e := mustChecker(t, nil, nil)
	oversized := strings.Repeat("x", 1024) + string([]byte{0xff})
	if _, err := e.Check(context.Background(), guardrails.Input, oversized); !errors.Is(err, guardrails.ErrTextTooLarge) {
		t.Fatalf("size must precede UTF-8 validation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Check(ctx, guardrails.Input, oversized); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must precede text validation: %v", err)
	}
	// 已完成评估在父取消时的保留见 parallel_test.go 的确定性用例。
}
