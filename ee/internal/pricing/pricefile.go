// 本文件把外部 JSON 与嵌入数据转换为经过完整校验的业务价格目录。
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

//go:embed data/cn-prices.json
var embeddedPrices []byte

const priceUnit = "per_million_tokens"

// 文件 DTO 与业务类型分离，缺失必填单价不能悄悄成为免费。
type priceFileJSON struct {
	Version     string      `json:"version"`
	PricingRule PricingRule `json:"pricing_rule"`
	Rates       struct {
		CNY       *float64 `json:"CNY"`
		CheckedAt string   `json:"checked_at"`
	} `json:"rates"`
	Vendors []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		EndpointHosts []string `json:"endpoint_hosts"`
		PricePage     string   `json:"price_page"`
		Models        []struct {
			Model              string   `json:"model"`
			Currency           Currency `json:"currency"`
			Unit               string   `json:"unit"`
			InputCost          *float64 `json:"input_cost"`
			OutputCost         *float64 `json:"output_cost"`
			CacheReadInputCost *float64 `json:"cache_read_input_cost"`
			CheckedAt          string   `json:"checked_at"`
			Note               string   `json:"note"`
		} `json:"models"`
	} `json:"vendors"`
}

// Load 读取外部文件；path 为空时使用随二进制嵌入的版本。失败不返回部分目录。
func Load(path string) (PriceFile, error) {
	data := embeddedPrices
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return PriceFile{}, fmt.Errorf("read price file: %w", err)
		}
	}
	return parsePriceFile(data)
}

func parsePriceFile(data []byte) (PriceFile, error) {
	var raw priceFileJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return PriceFile{}, fmt.Errorf("parse price file: %w", err)
	}
	file := PriceFile{Version: raw.Version, PricingRule: raw.PricingRule, Rates: map[Currency]float64{USD: 1}, RatesCheckedAt: raw.Rates.CheckedAt}
	if raw.Rates.CNY != nil {
		file.Rates[CNY] = *raw.Rates.CNY
	}
	for _, v := range raw.Vendors {
		vendor := Vendor{ID: v.ID, Name: v.Name, EndpointHosts: v.EndpointHosts, PricePage: v.PricePage}
		for _, m := range v.Models {
			if m.Unit != priceUnit {
				return PriceFile{}, fmt.Errorf("%s/%s: unit must be %s", v.ID, m.Model, priceUnit)
			}
			if m.InputCost == nil || m.OutputCost == nil {
				return PriceFile{}, fmt.Errorf("%s/%s: input_cost and output_cost are required", v.ID, m.Model)
			}
			vendor.Models = append(vendor.Models, ModelPrice{Model: m.Model, Currency: m.Currency, InputCost: *m.InputCost, OutputCost: *m.OutputCost, CacheReadInputCost: m.CacheReadInputCost, CheckedAt: m.CheckedAt, Note: m.Note})
		}
		file.Vendors = append(file.Vendors, vendor)
	}
	if err := file.validate(); err != nil {
		return PriceFile{}, err
	}
	return file, nil
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func (f PriceFile) validate() error {
	if f.PricingRule != LowestTierStandardRate && f.PricingRule != HighestTierPeakRate {
		return fmt.Errorf("invalid pricing_rule %q", f.PricingRule)
	}
	for currency, rate := range f.Rates {
		if !finiteNonnegative(rate) || rate == 0 {
			return fmt.Errorf("invalid rate for %s", currency)
		}
	}
	ids := map[string]bool{}
	for _, v := range f.Vendors {
		if strings.TrimSpace(v.ID) == "" || ids[v.ID] {
			return fmt.Errorf("empty or duplicate vendor id %q", v.ID)
		}
		ids[v.ID] = true
		if len(v.EndpointHosts) == 0 {
			return fmt.Errorf("%s: endpoint_hosts must not be empty", v.ID)
		}
		for _, host := range v.EndpointHosts {
			if strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/: @") {
				return fmt.Errorf("%s: invalid endpoint host %q", v.ID, host)
			}
		}
		models := map[string]bool{}
		for _, m := range v.Models {
			if strings.TrimSpace(m.Model) == "" || models[m.Model] {
				return fmt.Errorf("%s: empty or duplicate model %q", v.ID, m.Model)
			}
			models[m.Model] = true
			if m.Currency != CNY && m.Currency != USD {
				return fmt.Errorf("%s/%s: unknown currency %q", v.ID, m.Model, m.Currency)
			}
			if _, ok := f.Rates[m.Currency]; !ok {
				return fmt.Errorf("%s: missing rate for %s", v.ID, m.Currency)
			}
			if !finiteNonnegative(m.InputCost) || !finiteNonnegative(m.OutputCost) || (m.CacheReadInputCost != nil && !finiteNonnegative(*m.CacheReadInputCost)) {
				return fmt.Errorf("%s/%s: prices must be finite and nonnegative", v.ID, m.Model)
			}
		}
	}
	return nil
}
