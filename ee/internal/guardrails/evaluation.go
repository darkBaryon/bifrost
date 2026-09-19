// 本文件执行单条规则，将检测结果和失败情况转换为处置及执行记录。
package guardrails

import (
	"context"
	"errors"
)

// Outcome 区分检测通过、风险命中和检测失败。
type Outcome string

// 检测失败不能记成安全通过。
const (
	Passed  Outcome = "passed"
	Matched Outcome = "matched"
	Failed  Outcome = "failed"
)

// Failure 记录可安全输出的错误类别，不透传检测器错误文本。
type Failure string

// 框架检测器错误分类。
const (
	DetectorError   Failure = "detector_error"
	DetectorTimeout Failure = "detector_timeout"
	InvalidResult   Failure = "invalid_result"
	// TextTooLarge 表示检测器声明文本超过其自身上限（ErrDetectorTextTooLarge），与检测器故障区分。
	TextTooLarge Failure = "text_too_large"
)

// RuleEvaluation 是单条规则的风险等级、命中或失败情况及处置结果；不包含检测正文或异常原文。
type RuleEvaluation struct {
	RuleID       string
	DetectorID   string
	Category     Category
	Stage        Stage
	Outcome      Outcome
	Action       Action
	Failure      Failure
	HighestLevel Level
}

// evaluationState 区分规则已完成、因其他规则拦截被取消，以及父请求取消。
type evaluationState uint8

const (
	completed evaluationState = iota
	discarded
)

// evaluateRule 用 blockCtx 派生检测超时；检测因取消返回时，父取消是框架错误、拦截取消则丢弃，其余按结果记录。
func (c *Checker) evaluateRule(parent, blockCtx context.Context, rule Rule, text string) (RuleEvaluation, evaluationState, error) {
	ruleEvaluation := RuleEvaluation{
		RuleID: rule.ID, DetectorID: rule.DetectorID,
		Category: rule.Category, Stage: rule.Stage,
		Outcome: Passed, Action: Allow,
	}
	if err := parent.Err(); err != nil {
		return ruleEvaluation, discarded, err
	}
	if blockCtx.Err() != nil {
		return ruleEvaluation, discarded, nil
	}
	detectCtx, cancel := context.WithTimeout(blockCtx, rule.Timeout)
	findings, err := c.detectors[rule.DetectorID].Detect(detectCtx, text)
	deadlineErr := detectCtx.Err()
	cancel()
	// 检测器只有因取消而返回（按契约返回 ctx.Err()）时才区分父取消（框架错误）与拦截取消（丢弃）；其余返回一律记录。
	if errors.Is(err, context.Canceled) {
		if parentErr := parent.Err(); parentErr != nil {
			return ruleEvaluation, discarded, parentErr
		}
		if blockCtx.Err() != nil {
			return ruleEvaluation, discarded, nil
		}
	}
	switch {
	case errors.Is(deadlineErr, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		ruleEvaluation.Failure = DetectorTimeout
	case errors.Is(err, ErrDetectorTextTooLarge):
		ruleEvaluation.Failure = TextTooLarge
	case err != nil:
		ruleEvaluation.Failure = DetectorError
	default:
		for _, finding := range findings {
			if !validLevel(finding.Level) {
				ruleEvaluation.Failure = InvalidResult
				break
			}
			if finding.Level > ruleEvaluation.HighestLevel {
				ruleEvaluation.HighestLevel = finding.Level
			}
		}
	}
	if ruleEvaluation.Failure != "" {
		ruleEvaluation.Outcome, ruleEvaluation.Action, ruleEvaluation.HighestLevel = Failed, rule.OnError, 0
	} else if ruleEvaluation.HighestLevel >= rule.Threshold {
		ruleEvaluation.Outcome, ruleEvaluation.Action = Matched, rule.OnMatch
	}
	return ruleEvaluation, completed, nil
}
