// 本文件实现扫描流程：关键词预筛 → 正则 → 取密钥值 → 熵 → 白名单 → 通用规则去重。
package secrets

import (
	"context"
	"math"
	"strings"
)

// 每扫描这么多条规则检查一次取消，避免长文本上的扫描无法中断。
const cancelCheckEvery = 32

// finding 是内部命中记录；对外只暴露等级，不暴露值和位置。
type finding struct {
	rule   *rule
	secret string
}

func (d *Detector) scan(ctx context.Context, text string) ([]finding, error) {
	lowered := strings.ToLower(text)
	var findings []finding
	for i := range d.rules {
		if i%cancelCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		r := &d.rules[i]
		if !keywordHit(lowered, r.keywords) {
			continue
		}
		findings = append(findings, d.scanRule(r, text)...)
	}
	return dedupeGeneric(findings), nil
}

// keywordHit 判断规则是否值得执行；无关键词的规则总是执行。
func keywordHit(lowered string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	for _, k := range keywords {
		if strings.Contains(lowered, k) {
			return true
		}
	}
	return false
}

func (d *Detector) scanRule(r *rule, text string) []finding {
	var out []finding
	for _, loc := range r.regex.FindAllStringSubmatchIndex(text, -1) {
		match := strings.Trim(text[loc[0]:loc[1]], "\n")
		secret := extractSecret(r, text, loc, match)
		if secret == "" || strings.HasPrefix(secret, gatewayKeyPrefix) {
			continue
		}
		if r.entropy != 0 && shannonEntropy(secret) <= r.entropy {
			continue
		}
		line := lineOf(text, loc[0], loc[1])
		if d.global.allows(secret, match, line) || d.ignored(secret) {
			continue
		}
		allowed := false
		for _, al := range r.allow {
			if al.allows(secret, match, line) {
				allowed = true
				break
			}
		}
		if allowed {
			continue
		}
		out = append(out, finding{rule: r, secret: secret})
	}
	return out
}

// extractSecret 按 secret_group 取密钥值；未指定时取第一个非空捕获组，没有捕获组则取整体匹配。
func extractSecret(r *rule, text string, loc []int, match string) string {
	groups := len(loc)/2 - 1
	if r.secretGroup > 0 {
		start, end := loc[2*r.secretGroup], loc[2*r.secretGroup+1]
		if start < 0 {
			return ""
		}
		return text[start:end]
	}
	for g := 1; g <= groups; g++ {
		start, end := loc[2*g], loc[2*g+1]
		if start >= 0 && end > start {
			return text[start:end]
		}
	}
	return match
}

// lineOf 返回匹配所在的整行（跨行匹配时取首尾所在行之间的全部内容）。
func lineOf(text string, start, end int) string {
	from := strings.LastIndexByte(text[:start], '\n') + 1
	to := end
	if idx := strings.IndexByte(text[end:], '\n'); idx >= 0 {
		to = end + idx
	}
	return text[from:to]
}

// allows 与 Gitleaks 一致：正则按 target 选择比较对象，停用词始终只对密钥值判断。
func (al allowlist) allows(secret, match, line string) bool {
	target := secret
	switch al.target {
	case targetMatch:
		target = match
	case targetLine:
		target = line
	}
	for _, re := range al.regexes {
		if re.MatchString(target) {
			return true
		}
	}
	if len(al.stopWords) > 0 {
		lowered := strings.ToLower(secret)
		for _, w := range al.stopWords {
			if strings.Contains(lowered, w) {
				return true
			}
		}
	}
	return false
}

// ignored 应用管理员忽略词：命中值含任一子串即丢弃（不区分大小写）。
func (d *Detector) ignored(secret string) bool {
	if len(d.ignoredKeywords) == 0 {
		return false
	}
	lowered := strings.ToLower(secret)
	for _, w := range d.ignoredKeywords {
		if strings.Contains(lowered, w) {
			return true
		}
	}
	return false
}

// dedupeGeneric 去掉与专用规则命中同一值的通用规则结果。
func dedupeGeneric(findings []finding) []finding {
	specific := make(map[string]bool)
	for _, f := range findings {
		if !f.rule.generic {
			specific[f.secret] = true
		}
	}
	out := findings[:0]
	for _, f := range findings {
		if f.rule.generic && specific[f.secret] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// shannonEntropy 按字节频率计算香农熵，与 Gitleaks 一致。
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	entropy := 0.0
	length := float64(len(s))
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}
