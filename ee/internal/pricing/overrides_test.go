// 本文件验证币种换算、覆盖身份与可选缓存价契约。
package pricing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestConversion(t *testing.T) {
	for _, tt := range []struct {
		name       string
		amount     float64
		currency   Currency
		rate, want float64
		fail       bool
	}{
		{"CNY", 6, CNY, 7.2, 8.333333333333334e-7, false},
		{"USD", 6, USD, 7.2, 6e-6, false}, {"free", 0, CNY, 7.2, 0, false},
		{"zero rate", 6, CNY, 0, 0, true}, {"negative", -1, USD, 1, 0, true},
		{"NaN", math.NaN(), USD, 1, 0, true}, {"inf", 1, CNY, math.Inf(1), 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, e := toUSDPerToken(tt.amount, tt.currency, map[Currency]float64{CNY: tt.rate})
			if (e != nil) != tt.fail {
				t.Fatal(e)
			}
			if !tt.fail && math.Abs(v-tt.want) > 1e-20 {
				t.Fatalf("got %g want %g", v, tt.want)
			}
		})
	}
}

func TestOverride(t *testing.T) {
	f := testFile(t)
	v := f.Vendors[0]
	m := v.Models[0]
	a, e := makeOverride("OriginalName", v, m, f.Rates)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := makeOverride("OriginalName", v, m, f.Rates)
	other, _ := makeOverride("other", v, m, f.Rates)
	if !reflect.DeepEqual(a, b) || a.ID == other.ID || a.ProviderID != "OriginalName" || a.ScopeKind != ProviderScope || a.MatchType != ExactMatch || a.Pattern != m.Model || a.ConfigHash != "" {
		t.Fatalf("invalid row %+v", a)
	}
	if !reflect.DeepEqual(a.RequestTypes, []RequestType{ChatCompletion, Responses, TextCompletion}) {
		t.Fatal(a.RequestTypes)
	}
	var patch map[string]float64
	if e = json.Unmarshal([]byte(a.PricingPatchJSON), &patch); e != nil {
		t.Fatal(e)
	}
	if _, ok := patch["cache_read_input_token_cost"]; ok {
		t.Fatal("absent cache price serialized")
	}
	zero := 0.0
	m.CacheReadInputCost = &zero
	a, e = makeOverride("OriginalName", v, m, f.Rates)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal([]byte(a.PricingPatchJSON), &patch); e != nil {
		t.Fatal(e)
	}
	if val, ok := patch["cache_read_input_token_cost"]; !ok || val != 0 {
		t.Fatal(patch)
	}
}
