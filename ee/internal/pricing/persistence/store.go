// Package persistence 将国内定价的窄接口适配到上游配置存储和模型目录，不持有 HTTP Server。
package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog"
)

// Overrides 适配覆盖 CRUD；共享配置存储的生命周期由宿主管理。
type Overrides struct {
	config configstore.ConfigStore
}

// NewOverrides 连接已装配的配置存储，不建表、不迁移、不启动后台任务。
func NewOverrides(config configstore.ConfigStore) *Overrides {
	return &Overrides{config: config}
}

// List 读取全表，所有权由业务服务判定。
func (s *Overrides) List(ctx context.Context) ([]pricing.OverrideRow, error) {
	rows, err := s.config.GetPricingOverrides(ctx, configstore.PricingOverrideFilters{})
	if err != nil {
		return nil, err
	}
	result := make([]pricing.OverrideRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, fromTable(row))
	}
	return result, nil
}

// Create 将唯一约束冲突映射为 ErrRowExists。
func (s *Overrides) Create(ctx context.Context, row pricing.OverrideRow) error {
	return mapError(s.config.CreatePricingOverride(ctx, toTable(row)))
}

// Update 复用上游 Save，可在行被另一节点删除后重新创建。
func (s *Overrides) Update(ctx context.Context, row pricing.OverrideRow) error {
	return mapError(s.config.UpdatePricingOverride(ctx, toTable(row)))
}

// Delete 将已不存在映射为 ErrRowMissing，由服务按幂等成功处理。
func (s *Overrides) Delete(ctx context.Context, id string) error {
	return mapError(s.config.DeletePricingOverride(ctx, id))
}

// Catalog 适配本节点内存目录，生命周期由宿主管理。
type Catalog struct{ catalog *modelcatalog.ModelCatalog }

// NewCatalog 连接已装配的目录；表与目录的更新顺序由 pricing.Service 协调。
func NewCatalog(catalog *modelcatalog.ModelCatalog) *Catalog {
	return &Catalog{catalog: catalog}
}

// Upsert 增量写入期望覆盖，不替换管理员的其他覆盖。
func (c *Catalog) Upsert(rows ...pricing.OverrideRow) error {
	converted := make([]*tables.TablePricingOverride, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, toTable(row))
	}
	return c.catalog.UpsertPricingOverrides(converted...)
}

// Delete 移除指定覆盖的内存条目。
func (c *Catalog) Delete(id string) { c.catalog.DeletePricingOverride(id) }

// Providers 适配部署厂商读取，生命周期由宿主管理。
type Providers struct{ config configstore.ConfigStore }

// NewProviders 连接已装配的配置存储，不加载或修改厂商。
func NewProviders(config configstore.ConfigStore) *Providers {
	return &Providers{config: config}
}

// List 读取部署厂商，nil NetworkConfig 转为空 BaseURL。
func (p *Providers) List(ctx context.Context) ([]pricing.Provider, error) {
	configs, err := p.config.GetProvidersConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]pricing.Provider, 0, len(configs))
	for name, config := range configs {
		provider := pricing.Provider{
			Name:   string(name),
			Custom: config.CustomProviderConfig != nil,
		}
		if config.NetworkConfig != nil {
			provider.BaseURL = config.NetworkConfig.BaseURL
		}
		result = append(result, provider)
	}
	return result, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, configstore.ErrAlreadyExists):
		return fmt.Errorf("%w: %v", pricing.ErrRowExists, err)
	case errors.Is(err, configstore.ErrNotFound):
		return fmt.Errorf("%w: %v", pricing.ErrRowMissing, err)
	default:
		return err
	}
}
func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func pointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func fromTable(row tables.TablePricingOverride) pricing.OverrideRow {
	requests := make([]pricing.RequestType, len(row.RequestTypes))
	for i, r := range row.RequestTypes {
		requests[i] = pricing.RequestType(r)
	}
	return pricing.OverrideRow{
		ID:               row.ID,
		Name:             row.Name,
		ScopeKind:        pricing.ScopeKind(row.ScopeKind),
		ProviderID:       value(row.ProviderID),
		ProviderKeyID:    value(row.ProviderKeyID),
		VirtualKeyID:     value(row.VirtualKeyID),
		UserID:           value(row.UserID),
		MatchType:        pricing.MatchType(row.MatchType),
		Pattern:          row.Pattern,
		RequestTypes:     requests,
		PricingPatchJSON: row.PricingPatchJSON,
		ConfigHash:       row.ConfigHash,
	}
}
func toTable(row pricing.OverrideRow) *tables.TablePricingOverride {
	requests := make([]schemas.RequestType, len(row.RequestTypes))
	for i, r := range row.RequestTypes {
		requests[i] = schemas.RequestType(r)
	}
	return &tables.TablePricingOverride{
		ID:               row.ID,
		Name:             row.Name,
		ScopeKind:        string(row.ScopeKind),
		ProviderID:       pointer(row.ProviderID),
		ProviderKeyID:    pointer(row.ProviderKeyID),
		VirtualKeyID:     pointer(row.VirtualKeyID),
		UserID:           pointer(row.UserID),
		MatchType:        string(row.MatchType),
		Pattern:          row.Pattern,
		RequestTypes:     requests,
		PricingPatchJSON: row.PricingPatchJSON,
		ConfigHash:       row.ConfigHash,
	}
}
