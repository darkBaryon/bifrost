// 本文件走业务生成、适配转换和空基础价目表费用计算的完整链路。
package persistence

import (
	"context"
	"math"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
)

type memoryRows struct{ rows []pricing.OverrideRow }

func (m *memoryRows) List(context.Context) ([]pricing.OverrideRow, error) { return m.rows, nil }
func (m *memoryRows) Create(_ context.Context, r pricing.OverrideRow) error {
	m.rows = append(m.rows, r)
	return nil
}
func (m *memoryRows) Update(context.Context, pricing.OverrideRow) error { return nil }
func (m *memoryRows) Delete(context.Context, string) error              { return nil }

type oneProvider struct{}

func (oneProvider) List(context.Context) ([]pricing.Provider, error) {
	return []pricing.Provider{{Name: "MyCustomProvider", Custom: true, BaseURL: "test.example"}}, nil
}

type captureCatalog struct{}

func (captureCatalog) Upsert(...pricing.OverrideRow) error { return nil }
func (captureCatalog) Delete(string)                       {}

type quietLogger struct{}

func (quietLogger) Info(string, ...interface{}) {}
func (quietLogger) Warn(string, ...interface{}) {}

func TestCostWithOnlyOverride(t *testing.T) {
	cache := 1.2
	file := pricing.PriceFile{
		PricingRule: pricing.LowestTierStandardRate,
		Rates:       map[pricing.Currency]float64{pricing.USD: 1, pricing.CNY: 7.2},
		Vendors: []pricing.Vendor{
			{
				ID:            "test",
				EndpointHosts: []string{"test.example"},
				Models: []pricing.ModelPrice{
					{
						Model:              "unlisted-model",
						Currency:           pricing.CNY,
						InputCost:          6,
						OutputCost:         24,
						CacheReadInputCost: &cache,
					},
				},
			},
		},
	}
	rows := &memoryRows{}
	service, e := pricing.New(file, pricing.Options{}, pricing.Deps{
		Overrides: rows,
		Catalog:   captureCatalog{},
		Providers: oneProvider{},
		Log:       quietLogger{},
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.Sync(context.Background()); e != nil {
		t.Fatal(e)
	}
	ds := datasheet.NewTestStore(nil)
	if e = ds.UpsertOverrides(toTable(rows.rows[0])); e != nil {
		t.Fatal(e)
	}
	for _, request := range []schemas.RequestType{schemas.ChatCompletionRequest, schemas.ResponsesRequest, schemas.TextCompletionRequest, schemas.ChatCompletionStreamRequest} {
		usage := &schemas.BifrostLLMUsage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}
		cost := ds.CalculateCostForUsage(usage, "MyCustomProvider", "unlisted-model", request, &datasheet.LookupScopes{Provider: "MyCustomProvider"})
		want := (1000.0*6 + 500.0*24) / 1e6 / 7.2
		if math.Abs(cost-want) > 1e-12 {
			t.Fatalf("%s cost=%g want=%g", request, cost, want)
		}
	}
	usage := &schemas.BifrostLLMUsage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500, PromptTokensDetails: &schemas.ChatPromptTokensDetails{CachedReadTokens: 200}}
	cost := ds.CalculateCostForUsage(usage, "MyCustomProvider", "unlisted-model", schemas.ChatCompletionRequest, &datasheet.LookupScopes{Provider: "MyCustomProvider"})
	want := (800.0*6 + 200.0*1.2 + 500.0*24) / 1e6 / 7.2
	if math.Abs(cost-want) > 1e-12 {
		t.Fatalf("cached cost=%g want=%g", cost, want)
	}
	if got := ds.CalculateCostForUsage(usage, "MyCustomProvider", "not-in-file", schemas.ChatCompletionRequest, &datasheet.LookupScopes{Provider: "MyCustomProvider"}); got != 0 {
		t.Fatal(got)
	}
}
