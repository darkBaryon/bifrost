// Package judge 用大模型判官识别有害内容、提示词攻击和违反管理员业务规则的文本（检测项目 D1/D4/D5）。
// 判官模型由调用方以 Model 接口注入；本包只组织提示词、解析等级并重试，不依赖网关类型。
package judge

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// Model 是判官模型的窄接口：system 为提示词，user 为待审核 JSON，返回助手正文。
// 实现须响应 ctx 取消并返回 ctx.Err()；调用失败返回 error，不得把失败伪装成"无风险"。
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Kind 选择内置提示词。
type Kind string

// 三类判官：有害内容、提示词攻击各用固定提示词，业务规则把管理员规则填入模板。
const (
	Harmful      Kind = "harmful"
	PromptAttack Kind = "prompt_attack"
	BusinessRule Kind = "business_rule"
)

// 应用层重试次数范围（用户裁决 3–5 次）与退避基数；总时长受规则 Timeout 约束。
const (
	minRetries     = 3
	maxRetries     = 5
	DefaultRetries = 3
	retryBackoff   = 200 * time.Millisecond
	// defaultMaxBytes 是未注入上限时的送检上限；装配方总会传入 config 的判官上限，这里只需与 config.DefaultMaxTextBytes 同步。
	defaultMaxBytes = 64 * 1024
	// rulePlaceholder 是业务规则模板中的替换位。
	rulePlaceholder = "{{rule}}"
)

// Options 描述一个判官实例；每个检测项目、每条业务规则各建一个实例。
type Options struct {
	Model    Model
	Kind     Kind
	Rule     string           // Kind 为 BusinessRule 时必填，其余必须为空
	Stage    guardrails.Stage // 写入送检 JSON 的 stage 字段
	Retries  int              // 0 表示 DefaultRetries；否则须在 minRetries–maxRetries 之间
	MaxBytes int              // 0 表示 defaultMaxBytes
}

// Detector 是一个判官实例；无状态、可并发调用。
type Detector struct {
	model    Model
	system   string
	stage    guardrails.Stage
	retries  int
	maxBytes int
}

// 提示词首句含"审核器"字样；冒烟脚本的模型替身靠它识别判官请求（见 ee/scripts/guardrails-smoke.py），改写开头时同步。
//
//go:embed prompts/*.md
var promptFiles embed.FS

// New 校验选项并装配提示词。
func New(opts Options) (*Detector, error) {
	if opts.Model == nil {
		return nil, errors.New("judge: model is required")
	}
	if opts.Stage != guardrails.Input && opts.Stage != guardrails.Output {
		return nil, fmt.Errorf("judge: invalid stage %q", opts.Stage)
	}
	system, err := systemPrompt(opts.Kind, opts.Rule)
	if err != nil {
		return nil, err
	}
	retries := opts.Retries
	if retries == 0 {
		retries = DefaultRetries
	}
	if retries < minRetries || retries > maxRetries {
		return nil, fmt.Errorf("judge: retries must be between %d and %d", minRetries, maxRetries)
	}
	maxBytes := opts.MaxBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBytes < 0 {
		return nil, errors.New("judge: max bytes must be positive")
	}
	return &Detector{model: opts.Model, system: system, stage: opts.Stage, retries: retries, maxBytes: maxBytes}, nil
}

func systemPrompt(kind Kind, rule string) (string, error) {
	rule = strings.TrimSpace(rule)
	switch kind {
	case Harmful, PromptAttack:
		if rule != "" {
			return "", fmt.Errorf("judge: rule text is only accepted for %s", BusinessRule)
		}
	case BusinessRule:
		if rule == "" {
			return "", errors.New("judge: business rule text is required")
		}
	default:
		return "", fmt.Errorf("judge: unknown kind %q", kind)
	}
	raw, err := promptFiles.ReadFile("prompts/" + string(kind) + ".md")
	if err != nil {
		return "", fmt.Errorf("judge: load prompt: %w", err)
	}
	prompt := strings.TrimSpace(string(raw))
	if kind == BusinessRule {
		if !strings.Contains(prompt, rulePlaceholder) {
			return "", errors.New("judge: business rule prompt lacks placeholder")
		}
		prompt = strings.Replace(prompt, rulePlaceholder, rule, 1)
	}
	return prompt, nil
}

// Detect 调用判官并把等级转换为 Finding；超限不重试，调用失败或结果非法按退避重试，仍失败返回最后一次错误。
func (d *Detector) Detect(ctx context.Context, text string) ([]guardrails.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(text) > d.maxBytes {
		return nil, guardrails.ErrDetectorTextTooLarge
	}
	user, err := json.Marshal(payload{Stage: string(d.stage), Text: text})
	if err != nil {
		return nil, fmt.Errorf("judge: encode payload: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= d.retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, retryBackoff<<(attempt-1)); err != nil {
				return nil, err
			}
		}
		level, err := d.ask(ctx, string(user))
		if err == nil {
			if level == 0 {
				return nil, nil
			}
			return []guardrails.Finding{{Level: level}}, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lastErr = err
	}
	return nil, lastErr
}

type payload struct {
	Stage string `json:"stage"`
	Text  string `json:"text"`
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
