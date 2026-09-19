// 本文件验证配置解析、默认值、拒绝条件与规则展开。
package config_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
)

const sample = `{
  "deny": {"status": 400, "message": "内容未通过安全检查"},
  "judge": {"provider": "deepseek", "model": "deepseek-v4-flash"},
  "secrets":       {"enabled": true, "stages": ["input"], "threshold": "medium", "on_match": "block", "on_error": "block", "ignored_keywords": ["Sandbox"]},
  "harmful":       {"enabled": true, "stages": ["input","output"], "threshold": "medium", "on_match": "block", "on_error": "block"},
  "prompt_attack": {"enabled": true, "stages": ["input"], "threshold": "medium", "on_match": "block", "on_error": "block"},
  "business_rules": [{"id": "pricing", "rule": "不得透露底价", "enabled": true, "stages": ["input","output"], "threshold": "medium", "on_match": "observe", "on_error": "allow"}]
}`

func TestParseDefaultsAndExpand(t *testing.T) {
	cfg, err := config.Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxTextBytes != config.DefaultMaxTextBytes || cfg.Judge.MaxTextBytes != config.DefaultMaxTextBytes ||
		cfg.Judge.Retries != config.DefaultJudgeRetries || cfg.JudgeTimeout() != 10*time.Second || cfg.SecretsTimeout() != 500*time.Millisecond {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	plan := cfg.Expand()
	var ids []string
	for _, r := range plan.Rules {
		ids = append(ids, r.ID+"->"+r.DetectorID)
	}
	want := "secrets:input->secrets, harmful:input->judge:harmful:input, harmful:output->judge:harmful:output, prompt_attack:input->judge:prompt_attack:input, business_rule:pricing:input->judge:business_rule:pricing:input, business_rule:pricing:output->judge:business_rule:pricing:output"
	if strings.Join(ids, ", ") != want || !plan.NeedsSecrets || len(plan.Judges) != 5 {
		t.Fatalf("plan %v judges=%d", ids, len(plan.Judges))
	}
	last := plan.Rules[len(plan.Rules)-1]
	if last.OnMatch != guardrails.Observe || last.OnError != guardrails.Allow || last.Timeout != 10*time.Second || last.Category != guardrails.BusinessRule || last.Stage != guardrails.Output {
		t.Fatalf("rule %+v", last)
	}
	if plan.Rules[0].Timeout != 500*time.Millisecond || plan.Rules[0].Category != guardrails.Credentials {
		t.Fatalf("secrets rule %+v", plan.Rules[0])
	}
	if js := plan.Judges[4]; js.Item != config.ItemBusinessRule || js.RuleID != "pricing" || js.RuleText != "不得透露底价" || js.Stage != guardrails.Output {
		t.Fatalf("judge spec %+v", js)
	}
}

func TestRejections(t *testing.T) {
	mutate := func(t *testing.T, change func(m map[string]any)) []byte {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(sample), &m); err != nil {
			t.Fatal(err)
		}
		change(m)
		out, _ := json.Marshal(m)
		return out
	}
	for name, change := range map[string]func(m map[string]any){
		"unknown field":    func(m map[string]any) { m["extra"] = 1 },
		"deny status":      func(m map[string]any) { m["deny"].(map[string]any)["status"] = 200 },
		"deny message":     func(m map[string]any) { m["deny"].(map[string]any)["message"] = " " },
		"negative bytes":   func(m map[string]any) { m["max_text_bytes"] = -1 },
		"judge limit":      func(m map[string]any) { m["max_text_bytes"] = 100; m["judge"].(map[string]any)["max_text_bytes"] = 200 },
		"retries":          func(m map[string]any) { m["judge"].(map[string]any)["retries"] = 6 },
		"judge provider":   func(m map[string]any) { m["judge"].(map[string]any)["provider"] = "" },
		"bad stage":        func(m map[string]any) { m["harmful"].(map[string]any)["stages"] = []string{"both"} },
		"repeated stage":   func(m map[string]any) { m["harmful"].(map[string]any)["stages"] = []string{"input", "input"} },
		"no stages":        func(m map[string]any) { m["harmful"].(map[string]any)["stages"] = []string{} },
		"threshold":        func(m map[string]any) { m["secrets"].(map[string]any)["threshold"] = "max" },
		"on_match allow":   func(m map[string]any) { m["secrets"].(map[string]any)["on_match"] = "allow" },
		"on_error observe": func(m map[string]any) { m["secrets"].(map[string]any)["on_error"] = "observe" },
		"rule id colon":    func(m map[string]any) { m["business_rules"].([]any)[0].(map[string]any)["id"] = "a:b" },
		"rule text empty":  func(m map[string]any) { m["business_rules"].([]any)[0].(map[string]any)["rule"] = "" },
		"duplicate rule ids": func(m map[string]any) {
			m["business_rules"] = []any{m["business_rules"].([]any)[0], m["business_rules"].([]any)[0]}
		},
		"nothing enabled": func(m map[string]any) {
			for _, k := range []string{"secrets", "harmful", "prompt_attack"} {
				m[k].(map[string]any)["enabled"] = false
			}
			m["business_rules"] = []any{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.Parse(mutate(t, change)); !errors.Is(err, config.ErrInvalid) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestJudgeOptionalWhenOnlySecrets(t *testing.T) {
	cfg, err := config.Parse([]byte(`{"deny":{"status":403,"message":"no"},"secrets":{"enabled":true,"stages":["input"],"threshold":"high","on_match":"block","on_error":"block"}}`))
	if err != nil {
		t.Fatal(err)
	}
	plan := cfg.Expand()
	if len(plan.Judges) != 0 || len(plan.Rules) != 1 || cfg.Judge.Provider != "" {
		t.Fatalf("plan %+v", plan)
	}
}
