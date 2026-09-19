// 本文件把已校验的配置展开为根包规则；检测器实例由装配方按 DetectorID 提供。
package config

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
)

// 检测项目名，作为规则 ID 与判官实例 ID 的前缀。
const (
	itemSecrets      = "secrets"
	ItemHarmful      = "harmful"
	ItemPromptAttack = "prompt_attack"
	ItemBusinessRule = "business_rule"
	// SecretsDetectorID 是密钥检测器的固定注册 ID。
	SecretsDetectorID = "secrets"
	// judgeDetectorPrefix 拼出判官实例 ID：judge:<项目>[:<规则 ID>]:<阶段>。
	judgeDetectorPrefix = "judge:"
)

// JudgeSpec 描述装配方需要创建的一个判官实例。
type JudgeSpec struct {
	DetectorID string
	Item       string           // ItemHarmful / ItemPromptAttack / ItemBusinessRule
	RuleID     string           // 业务规则 ID；其余为空
	RuleText   string           // 业务规则正文；其余为空
	Stage      guardrails.Stage // 判官送检 JSON 的 stage
}

// Plan 是配置展开后的规则集与所需检测器清单。
type Plan struct {
	Rules        []guardrails.Rule
	Judges       []JudgeSpec
	NeedsSecrets bool
}

// Expand 按固定顺序（密钥 → 有害 → 提示词攻击 → 业务规则）生成规则；同项目多阶段各一条规则。
// 调用前须先通过 Validate。
func (c Config) Expand() Plan {
	var p Plan
	if c.Secrets.Enabled {
		p.NeedsSecrets = true
		for _, stage := range c.Secrets.Stages {
			p.Rules = append(p.Rules, rule(itemSecrets+":"+stage, SecretsDetectorID, guardrails.Credentials, stage, c.Secrets.Item, c.SecretsTimeout()))
		}
	}
	addJudge := func(item, ruleID, ruleText string, category guardrails.Category, it Item) {
		for _, stage := range it.Stages {
			id := item
			if ruleID != "" {
				id += ":" + ruleID
			}
			detectorID := judgeDetectorPrefix + id + ":" + stage
			p.Judges = append(p.Judges, JudgeSpec{DetectorID: detectorID, Item: item, RuleID: ruleID, RuleText: ruleText, Stage: guardrails.Stage(stage)})
			p.Rules = append(p.Rules, rule(id+":"+stage, detectorID, category, stage, it, c.JudgeTimeout()))
		}
	}
	if c.Harmful.Enabled {
		addJudge(ItemHarmful, "", "", guardrails.HarmfulContent, c.Harmful)
	}
	if c.PromptAttack.Enabled {
		addJudge(ItemPromptAttack, "", "", guardrails.PromptAttack, c.PromptAttack)
	}
	for _, br := range c.BusinessRules {
		if br.Enabled {
			addJudge(ItemBusinessRule, br.ID, br.Rule, guardrails.BusinessRule, br.Item)
		}
	}
	return p
}

func rule(id, detectorID string, category guardrails.Category, stage string, it Item, timeout time.Duration) guardrails.Rule {
	threshold, _ := guardrails.ParseLevel(it.Threshold)
	return guardrails.Rule{
		ID: id, DetectorID: detectorID, Category: category, Stage: guardrails.Stage(stage),
		Threshold: threshold, OnMatch: guardrails.Action(it.OnMatch), OnError: guardrails.Action(it.OnError), Timeout: timeout,
	}
}
