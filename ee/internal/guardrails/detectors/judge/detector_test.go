// 本文件用替身模型验证等级解析、重试、超时与上限；不调用任何真实模型。
package judge_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/judge"
)

// fakeModel 按调用顺序返回预设结果；calls 记录调用次数与最近的 system/user。
type fakeModel struct {
	replies []reply
	calls   atomic.Int32
	system  string
	user    string
}

type reply struct {
	content string
	err     error
	delay   time.Duration
}

func (m *fakeModel) Complete(ctx context.Context, system, user string) (string, error) {
	n := int(m.calls.Add(1)) - 1
	m.system, m.user = system, user
	r := m.replies[min(n, len(m.replies)-1)]
	if r.delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(r.delay):
		}
	}
	return r.content, r.err
}

func newJudge(t *testing.T, m judge.Model, kind judge.Kind, rule string) *judge.Detector {
	t.Helper()
	d, err := judge.New(judge.Options{Model: m, Kind: kind, Rule: rule, Stage: guardrails.Input})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestVerdictLevels(t *testing.T) {
	for content, want := range map[string]guardrails.Level{
		`{"risk_level":"none","reason":"ok"}`:            0,
		`{"risk_level":"low"}`:                           guardrails.Low,
		`{"risk_level": "Medium", "reason": "x"}`:        guardrails.Medium,
		"```json\n{\"risk_level\":\"high\"}\n```":        guardrails.High,
		"  {\"risk_level\":\"HIGH\",\"categories\":[]} ": guardrails.High,
	} {
		m := &fakeModel{replies: []reply{{content: content}}}
		findings, err := newJudge(t, m, judge.Harmful, "").Detect(context.Background(), "text")
		if err != nil {
			t.Fatalf("%s: %v", content, err)
		}
		if want == 0 {
			if len(findings) != 0 {
				t.Fatalf("%s: findings %v", content, findings)
			}
			continue
		}
		if len(findings) != 1 || findings[0].Level != want {
			t.Fatalf("%s: findings %v", content, findings)
		}
	}
}

func TestPromptsAndPayload(t *testing.T) {
	m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`}}}
	d, err := judge.New(judge.Options{Model: m, Kind: judge.BusinessRule, Rule: "不得透露底价", Stage: guardrails.Output})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Detect(context.Background(), "报价 \"多少\""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.system, "<rule>\n不得透露底价\n</rule>") || strings.Contains(m.system, "{{rule}}") {
		t.Fatalf("rule not substituted: %s", m.system)
	}
	if m.user != `{"stage":"output","text":"报价 \"多少\""}` {
		t.Fatalf("payload %s", m.user)
	}
	for _, kind := range []judge.Kind{judge.Harmful, judge.PromptAttack} {
		m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`}}}
		newJudge(t, m, kind, "").Detect(context.Background(), "x")
		if !strings.Contains(m.system, "risk_level") || strings.Contains(m.system, "{{") {
			t.Fatalf("%s prompt malformed", kind)
		}
	}
}

func TestRetriesThenFails(t *testing.T) {
	for name, r := range map[string]reply{
		"error":     {err: errors.New("provider down")},
		"malformed": {content: "not json"},
		"unknown":   {content: `{"risk_level":"critical"}`},
		"empty":     {content: ""},
	} {
		t.Run(name, func(t *testing.T) {
			m := &fakeModel{replies: []reply{r}}
			start := time.Now()
			_, err := newJudge(t, m, judge.Harmful, "").Detect(context.Background(), "text")
			if err == nil || m.calls.Load() != 1+judge.DefaultRetries {
				t.Fatalf("calls=%d err=%v", m.calls.Load(), err)
			}
			// 3 次重试的退避为 200+400+800ms。
			if elapsed := time.Since(start); elapsed < 1400*time.Millisecond || elapsed > 2500*time.Millisecond {
				t.Fatalf("backoff total %v", elapsed)
			}
			if name != "error" && !errors.Is(err, judge.ErrInvalidVerdict) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRetrySucceedsLater(t *testing.T) {
	m := &fakeModel{replies: []reply{{err: errors.New("flaky")}, {content: `{"risk_level":"medium"}`}}}
	findings, err := newJudge(t, m, judge.Harmful, "").Detect(context.Background(), "text")
	if err != nil || len(findings) != 1 || findings[0].Level != guardrails.Medium || m.calls.Load() != 2 {
		t.Fatalf("findings=%v err=%v calls=%d", findings, err, m.calls.Load())
	}
}

func TestTimeoutStopsRetrying(t *testing.T) {
	m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`, delay: time.Second}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newJudge(t, m, judge.Harmful, "").Detect(ctx, "text")
	if !errors.Is(err, context.DeadlineExceeded) || m.calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, m.calls.Load())
	}
	m = &fakeModel{replies: []reply{{err: errors.New("down")}}}
	ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = newJudge(t, m, judge.Harmful, "").Detect(ctx, "text")
	if !errors.Is(err, context.DeadlineExceeded) || m.calls.Load() > 2 {
		t.Fatalf("retry ignored deadline: err=%v calls=%d", err, m.calls.Load())
	}
}

func TestTooLargeIsNotRetried(t *testing.T) {
	m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`}}}
	d, err := judge.New(judge.Options{Model: m, Kind: judge.Harmful, Stage: guardrails.Input, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Detect(context.Background(), "123456789"); !errors.Is(err, guardrails.ErrDetectorTextTooLarge) || m.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, m.calls.Load())
	}
	if _, err := d.Detect(context.Background(), "12345678"); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidOptions(t *testing.T) {
	m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`}}}
	for name, opts := range map[string]judge.Options{
		"no model":        {Kind: judge.Harmful, Stage: guardrails.Input},
		"bad kind":        {Model: m, Kind: "other", Stage: guardrails.Input},
		"bad stage":       {Model: m, Kind: judge.Harmful, Stage: "x"},
		"rule missing":    {Model: m, Kind: judge.BusinessRule, Stage: guardrails.Input},
		"rule not wanted": {Model: m, Kind: judge.Harmful, Rule: "x", Stage: guardrails.Input},
		"retries low":     {Model: m, Kind: judge.Harmful, Stage: guardrails.Input, Retries: 2},
		"retries high":    {Model: m, Kind: judge.Harmful, Stage: guardrails.Input, Retries: 6},
		"negative bytes":  {Model: m, Kind: judge.Harmful, Stage: guardrails.Input, MaxBytes: -1},
	} {
		if _, err := judge.New(opts); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestCanceledBeforeCall(t *testing.T) {
	m := &fakeModel{replies: []reply{{content: `{"risk_level":"none"}`}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newJudge(t, m, judge.Harmful, "").Detect(ctx, "text"); !errors.Is(err, context.Canceled) || m.calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, m.calls.Load())
	}
}
