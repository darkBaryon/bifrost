// 本文件解析判官返回的 JSON 等级；理由字段不读取、不记录。
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// ErrInvalidVerdict 表示判官返回不是约定的 JSON 或等级未知；算一次失败，可重试。
var ErrInvalidVerdict = errors.New("judge: invalid verdict")

// noRiskName 是判官表示无风险的等级名；其余等级名由根包定义。
const noRiskName = "none"

type verdict struct {
	RiskLevel string `json:"risk_level"`
}

// ask 调用一次判官并解析等级；返回 0 表示无风险。
func (d *Detector) ask(ctx context.Context, user string) (guardrails.Level, error) {
	content, err := d.model.Complete(ctx, d.system, user)
	if err != nil {
		return 0, err
	}
	return parseLevel(content)
}

// parseLevel 接受纯 JSON，或被 ``` 代码围栏包裹的 JSON。
func parseLevel(content string) (guardrails.Level, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var v verdict
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &v); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidVerdict, err)
	}
	name := strings.ToLower(strings.TrimSpace(v.RiskLevel))
	if name == noRiskName {
		return 0, nil
	}
	if level, ok := guardrails.ParseLevel(name); ok {
		return level, nil
	}
	return 0, fmt.Errorf("%w: unknown risk level", ErrInvalidVerdict)
}
