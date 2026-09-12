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

// Store 适配覆盖 CRUD；共享存储和目录的生命周期由宿主管理。
type Store struct {
	config  configstore.ConfigStore
	catalog *modelcatalog.ModelCatalog
}

// NewStore 只连接已经装配好的依赖，不建表、不迁移、不启动后台任务。
func NewStore(config configstore.ConfigStore, catalog *modelcatalog.ModelCatalog) *Store {
	return &Store{config, catalog}
}

// List 读取全表，所有权由业务服务判定。
func (s *Store) List(ctx context.Context) ([]pricing.OverrideRow, error) {
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
func (s *Store) Create(ctx context.Context, row pricing.OverrideRow) error {
	return mapError(s.config.CreatePricingOverride(ctx, toTable(row)))
}

// Update 复用上游 Save，可在行被另一节点删除后重新创建。
func (s *Store) Update(ctx context.Context, row pricing.OverrideRow) error {
	return mapError(s.config.UpdatePricingOverride(ctx, toTable(row)))
}

// Delete 将已不存在映射为 ErrRowMissing，由服务按幂等成功处理。
func (s *Store) Delete(ctx context.Context, id string) error {
	return mapError(s.config.DeletePricingOverride(ctx, id))
}

// Catalog 返回目录适配，避免与存储 Delete 的方法签名混淆。
func (s *Store) Catalog() catalogAdapter { return catalogAdapter{s.catalog} }

// Providers 返回厂商读取适配，nil NetworkConfig 转为空 BaseURL。
func (s *Store) Providers() providerAdapter { return providerAdapter{s.config} }

type catalogAdapter struct{ catalog *modelcatalog.ModelCatalog }

func (c catalogAdapter) Upsert(rows ...pricing.OverrideRow) error {
	converted := make([]*tables.TablePricingOverride, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, toTable(row))
	}
	return c.catalog.UpsertPricingOverrides(converted...)
}
func (c catalogAdapter) Delete(id string) { c.catalog.DeletePricingOverride(id) }

type providerAdapter struct{ config configstore.ConfigStore }

func (p providerAdapter) List(ctx context.Context) ([]pricing.Provider, error) {
	configs, err := p.config.GetProvidersConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]pricing.Provider, 0, len(configs))
	for name, config := range configs {
		provider := pricing.Provider{Name: string(name), Custom: config.CustomProviderConfig != nil}
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
	return pricing.OverrideRow{ID: row.ID, Name: row.Name, ScopeKind: pricing.ScopeKind(row.ScopeKind), ProviderID: value(row.ProviderID), ProviderKeyID: value(row.ProviderKeyID), VirtualKeyID: value(row.VirtualKeyID), UserID: value(row.UserID), MatchType: pricing.MatchType(row.MatchType), Pattern: row.Pattern, RequestTypes: requests, PricingPatchJSON: row.PricingPatchJSON, ConfigHash: row.ConfigHash}
}
func toTable(row pricing.OverrideRow) *tables.TablePricingOverride {
	requests := make([]schemas.RequestType, len(row.RequestTypes))
	for i, r := range row.RequestTypes {
		requests[i] = schemas.RequestType(r)
	}
	return &tables.TablePricingOverride{ID: row.ID, Name: row.Name, ScopeKind: string(row.ScopeKind), ProviderID: pointer(row.ProviderID), ProviderKeyID: pointer(row.ProviderKeyID), VirtualKeyID: pointer(row.VirtualKeyID), UserID: pointer(row.UserID), MatchType: string(row.MatchType), Pattern: row.Pattern, RequestTypes: requests, PricingPatchJSON: row.PricingPatchJSON, ConfigHash: row.ConfigHash}
}
