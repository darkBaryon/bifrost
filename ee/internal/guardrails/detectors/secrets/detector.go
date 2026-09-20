// Package secrets 用规则在本地识别文本中的 API Key、访问凭据和私钥（检测项目 D3），不调用外部服务。
// 规则数据源自 Gitleaks 并按本仓核实结果修正与补充；扫描逻辑自行实现。
package secrets

import (
	"context"
	"sort"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// gatewayKeyPrefix 是 Bifrost 自身虚拟 key 的前缀，命中值以它开头时一律不报，避免用户提及网关 key 被拦。
const gatewayKeyPrefix = "sk-bf-"

// Options 是装配方可调的参数；规则本身不可配置。
type Options struct {
	// IgnoredKeywords 是管理员的误报止血词：命中值包含任一子串（不区分大小写）即丢弃。
	IgnoredKeywords []string
}

// Detector 持有编译后的规则；无状态、可并发调用。
type Detector struct {
	rules           []rule
	global          allowlist
	ignoredKeywords []string
}

// New 加载嵌入的规则数据并编译；规则数据非法时返回错误（属构建缺陷，不应在运行期出现）。
func New(opts Options) (*Detector, error) {
	rules, global, err := loadRules(embeddedRules)
	if err != nil {
		return nil, err
	}
	return &Detector{rules: rules, global: global, ignoredKeywords: normalizeKeywords(opts.IgnoredKeywords)}, nil
}

// RuleCount 返回已加载的规则数；测试用它锚定数据文件版本，规则数变化时须同步更新断言。
func (d *Detector) RuleCount() int { return len(d.rules) }

// Detect 返回每个命中一条 Finding，只携带等级；不返回命中值与位置。被取消时返回 ctx.Err()。
func (d *Detector) Detect(ctx context.Context, text string) ([]guardrails.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	findings, err := d.scan(ctx, text)
	if err != nil {
		return nil, err
	}
	out := make([]guardrails.Finding, 0, len(findings))
	for _, f := range findings {
		out = append(out, guardrails.Finding{Level: f.rule.level})
	}
	return out, nil
}

func normalizeKeywords(words []string) []string {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			set[w] = true
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}
