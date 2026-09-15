// Package guardrails 负责本地内容检测的规则执行；检测算法和网关协议由调用方接入。
package guardrails

import (
	"context"
	"errors"
	"time"
)

// Stage 表示检查输入还是完整输出。
type Stage string

// 检测阶段使用业务名称，与网关协议无关。
const (
	Input  Stage = "input"
	Output Stage = "output"
)

// Category 保留产品已确认的检测项目编号。
type Category string

// 本期允许接入的四类检测；常量存在不代表算法已实现。
const (
	HarmfulContent Category = "D1"
	Credentials    Category = "D3"
	PromptAttack   Category = "D4"
	BusinessRule   Category = "D5"
)

// Level 是检测器归一后的风险等级；零值无效，未发现风险用空结果表达。
type Level uint8

// 等级只表达相对高低，具体判定由检测器负责。
const (
	Low Level = iota + 1
	Medium
	High
)

// Action 表示网关应执行的动作。
type Action string

// 规则命中支持 Block/Observe，检测失败支持 Allow/Block。
const (
	Allow   Action = "allow"
	Block   Action = "block"
	Observe Action = "observe"
)

// Finding 只携带风险等级，不保留命中原文。
type Finding struct{ Level Level }

// Detector 是本地检测器契约。实现必须支持并发并响应 ctx 取消；调用为同步，框架不强杀 goroutine。
type Detector interface {
	Detect(ctx context.Context, text string) ([]Finding, error)
}

// Rule 显式指定一次检测的条件和处置；所有字段必填，不隐含失败放行策略。
type Rule struct {
	ID         string
	DetectorID string
	Category   Category
	Stage      Stage
	Threshold  Level
	OnMatch    Action
	OnError    Action
	Timeout    time.Duration
}

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

// Event 是单条规则的执行记录；不包含检测正文或异常原文。
type Event struct {
	RuleID       string
	DetectorID   string
	Category     Category
	Stage        Stage
	Outcome      Outcome
	Action       Action
	Failure      Failure
	HighestLevel Level
}

// Result 包含最终 Allow/Block 决定及已执行规则；阻断之后的规则不执行。
type Result struct {
	Action Action
	Events []Event
}

// 框架输入错误不应用检测器失败策略，调用方不得将其当作通过。
var (
	ErrInvalidInput = errors.New("guardrails: invalid input")
	ErrTextTooLarge = errors.New("guardrails: text exceeds configured limit")
)

// ErrorCode 是入口返回的安全错误类别，避免用普通插件 error 冒充阻断。
type ErrorCode string

// 安全拦截与无法完成检查使用不同错误码。
const (
	ContentBlocked          ErrorCode = "content_safety_blocked"
	CheckFailed             ErrorCode = "content_safety_check_failed"
	UnsupportedContent      ErrorCode = "content_safety_unsupported_content"
	UnsupportedOutputStream ErrorCode = "content_safety_output_stream_unsupported"
	TextTooLarge            ErrorCode = "content_safety_text_too_large"
	CheckCanceled           ErrorCode = "content_safety_canceled"
)
