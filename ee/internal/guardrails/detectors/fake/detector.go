// Package fake 提供开发联调用的标记检测器，不具备真实内容安全识别能力。
package fake

import (
	"context"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// 联调使用固定标记，避免把普通业务词误当成真实检测能力。
const riskMarker = "[guardrails-test]"

// Detector 遇到测试标记返回高风险，否则返回空结果；无状态，可并发调用。
type Detector struct{}

// Detect 只匹配开发测试标记，并遵守调用方的取消信号。
func (Detector) Detect(ctx context.Context, text string) ([]guardrails.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(text, riskMarker) {
		return []guardrails.Finding{{Level: guardrails.High}}, nil
	}
	return nil, nil
}
