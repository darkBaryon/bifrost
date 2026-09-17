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

func (c *Checker) evaluateRule(ctx context.Context, rule Rule, text string) (RuleEvaluation, error) {
	ruleEvaluation := RuleEvaluation{
		RuleID: rule.ID, DetectorID: rule.DetectorID,
		Category: rule.Category, Stage: rule.Stage,
		Outcome: Passed, Action: Allow,
	}
	if err := ctx.Err(); err != nil {
		return ruleEvaluation, err
	}
	detectCtx, cancel := context.WithTimeout(ctx, rule.Timeout)
	findings, err := c.detectors[rule.DetectorID].Detect(detectCtx, text)
	deadlineErr := detectCtx.Err()
	cancel()
	if parentErr := ctx.Err(); parentErr != nil {
		return ruleEvaluation, parentErr
	}
	switch {
	case errors.Is(deadlineErr, context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		ruleEvaluation.Failure = DetectorTimeout
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
	return ruleEvaluation, nil
}
