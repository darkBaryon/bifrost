// 本文件校验并保存规则快照，按阶段编排本地检测与处置。
package guardrails

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Engine 是只读的规则快照，须通过 New 构造，可并发调用；更新配置时构造新实例。
type Engine struct {
	rules        []Rule
	detectors    map[string]Detector
	maxTextBytes int
}

// New 校验配置并复制规则和注册表；检测器实例由调用方持有其生命周期，必须并发安全。
func New(rules []Rule, detectors map[string]Detector, maxTextBytes int) (*Engine, error) {
	if maxTextBytes <= 0 {
		return nil, fmt.Errorf("guardrails: maxTextBytes must be positive")
	}
	e := &Engine{rules: append([]Rule(nil), rules...), detectors: make(map[string]Detector), maxTextBytes: maxTextBytes}
	ids := make(map[string]bool)
	for _, r := range e.rules {
		if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.DetectorID) == "" || ids[r.ID] {
			return nil, fmt.Errorf("guardrails: rule IDs must be nonempty and unique")
		}
		ids[r.ID] = true
		if !validStage(r.Stage) || !validCategory(r.Category) || !validLevel(r.Threshold) || r.Timeout <= 0 {
			return nil, fmt.Errorf("guardrails: invalid stage, category, threshold or timeout in rule %q", r.ID)
		}
		if r.OnMatch != Block && r.OnMatch != Observe {
			return nil, fmt.Errorf("guardrails: invalid match action in rule %q", r.ID)
		}
		if r.OnError != Allow && r.OnError != Block {
			return nil, fmt.Errorf("guardrails: explicit error action required in rule %q", r.ID)
		}
		d := detectors[r.DetectorID]
		if nilDetector(d) {
			return nil, fmt.Errorf("guardrails: detector %q is not registered", r.DetectorID)
		}
		e.detectors[r.DetectorID] = d
	}
	return e, nil
}

// HasRules 用于入口判断某阶段是否需要检查，不表示内容已通过。
func (e *Engine) HasRules(stage Stage) bool {
	for _, r := range e.rules {
		if r.Stage == stage {
			return true
		}
	}
	return false
}

// MaxTextBytes 返回配置的单次完整文本字节上限，供入口在拼接文本前限长。
func (e *Engine) MaxTextBytes() int { return e.maxTextBytes }

// Check 检查完整文本。取消、非法输入及超限返回 error；检测器失败则由规则决定，记录为 Failed。
// 超时依赖检测器协作取消；即使检测器迟返回成功，超过期限的结果也按失败处理。
func (e *Engine) Check(ctx context.Context, stage Stage, text string) (Result, error) {
	result := Result{Action: Block}
	if e == nil || e.maxTextBytes <= 0 || ctx == nil || !validStage(stage) || !utf8.ValidString(text) {
		return result, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(text) > e.maxTextBytes {
		return result, ErrTextTooLarge
	}
	result.Action = Allow
	for _, rule := range e.rules {
		if rule.Stage != stage {
			continue
		}
		event, err := e.run(ctx, rule, text)
		if err != nil {
			result.Action = Block
			return result, err
		}
		result.Events = append(result.Events, event)
		if event.Action == Block {
			result.Action = Block
			break
		}
	}
	return result, nil
}

func (e *Engine) run(ctx context.Context, r Rule, text string) (Event, error) {
	event := Event{RuleID: r.ID, DetectorID: r.DetectorID, Category: r.Category, Stage: r.Stage, Outcome: Passed, Action: Allow}
	if err := ctx.Err(); err != nil {
		return event, err
	}
	detectCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	findings, err := e.detectors[r.DetectorID].Detect(detectCtx, text)
	deadlineErr := detectCtx.Err()
	cancel()
	if parentErr := ctx.Err(); parentErr != nil {
		return event, parentErr
	}
	switch {
	case errors.Is(deadlineErr, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		event.Failure = DetectorTimeout
	case err != nil:
		event.Failure = DetectorError
	default:
		for _, finding := range findings {
			if !validLevel(finding.Level) {
				event.Failure = InvalidResult
				break
			}
			if finding.Level > event.HighestLevel {
				event.HighestLevel = finding.Level
			}
		}
	}
	if event.Failure != "" {
		event.Outcome, event.Action, event.HighestLevel = Failed, r.OnError, 0
	} else if event.HighestLevel >= r.Threshold {
		event.Outcome, event.Action = Matched, r.OnMatch
	}
	return event, nil
}

func validStage(s Stage) bool { return s == Input || s == Output }
func validLevel(l Level) bool { return l >= Low && l <= High }
func validCategory(c Category) bool {
	return c == HarmfulContent || c == Credentials || c == PromptAttack || c == BusinessRule
}

// 注册表允许结构体或指针实现接口，同时拒绝装入接口的 typed nil。
func nilDetector(d Detector) bool {
	if d == nil {
		return true
	}
	v := reflect.ValueOf(d)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
