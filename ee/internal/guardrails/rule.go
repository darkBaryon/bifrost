// 本文件定义检查规则，并校验其条件、处置策略和检测器绑定。
package guardrails

import (
	"fmt"
	"strings"
	"time"
)

// Stage 表示检查输入还是完整输出。
type Stage string

// 检测阶段使用业务名称，与网关协议无关。
const (
	Input  Stage = "input"
	Output Stage = "output"
)

// Category 标识检测内容的业务类别。
type Category string

// 本期允许接入的四类检测；常量存在不代表算法已实现。
const (
	HarmfulContent Category = "harmful_content"
	Credentials    Category = "credentials"
	PromptAttack   Category = "prompt_attack"
	BusinessRule   Category = "business_rule"
)

// Action 表示网关应执行的动作。
type Action string

// 规则命中支持 Block/Observe，检测失败支持 Allow/Block。
const (
	Allow   Action = "allow"
	Block   Action = "block"
	Observe Action = "observe"
)

// Scope 表示输入阶段送检的文本范围；输出阶段忽略。
// 判官类规则只看最后一条消息：历史在它到达时已查过，且被拦的消息会留在客户端历史里，查全部会让之后每条请求都被拦。
// 密钥这类"发出去即泄露"的检测要看全部消息：历史里的密钥每一轮都在往外发，输出检查兜不住。
type Scope int

const (
	LastMessage Scope = iota // 零值：只送最后一条消息
	AllMessages              // 送全部消息拼接的文本
)

// Rule 显式指定一次检测的条件和处置；所有字段必填，不隐含失败放行策略。
type Rule struct {
	ID         string
	DetectorID string
	Category   Category
	Stage      Stage
	Scope      Scope // 输入阶段送检范围，见 Scope；输出阶段忽略
	Threshold  Level
	OnMatch    Action
	OnError    Action
	Timeout    time.Duration
}

func validateRules(rules []Rule, detectors map[string]Detector) error {
	ids := make(map[string]bool)
	for _, rule := range rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.DetectorID) == "" || ids[rule.ID] {
			return fmt.Errorf("guardrails: rule IDs must be nonempty and unique")
		}
		ids[rule.ID] = true
		if !validStage(rule.Stage) || !validCategory(rule.Category) || !validLevel(rule.Threshold) || rule.Timeout <= 0 {
			return fmt.Errorf("guardrails: invalid stage, category, threshold or timeout in rule %q", rule.ID)
		}
		if rule.Scope != LastMessage && rule.Scope != AllMessages {
			return fmt.Errorf("guardrails: invalid scope in rule %q", rule.ID)
		}
		if rule.OnMatch != Block && rule.OnMatch != Observe {
			return fmt.Errorf("guardrails: invalid match action in rule %q", rule.ID)
		}
		if rule.OnError != Allow && rule.OnError != Block {
			return fmt.Errorf("guardrails: explicit error action required in rule %q", rule.ID)
		}
		d := detectors[rule.DetectorID]
		if nilDetector(d) {
			return fmt.Errorf("guardrails: detector %q is not registered", rule.DetectorID)
		}
	}
	return nil
}

func validStage(s Stage) bool { return s == Input || s == Output }

func validCategory(c Category) bool {
	return c == HarmfulContent || c == Credentials || c == PromptAttack || c == BusinessRule
}
