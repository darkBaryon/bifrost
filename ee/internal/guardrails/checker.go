// Package guardrails 负责本地内容检测的规则执行；检测算法和网关协议由调用方接入。
// 本文件保存规则快照，按阶段执行检查并汇总处置结果。
package guardrails

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Checker 按阶段执行安全规则并汇总处置结果；通过 New 保存只读配置，可并发调用，更新配置时构造新实例。
type Checker struct {
	rules        []Rule
	detectors    map[string]Detector
	maxTextBytes int
}

// CheckResult 是一次文本检查的最终 Allow/Block 决定及各条规则的执行结果；阻断之后的规则不执行。
type CheckResult struct {
	Action          Action
	RuleEvaluations []RuleEvaluation
}

// 框架输入错误不应用检测器失败策略，调用方不得将其当作通过。
var (
	ErrInvalidInput = errors.New("guardrails: invalid input")
	ErrTextTooLarge = errors.New("guardrails: text exceeds configured limit")
)

// New 校验配置并复制规则和注册表；检测器实例由调用方持有其生命周期，必须并发安全。
func New(rules []Rule, detectors map[string]Detector, maxTextBytes int) (*Checker, error) {
	if maxTextBytes <= 0 {
		return nil, fmt.Errorf("guardrails: maxTextBytes must be positive")
	}
	rules = append([]Rule(nil), rules...)
	if err := validateRules(rules, detectors); err != nil {
		return nil, err
	}
	registered := make(map[string]Detector)
	for _, rule := range rules {
		registered[rule.DetectorID] = detectors[rule.DetectorID]
	}
	return &Checker{rules: rules, detectors: registered, maxTextBytes: maxTextBytes}, nil
}

// HasRules 用于入口判断某阶段是否需要检查，不表示内容已通过。
func (c *Checker) HasRules(stage Stage) bool {
	for _, rule := range c.rules {
		if rule.Stage == stage {
			return true
		}
	}
	return false
}

// MaxTextBytes 返回配置的单次完整文本字节上限，供入口在拼接文本前限长。
func (c *Checker) MaxTextBytes() int { return c.maxTextBytes }

// Check 检查完整文本。取消、非法输入及超限返回 error；检测器失败则由规则决定，记录为 Failed。
// 返回 error 时 Action 恒为 Block，RuleEvaluations 保留出错前已完成规则的执行结果，调用方仍可记录。
// 超时依赖检测器协作取消；即使检测器迟返回成功，超过期限的结果也按失败处理。
func (c *Checker) Check(ctx context.Context, stage Stage, text string) (CheckResult, error) {
	checkResult := CheckResult{Action: Block}
	if c == nil || c.maxTextBytes <= 0 || ctx == nil || !validStage(stage) {
		return checkResult, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return checkResult, err
	}
	if len(text) > c.maxTextBytes {
		return checkResult, ErrTextTooLarge
	}
	// 先判取消和长度，再做线性的 UTF-8 扫描，避免超限输入消耗扫描成本。
	if !utf8.ValidString(text) {
		return checkResult, ErrInvalidInput
	}
	checkResult.Action = Allow
	for _, rule := range c.rules {
		if rule.Stage != stage {
			continue
		}
		ruleEvaluation, err := c.evaluateRule(ctx, rule, text)
		if err != nil {
			checkResult.Action = Block
			return checkResult, err
		}
		checkResult.RuleEvaluations = append(checkResult.RuleEvaluations, ruleEvaluation)
		if ruleEvaluation.Action == Block {
			checkResult.Action = Block
			break
		}
	}
	return checkResult, nil
}
