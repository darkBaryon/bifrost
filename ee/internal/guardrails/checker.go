// Package guardrails 负责本地内容检测的规则执行；检测算法和网关协议由调用方接入。
// 本文件保存规则快照，按阶段并行执行检查并汇总处置结果。
package guardrails

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

// Checker 按阶段执行安全规则并汇总处置结果；通过 New 保存只读配置，可并发调用，更新配置时构造新实例。
type Checker struct {
	rules        []Rule
	detectors    map[string]Detector
	maxTextBytes int
}

// CheckResult 是一次文本检查的最终 Allow/Block 决定及各条规则的执行结果。
// 同阶段规则并行执行；任一规则判定拦截即取消其余，被取消而未完成的规则不出现在 RuleEvaluations 中。
type CheckResult struct {
	Action Action
	// Blocking 在 Action==Block 且没有框架错误时指向 RuleEvaluations 中决定拦截的元素（多条时取配置顺序最靠前的）；其余情况为 nil。
	Blocking        *RuleEvaluation
	RuleEvaluations []RuleEvaluation
}

// 框架输入错误不应用检测器失败策略，调用方不得将其当作通过。
var (
	ErrInvalidInput = errors.New("guardrails: invalid input")
	ErrTextTooLarge = errors.New("guardrails: text exceeds configured limit")
	// ErrDetectorTextTooLarge 由检测器返回，表示文本超过检测器自身上限；框架记为 TextTooLarge 失败，按规则失败策略处置。
	ErrDetectorTextTooLarge = errors.New("guardrails: text exceeds detector limit")
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
// 返回 error 时 Action 恒为 Block、Blocking 为 nil，RuleEvaluations 保留出错前已完成规则的执行结果，调用方仍可记录。
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
	return c.runStage(ctx, stage, text)
}

// runStage 并行执行同阶段规则；blockCtx 只因拦截被取消，父 ctx 取消仍作为框架错误返回。
func (c *Checker) runStage(parent context.Context, stage Stage, text string) (CheckResult, error) {
	blockCtx, cancel := context.WithCancel(parent)
	defer cancel()
	var staged []Rule
	for _, rule := range c.rules {
		if rule.Stage == stage {
			staged = append(staged, rule)
		}
	}
	// 先定长分配再按下标取址，goroutine 写入的位置在整个执行期间固定。
	slots := make([]slot, len(staged))
	var wg sync.WaitGroup
	for i, rule := range staged {
		wg.Add(1)
		go func(rule Rule, s *slot) {
			defer wg.Done()
			s.evaluation, s.state, s.err = c.evaluateRule(parent, blockCtx, rule, text)
			if s.state == completed && s.evaluation.Action == Block {
				cancel()
			}
		}(rule, &slots[i])
	}
	wg.Wait()
	checkResult := CheckResult{Action: Allow}
	for i := range slots {
		if slots[i].state == completed {
			checkResult.RuleEvaluations = append(checkResult.RuleEvaluations, slots[i].evaluation)
		}
	}
	// 父请求取消是框架错误，即使所有检测器都已成功返回也不得放行。
	if err := parent.Err(); err != nil {
		checkResult.Action = Block
		return checkResult, err
	}
	for i := range slots {
		if slots[i].err != nil {
			checkResult.Action = Block
			return checkResult, slots[i].err
		}
	}
	for i := range checkResult.RuleEvaluations {
		if checkResult.RuleEvaluations[i].Action == Block {
			checkResult.Action = Block
			checkResult.Blocking = &checkResult.RuleEvaluations[i]
			break
		}
	}
	return checkResult, nil
}

// slot 收集单条规则的并行结果，按配置顺序排列。
type slot struct {
	evaluation RuleEvaluation
	state      evaluationState
	err        error
}
