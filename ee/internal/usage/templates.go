// Package usage 定义额度模板配置；本期不执行个人分配或请求扣额。
package usage

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Error 是可公开的模板错误码。
type Error string

// ErrInvalid 表示模板字段不符合合同。
const ErrInvalid Error = "invalid"

func (e Error) Error() string { return string(e) }

// ResetDuration 表示UTC自然周期。
type ResetDuration string

// 模板合同允许的自然周期；周从周一开始。
const (
	Day   ResetDuration = "1d"
	Week  ResetDuration = "1w"
	Month ResetDuration = "1M"
	Year  ResetDuration = "1Y"
)

// 模板合同的字段及分页上限。
const (
	MaxName         = 100
	MaxDescription  = 1000
	MaxModels       = 200
	DefaultPageSize = 20
	MaxPageSize     = 100
	minuteSeconds   = 60
	hourSeconds     = 60 * minuteSeconds
	daySeconds      = 24 * hourSeconds
)

// RateLimit 保存可选的token与调用频率限制；零表示未配置该维度。
type RateLimit struct {
	TokenMax, RequestMax int64
	WindowSeconds        int
}

// Config 是模板配置，不包含员工或已用量。
type Config struct {
	MaxCostUSD    float64
	ResetDuration ResetDuration
	AllowedModels []string
	RateLimit     *RateLimit
}

// Template 是独立配置值，Version用于乐观并发控制。
type Template struct {
	ID, Name, Description string
	Config                Config
	Version               int64
}

// Normalize 校验并复制配置，默认自然月；不检查厂商是否已装配。
func Normalize(t Template) (Template, error) {
	t.Name = strings.TrimSpace(t.Name)
	c := &t.Config
	if c.ResetDuration == "" {
		c.ResetDuration = Month
	}
	if utf8.RuneCountInString(t.Name) < 1 || utf8.RuneCountInString(t.Name) > MaxName || utf8.RuneCountInString(t.Description) > MaxDescription ||
		math.IsNaN(c.MaxCostUSD) || math.IsInf(c.MaxCostUSD, 0) || c.MaxCostUSD <= 0 || len(c.AllowedModels) < 1 || len(c.AllowedModels) > MaxModels {
		return Template{}, ErrInvalid
	}
	switch c.ResetDuration {
	case Day, Week, Month, Year:
	default:
		return Template{}, ErrInvalid
	}
	for _, model := range c.AllowedModels {
		if model == "*" {
			continue
		}
		provider, name, found := strings.Cut(model, "/")
		if !found || provider == "" || name == "" || strings.Contains(provider, "*") || strings.IndexFunc(model, unicode.IsSpace) >= 0 || (name != "*" && strings.Contains(name, "*")) {
			return Template{}, ErrInvalid
		}
	}
	c.AllowedModels = append([]string{}, c.AllowedModels...)
	if c.RateLimit != nil {
		rate := *c.RateLimit
		if rate.TokenMax < 0 || rate.RequestMax < 0 || (rate.TokenMax == 0 && rate.RequestMax == 0) ||
			(rate.WindowSeconds != minuteSeconds && rate.WindowSeconds != hourSeconds && rate.WindowSeconds != daySeconds) {
			return Template{}, ErrInvalid
		}
		c.RateLimit = &rate
	}
	return t, nil
}
