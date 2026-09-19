// 本文件用「硬样本」回归密钥检测器：真实格式的假密钥出现在代码、配置、日志与中文对话里，
// 以及容易误拦的占位符、环境变量引用、哈希与 UUID。样本见 data/samples-hard.json。
package secrets_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/secrets"
)

//go:embed data/samples-hard.json
var hardSamples []byte

func TestHardSamples(t *testing.T) {
	var file struct {
		Samples []struct{ ID, Group, Expect, Text string } `json:"samples"`
	}
	if err := json.Unmarshal(hardSamples, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Samples) < 20 {
		t.Fatalf("样本过少：%d", len(file.Samples))
	}
	d := newDetector(t, secrets.Options{})
	for _, s := range file.Samples {
		findings, err := d.Detect(context.Background(), s.Text)
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		highest := guardrails.Level(0)
		for _, f := range findings {
			if f.Level > highest {
				highest = f.Level
			}
		}
		got := "pass"
		if highest >= guardrails.Medium {
			got = "block"
		}
		if got != s.Expect {
			t.Errorf("%s（%s）expect=%s got=%s level=%d", s.ID, s.Group, s.Expect, got, highest)
		}
	}
}
