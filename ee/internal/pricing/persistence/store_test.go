// 本文件验证真实适配的错误映射、行字段往返与空厂商网络配置。
package persistence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
)

type providerConfigStub struct {
	configstore.ConfigStore
	configs map[schemas.ModelProvider]configstore.ProviderConfig
}

func (s providerConfigStub) GetProvidersConfig(context.Context) (map[schemas.ModelProvider]configstore.ProviderConfig, error) {
	return s.configs, nil
}

func TestStoreMapping(t *testing.T) {
	for _, tt := range []struct{ from, to error }{{configstore.ErrAlreadyExists, pricing.ErrRowExists}, {configstore.ErrNotFound, pricing.ErrRowMissing}} {
		if !errors.Is(mapError(fmt.Errorf("wrapped: %w", tt.from)), tt.to) {
			t.Fatal(tt)
		}
	}
	other := errors.New("storage failure")
	if mapError(other) != other || mapError(nil) != nil {
		t.Fatal("unrelated error changed")
	}
	row := pricing.OverrideRow{ID: "id", Name: "name", ScopeKind: pricing.ProviderScope, ProviderID: "OriginalName", ProviderKeyID: "key", VirtualKeyID: "vk", UserID: "user", MatchType: pricing.ExactMatch, Pattern: "model", RequestTypes: []pricing.RequestType{pricing.ChatCompletion}, PricingPatchJSON: `{"input_cost_per_token":0}`, ConfigHash: "hash"}
	if got := fromTable(*toTable(row)); !reflect.DeepEqual(got, row) {
		t.Fatalf("%+v", got)
	}
	for _, configs := range []map[schemas.ModelProvider]configstore.ProviderConfig{nil, {"custom": {CustomProviderConfig: &schemas.CustomProviderConfig{}}}} {
		rows, e := (providerAdapter{providerConfigStub{configs: configs}}).List(context.Background())
		if e != nil || len(rows) != len(configs) {
			t.Fatal(rows, e)
		}
		if len(rows) > 0 && (!rows[0].Custom || rows[0].BaseURL != "" || rows[0].Name != "custom") {
			t.Fatal(rows)
		}
	}
}

func TestCatalogAdapter(t *testing.T) {
	ds := datasheet.NewTestStore(nil)
	adapter := NewStore(nil, modelcatalog.NewTestCatalogWithDatasheet(ds)).Catalog()
	row := pricing.OverrideRow{ID: "adapter", Name: "price", ScopeKind: pricing.ProviderScope, ProviderID: "custom", MatchType: pricing.ExactMatch, Pattern: "model", RequestTypes: []pricing.RequestType{pricing.ChatCompletion}, PricingPatchJSON: `{"input_cost_per_token":0.01,"output_cost_per_token":0.02}`}
	if e := adapter.Upsert(row); e != nil {
		t.Fatal(e)
	}
	usage := &schemas.BifrostLLMUsage{PromptTokens: 10, CompletionTokens: 5}
	cost := func() float64 {
		return ds.CalculateCostForUsage(usage, "custom", "model", schemas.ChatCompletionRequest, &datasheet.LookupScopes{Provider: "custom"})
	}
	if cost() != 0.2 {
		t.Fatal(cost())
	}
	adapter.Delete(row.ID)
	if cost() != 0 {
		t.Fatal(cost())
	}
}
