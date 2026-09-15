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

func mustEngine(t *testing.T, rules []guardrails.Rule, detectors map[string]guardrails.Detector) *guardrails.Engine {
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
			e := mustEngine(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
				return []guardrails.Finding{{Level: tc.level}}, nil
			})})
			result, err := e.Check(context.Background(), guardrails.Input, "text")
			if err != nil || result.Action != tc.want || len(result.Events) != 1 || result.Events[0].Outcome != tc.outcome {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestOrderShortCircuitAndStages(t *testing.T) {
	var calls []string
	detectors := map[string]guardrails.Detector{}
	for _, id := range []string{"observe", "block", "never", "output"} {
		detectors[id] = detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			calls = append(calls, id)
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		})
	}
	rules := []guardrails.Rule{rule("observe"), rule("output"), rule("block"), rule("never")}
	rules[0].OnMatch = guardrails.Observe
	rules[1].Stage = guardrails.Output
	e := mustEngine(t, rules, detectors)
	result, err := e.Check(context.Background(), guardrails.Input, "")
	if err != nil || result.Action != guardrails.Block || strings.Join(calls, ",") != "observe,block" {
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
				e := mustEngine(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": tc.detector})
				result, err := e.Check(context.Background(), guardrails.Input, "text")
				if err != nil || result.Action != action || len(result.Events) != 1 || result.Events[0].Failure != tc.failure || result.Events[0].Outcome != guardrails.Failed {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestAllowFailureContinuesToBlockingRule(t *testing.T) {
	r := rule("broken")
	r.OnError = guardrails.Allow
	e := mustEngine(t, []guardrails.Rule{r, rule("block")}, map[string]guardrails.Detector{
		"broken": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { return nil, errors.New("failed") }),
		"block": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
			return []guardrails.Finding{{Level: guardrails.High}}, nil
		}),
	})
	result, err := e.Check(context.Background(), guardrails.Input, "text")
	if err != nil || result.Action != guardrails.Block || len(result.Events) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCancellationIsNotFailOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := rule("one")
	r.OnError = guardrails.Allow
	e := mustEngine(t, []guardrails.Rule{r}, map[string]guardrails.Detector{"one": detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) { cancel(); return nil, nil })})
	result, err := e.Check(ctx, guardrails.Input, "text")
	if !errors.Is(err, context.Canceled) || result.Action != guardrails.Block {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestInputLimitsAndEmptyPolicy(t *testing.T) {
	e := mustEngine(t, nil, nil)
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
	if err != nil || result.Action != guardrails.Allow || len(result.Events) != 0 || e.HasRules(guardrails.Input) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestConfigurationSnapshotAndConcurrentChecks(t *testing.T) {
	d := detectorFunc(func(context.Context, string) ([]guardrails.Finding, error) {
		return []guardrails.Finding{{Level: guardrails.High}}, nil
	})
	rules := []guardrails.Rule{rule("one")}
	detectors := map[string]guardrails.Detector{"one": d}
	e := mustEngine(t, rules, detectors)
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
