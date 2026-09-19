// 本文件把已校验的配置构建成检查器：校验判官 provider、创建密钥与判官检测器、装配规则。
package host

import (
	"context"
	"fmt"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/judge"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/secrets"
	"github.com/maximhq/bifrost/core/schemas"
)

// ProviderLookup 查询网关是否配置了某个 provider；由 configstore.ConfigStore 满足。
type ProviderLookup interface {
	GetProviderConfig(ctx context.Context, provider schemas.ModelProvider) (*schemas.ProviderConfig, error)
}

// Builder 持有构建检查器所需的网关依赖；可重复用于每次配置更新。
type Builder struct {
	client    Client
	providers ProviderLookup
	log       Logger
}

// NewBuilder 显式接收依赖；任一为 nil 时返回错误。
func NewBuilder(client Client, providers ProviderLookup, log Logger) (*Builder, error) {
	if client == nil || providers == nil || log == nil {
		return nil, fmt.Errorf("guardrails host: client, provider lookup and logger are required")
	}
	return &Builder{client: client, providers: providers, log: log}, nil
}

// Build 按配置创建检测器与规则并返回检查器；配置须已通过 config.Validate。
// 判官 provider 必须已在网关配置，否则返回错误；provider 自带重试时记 Warn（实际调用次数会与检测器重试相乘）。
func (b *Builder) Build(ctx context.Context, cfg config.Config) (*guardrails.Checker, error) {
	plan := cfg.Expand()
	detectors := make(map[string]guardrails.Detector, len(plan.Judges)+1)
	if plan.NeedsSecrets {
		d, err := secrets.New(secrets.Options{IgnoredKeywords: cfg.Secrets.IgnoredKeywords})
		if err != nil {
			return nil, err
		}
		detectors[config.SecretsDetectorID] = d
	}
	if len(plan.Judges) > 0 {
		if err := b.checkProvider(ctx, cfg.Judge); err != nil {
			return nil, err
		}
		model := NewJudgeModel(b.client, cfg.Judge.Provider, cfg.Judge.Model, b.log)
		for _, spec := range plan.Judges {
			d, err := judge.New(judge.Options{
				Model: model, Kind: judgeKind(spec.Item), Rule: spec.RuleText, Stage: spec.Stage,
				Retries: cfg.Judge.Retries, MaxBytes: cfg.Judge.MaxTextBytes,
			})
			if err != nil {
				return nil, fmt.Errorf("guardrails host: %s: %w", spec.DetectorID, err)
			}
			detectors[spec.DetectorID] = d
		}
	}
	return guardrails.New(plan.Rules, detectors, cfg.MaxTextBytes)
}

func (b *Builder) checkProvider(ctx context.Context, j config.Judge) error {
	pc, err := b.providers.GetProviderConfig(ctx, schemas.ModelProvider(j.Provider))
	if err != nil || pc == nil {
		return fmt.Errorf("guardrails host: judge provider %q is not configured", j.Provider)
	}
	if pc.NetworkConfig.MaxRetries > 0 {
		b.log.Warn("content safety judge provider %s has max_retries=%d; judge calls multiply with detector retries", j.Provider, pc.NetworkConfig.MaxRetries)
	}
	return nil
}

func judgeKind(item string) judge.Kind {
	switch item {
	case config.ItemHarmful:
		return judge.Harmful
	case config.ItemPromptAttack:
		return judge.PromptAttack
	default:
		return judge.BusinessRule
	}
}
