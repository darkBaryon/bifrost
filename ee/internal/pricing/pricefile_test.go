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
