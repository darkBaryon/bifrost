// 本文件验证价格文件契约，避免漏字段、非法金额或重复模型静默导入。
package pricing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validPrices = `{"version":"2026-09-13","pricing_rule":"lowest-tier-standard-rate","rates":{"CNY":7.2,"checked_at":"2026-09-13"},"vendors":[{"id":"dashscope","name":"阿里百炼","endpoint_hosts":["dashscope.aliyuncs.com"],"price_page":"https://example.com/prices","models":[{"model":"qwen-test","currency":"CNY","unit":"per_million_tokens","input_cost":6,"output_cost":24,"checked_at":"2026-09-13","note":"最低档"}]}]}`

func TestPriceFile(t *testing.T) {
	tests := []struct{ name, old, replacement, want string }{
		{name: "valid"},
		{"hosts", `["dashscope.aliyuncs.com"]`, `[]`, "endpoint_hosts"},
		{"duplicate model", `"note":"最低档"}`, `"note":"最低档"},{"model":"qwen-test","currency":"CNY","unit":"per_million_tokens","input_cost":1,"output_cost":2}`, "duplicate model"},
		{"negative", `"input_cost":6`, `"input_cost":-1`, "nonnegative"},
		{"currency", `"currency":"CNY"`, `"currency":"EUR"`, "unknown currency"},
		{"rate missing", `"CNY":7.2,`, ``, "missing rate"},
		{"rule", `lowest-tier-standard-rate`, `invalid`, "pricing_rule"},
		{"rate zero", `"CNY":7.2`, `"CNY":0`, "invalid rate"},
		{"rate negative", `"CNY":7.2`, `"CNY":-1`, "invalid rate"},
		{"missing price", `"input_cost":6,`, ``, "required"},
		{"unit", `per_million_tokens`, `per_token`, "unit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := validPrices
			if tt.old != "" {
				data = strings.Replace(data, tt.old, tt.replacement, 1)
			}
			f, err := parsePriceFile([]byte(data))
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("want %q, got %v", tt.want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.Vendors[0].Models[0].InputCost != 6 || f.Rates[USD] != 1 {
				t.Fatalf("unexpected file: %+v", f)
			}
		})
	}
}

func TestDuplicateVendors(t *testing.T) {
	f, err := parsePriceFile([]byte(validPrices))
	if err != nil {
		t.Fatal(err)
	}
	f.Vendors = append(f.Vendors, f.Vendors[0])
	if err = f.validate(); err == nil || !strings.Contains(err.Error(), "duplicate vendor") {
		t.Fatal(err)
	}
}

func TestLoadExternalAndMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if _, err := Load(path); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := os.WriteFile(path, []byte(validPrices), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePriceFile([]byte(`{"pricing_rule":"lowest-tier-standard-rate","vendors":[]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedPriceFile(t *testing.T) {
	f, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Vendors) != 4 || f.PricingRule != LowestTierStandardRate {
		t.Fatalf("unexpected embedded catalog: %+v", f)
	}
	for _, vendor := range f.Vendors {
		if vendor.PricePage == "" {
			t.Fatalf("missing source: %s", vendor.ID)
		}
		for _, model := range vendor.Models {
			if model.CheckedAt == "" || model.Note == "" || strings.Contains(model.Model, "preview") {
				t.Fatalf("missing audit fields or preview model: %+v", model)
			}
		}
	}
	// 独立核对 A1 的关键维度，防止把思考价、优惠价或控制台缓存例外导入。
	floatPointer := func(v float64) *float64 { return &v }
	checks := map[string]struct {
		input, output float64
		cache         *float64
	}{
		"qwen-plus":      {0.8, 2, floatPointer(0.16)},
		"qwen3.8-max":    {12, 36, nil},
		"glm-4.7":        {2, 8, floatPointer(0.4)},
		"deepseek-flash": {2, 8, floatPointer(0.04)},
	}
	for _, vendor := range f.Vendors {
		for _, model := range vendor.Models {
			want, ok := checks[model.Model]
			if !ok {
				continue
			}
			if model.InputCost != want.input || model.OutputCost != want.output || (model.CacheReadInputCost == nil) != (want.cache == nil) {
				t.Fatalf("A1 price mismatch: %+v", model)
			}
			if want.cache != nil && *model.CacheReadInputCost != *want.cache {
				t.Fatalf("cache price mismatch: %+v", model)
			}
			delete(checks, model.Model)
		}
	}
	if len(checks) != 0 {
		t.Fatalf("missing checked models: %v", checks)
	}
}
