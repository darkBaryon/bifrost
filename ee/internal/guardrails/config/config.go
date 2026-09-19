// Package config 是内容安全配置的序列化边界：解析并校验管理员提交的 JSON，生成根包规则。
// 本包只用标准库；带 json 标签的类型只在这里出现，检测器与规则仍是根包的业务类型。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// 配置数值边界与默认值；产品裁决见 workbench 方案 v5 §4.5。
// 判官重试范围与默认值须与 detectors/judge 的 minRetries/maxRetries/DefaultRetries 一致；judge 不依赖本包，故各写一份并互相注明。
const (
	DefaultMaxTextBytes     = 64 * 1024
	DefaultJudgeRetries     = 3
	defaultJudgeTimeoutMS   = 10_000
	defaultSecretsTimeoutMS = 500
	minJudgeRetries         = 3
	maxJudgeRetries         = 5
)

// ErrInvalid 标记配置校验失败；错误文本说明具体字段。
var ErrInvalid = errors.New("guardrails config: invalid")

// Config 是管理员提交的完整配置。
type Config struct {
	Deny          Deny           `json:"deny"`
	MaxTextBytes  int            `json:"max_text_bytes"`
	Judge         Judge          `json:"judge"`
	Secrets       Secrets        `json:"secrets"`
	Harmful       Item           `json:"harmful"`
	PromptAttack  Item           `json:"prompt_attack"`
	BusinessRules []BusinessRule `json:"business_rules"`
}

// Deny 是拦截时返回给客户端的状态码与文案。
type Deny struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// Judge 是所有判官实例共用的模型与调用参数。
type Judge struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Retries      int    `json:"retries"`
	TimeoutMS    int    `json:"timeout_ms"`
	MaxTextBytes int    `json:"max_text_bytes"`
}

// Item 是一个检测项目在各阶段的启用与处置。
type Item struct {
	Enabled   bool     `json:"enabled"`
	Stages    []string `json:"stages"`
	Threshold string   `json:"threshold"`
	OnMatch   string   `json:"on_match"`
	OnError   string   `json:"on_error"`
}

// Secrets 在 Item 之上增加密钥检测自己的超时与忽略词。
type Secrets struct {
	Item
	TimeoutMS       int      `json:"timeout_ms"`
	IgnoredKeywords []string `json:"ignored_keywords"`
}

// BusinessRule 是一条管理员用自然语言写的规则。
type BusinessRule struct {
	Item
	ID   string `json:"id"`
	Rule string `json:"rule"`
}

// Parse 解析 JSON（拒绝未知字段）、填默认值并校验。
func Parse(data []byte) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.More() {
		return Config{}, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.MaxTextBytes == 0 {
		c.MaxTextBytes = DefaultMaxTextBytes
	}
	if c.Judge.Retries == 0 {
		c.Judge.Retries = DefaultJudgeRetries
	}
	if c.Judge.TimeoutMS == 0 {
		c.Judge.TimeoutMS = defaultJudgeTimeoutMS
	}
	if c.Judge.MaxTextBytes == 0 {
		c.Judge.MaxTextBytes = c.MaxTextBytes
	}
	if c.Secrets.TimeoutMS == 0 {
		c.Secrets.TimeoutMS = defaultSecretsTimeoutMS
	}
}

// validate 检查全部字段；已启用的判官项目要求 judge 配置完整。错误信息按固定顺序给出第一处问题。
func (c Config) validate() error {
	if c.Deny.Status < guardrails.MinDenyStatus || c.Deny.Status > guardrails.MaxDenyStatus || strings.TrimSpace(c.Deny.Message) == "" {
		return fmt.Errorf("%w: deny.status must be %d–%d and deny.message non-empty", ErrInvalid, guardrails.MinDenyStatus, guardrails.MaxDenyStatus)
	}
	if c.MaxTextBytes <= 0 || c.Judge.MaxTextBytes <= 0 || c.Judge.TimeoutMS <= 0 || c.Secrets.TimeoutMS <= 0 {
		return fmt.Errorf("%w: byte limits and timeouts must be positive", ErrInvalid)
	}
	if c.Judge.MaxTextBytes > c.MaxTextBytes {
		return fmt.Errorf("%w: judge.max_text_bytes exceeds max_text_bytes", ErrInvalid)
	}
	if c.Judge.Retries < minJudgeRetries || c.Judge.Retries > maxJudgeRetries {
		return fmt.Errorf("%w: judge.retries must be %d–%d", ErrInvalid, minJudgeRetries, maxJudgeRetries)
	}
	enabled := 0
	// 顺序与 Expand 一致，同一份坏配置总是报同一个字段。
	for _, entry := range []struct {
		name string
		item Item
	}{{itemSecrets, c.Secrets.Item}, {ItemHarmful, c.Harmful}, {ItemPromptAttack, c.PromptAttack}} {
		if !entry.item.Enabled {
			continue
		}
		enabled++
		if err := entry.item.validate(entry.name); err != nil {
			return err
		}
	}
	ids := make(map[string]bool, len(c.BusinessRules))
	for _, br := range c.BusinessRules {
		id := strings.TrimSpace(br.ID)
		if id == "" || strings.Contains(id, ":") || ids[id] {
			return fmt.Errorf("%w: business rule id %q empty, duplicated or contains ':'", ErrInvalid, br.ID)
		}
		ids[id] = true
		if strings.TrimSpace(br.Rule) == "" {
			return fmt.Errorf("%w: business rule %q has empty rule text", ErrInvalid, id)
		}
		if !br.Enabled {
			continue
		}
		enabled++
		if err := br.validate("business_rules." + id); err != nil {
			return err
		}
	}
	if enabled == 0 {
		return fmt.Errorf("%w: no detection item enabled", ErrInvalid)
	}
	if c.judgeNeeded() && (strings.TrimSpace(c.Judge.Provider) == "" || strings.TrimSpace(c.Judge.Model) == "") {
		return fmt.Errorf("%w: judge.provider and judge.model are required by enabled judge items", ErrInvalid)
	}
	return nil
}

func (i Item) validate(name string) error {
	if len(i.Stages) == 0 {
		return fmt.Errorf("%w: %s.stages is empty", ErrInvalid, name)
	}
	seen := make(map[string]bool, 2)
	for _, s := range i.Stages {
		if (s != string(guardrails.Input) && s != string(guardrails.Output)) || seen[s] {
			return fmt.Errorf("%w: %s.stages has invalid or repeated stage %q", ErrInvalid, name, s)
		}
		seen[s] = true
	}
	if _, ok := guardrails.ParseLevel(i.Threshold); !ok {
		return fmt.Errorf("%w: %s.threshold must be low, medium or high", ErrInvalid, name)
	}
	if i.OnMatch != string(guardrails.Block) && i.OnMatch != string(guardrails.Observe) {
		return fmt.Errorf("%w: %s.on_match must be block or observe", ErrInvalid, name)
	}
	if i.OnError != string(guardrails.Block) && i.OnError != string(guardrails.Allow) {
		return fmt.Errorf("%w: %s.on_error must be block or allow", ErrInvalid, name)
	}
	return nil
}

// judgeNeeded 报告是否有启用的判官类项目。
func (c Config) judgeNeeded() bool {
	if c.Harmful.Enabled || c.PromptAttack.Enabled {
		return true
	}
	for _, br := range c.BusinessRules {
		if br.Enabled {
			return true
		}
	}
	return false
}

// JudgeTimeout 与 SecretsTimeout 把毫秒配置转换为时长。
func (c Config) JudgeTimeout() time.Duration {
	return time.Duration(c.Judge.TimeoutMS) * time.Millisecond
}
func (c Config) SecretsTimeout() time.Duration {
	return time.Duration(c.Secrets.TimeoutMS) * time.Millisecond
}
