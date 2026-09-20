// 本文件加载并编译随二进制嵌入的密钥规则数据（data/rules.json，源自 Gitleaks，见 data/NOTICE）。
package secrets

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

//go:embed data/rules.json
var embeddedRules []byte

// 规则数据文件的序列化结构；字段含义见 ee/scripts/secrets-rules-convert.py。
type rulesFile struct {
	GlobalAllow allowFile  `json:"global_allow"`
	Rules       []ruleFile `json:"rules"`
}

type ruleFile struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Regex       string      `json:"regex"`
	Keywords    []string    `json:"keywords"`
	Entropy     float64     `json:"entropy"`
	SecretGroup int         `json:"secret_group"`
	Level       string      `json:"level"`
	Allow       []allowFile `json:"allow"`
}

type allowFile struct {
	Target    string   `json:"target"`
	Regexes   []string `json:"regexes"`
	StopWords []string `json:"stop_words"`
}

// rule 是编译后的单条规则。
type rule struct {
	id          string
	regex       *regexp.Regexp
	keywords    []string // 小写；空表示对所有文本执行
	entropy     float64  // 0 表示不检查熵
	secretGroup int
	level       guardrails.Level
	generic     bool // id 以 generic- 开头；同一命中值已被专用规则命中时丢弃
	allow       []allowlist
}

// allowlist 描述一组豁免条件：正则命中 target 对象或密钥值含任一停用词即豁免。
type allowlist struct {
	target    allowTarget
	regexes   []*regexp.Regexp
	stopWords []string // 小写
}

type allowTarget uint8

const (
	targetSecret allowTarget = iota // 捕获出的密钥值（默认）
	targetMatch                     // 正则整体匹配到的片段
	targetLine                      // 匹配所在的整行
)

// genericPrefix 标记无法归属厂商的通用规则，其结果在专用规则命中同一值时去重。
const genericPrefix = "generic-"

func loadRules(data []byte) ([]rule, allowlist, error) {
	var file rulesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, allowlist{}, fmt.Errorf("secrets: parse rules: %w", err)
	}
	global, err := compileAllow(allowFile{Target: "secret", Regexes: file.GlobalAllow.Regexes, StopWords: file.GlobalAllow.StopWords})
	if err != nil {
		return nil, allowlist{}, fmt.Errorf("secrets: global allowlist: %w", err)
	}
	rules := make([]rule, 0, len(file.Rules))
	seen := make(map[string]bool, len(file.Rules))
	for _, rf := range file.Rules {
		if rf.ID == "" || seen[rf.ID] {
			return nil, allowlist{}, fmt.Errorf("secrets: rule id %q empty or duplicated", rf.ID)
		}
		seen[rf.ID] = true
		re, err := regexp.Compile(rf.Regex)
		if err != nil {
			return nil, allowlist{}, fmt.Errorf("secrets: rule %s: %w", rf.ID, err)
		}
		if rf.SecretGroup < 0 || rf.SecretGroup > re.NumSubexp() {
			return nil, allowlist{}, fmt.Errorf("secrets: rule %s: secret_group %d out of range", rf.ID, rf.SecretGroup)
		}
		level, ok := guardrails.ParseLevel(rf.Level)
		if !ok {
			return nil, allowlist{}, fmt.Errorf("secrets: rule %s: unknown level %q", rf.ID, rf.Level)
		}
		r := rule{id: rf.ID, regex: re, entropy: rf.Entropy, secretGroup: rf.SecretGroup, level: level, generic: strings.HasPrefix(rf.ID, genericPrefix)}
		for _, k := range rf.Keywords {
			if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
				r.keywords = append(r.keywords, k)
			}
		}
		for _, af := range rf.Allow {
			al, err := compileAllow(af)
			if err != nil {
				return nil, allowlist{}, fmt.Errorf("secrets: rule %s allowlist: %w", rf.ID, err)
			}
			r.allow = append(r.allow, al)
		}
		rules = append(rules, r)
	}
	return rules, global, nil
}

func compileAllow(af allowFile) (allowlist, error) {
	al := allowlist{}
	switch af.Target {
	case "", "secret":
		al.target = targetSecret
	case "match":
		al.target = targetMatch
	case "line":
		al.target = targetLine
	default:
		return al, fmt.Errorf("unknown allowlist target %q", af.Target)
	}
	for _, pattern := range af.Regexes {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return al, err
		}
		al.regexes = append(al.regexes, re)
	}
	for _, w := range af.StopWords {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			al.stopWords = append(al.stopWords, w)
		}
	}
	return al, nil
}
