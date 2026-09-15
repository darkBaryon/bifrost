// 本文件验证配置必须明确、检测器缺失不能伪装成已启用。
package guardrails_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

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

func TestUninitializedEngineAndContext(t *testing.T) {
	for _, e := range []*guardrails.Engine{nil, {}} {
		result, err := e.Check(context.Background(), guardrails.Input, "")
		if !errors.Is(err, guardrails.ErrInvalidInput) || result.Action != guardrails.Block {
			t.Fatalf("uninitialized result=%+v err=%v", result, err)
		}
	}
	e := mustEngine(t, nil, nil)
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
