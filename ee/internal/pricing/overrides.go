// 本文件将官网单价转换为美元/token 的精确匹配覆盖，稳定 ID 也用于所有权核验。
package pricing

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const (
	namePrefix = "ee-pricing: "
	// namespaceID 发布后不可改变，否则重启会产生另一组覆盖 ID。
	namespaceID      = "00f4c87d-3562-53ab-a7c1-d03f9673ceaa"
	tokensPerMillion = 1_000_000
)

// ScopeKind 与 MatchType 是持久化覆盖契约的枚举。
type ScopeKind string
type MatchType string

// RequestType 只允许非流式请求名；上游负责流式请求归一化。
type RequestType string

// 本功能使用的作用域、匹配方式及请求类型。
const (
	ProviderScope  ScopeKind   = "provider"
	ExactMatch     MatchType   = "exact"
	ChatCompletion RequestType = "chat_completion"
	Responses      RequestType = "responses"
	TextCompletion RequestType = "text_completion"
)

// OverrideRow 是存储与目录共用的业务快照，不含上游类型或持久化标签。
// 非本功能行的其他作用域字段保留，以便比较时不会忽略管理员修改。
type OverrideRow struct {
	ID, Name                                string
	ScopeKind                               ScopeKind
	ProviderID, ProviderKeyID, VirtualKeyID string
	MatchType                               MatchType
	Pattern                                 string
	RequestTypes                            []RequestType
	PricingPatchJSON, ConfigHash            string
}

func overrideID(provider, model string) string {
	return uuid.NewSHA1(uuid.MustParse(namespaceID), []byte(provider+"/"+model)).String()
}

func toUSDPerToken(amount float64, currency Currency, rates map[Currency]float64) (float64, error) {
	rate := 1.0
	switch currency {
	case USD:
	case CNY:
		rate = rates[CNY]
	default:
		return 0, fmt.Errorf("unknown currency %q", currency)
	}
	if !finiteNonnegative(amount) || !finiteNonnegative(rate) || rate == 0 {
		return 0, fmt.Errorf("invalid amount or rate for %s", currency)
	}
	value := amount / tokensPerMillion / rate
	if !finiteNonnegative(value) {
		return 0, fmt.Errorf("converted price overflows for %s", currency)
	}
	return value, nil
}

func makeOverride(provider string, vendor Vendor, model ModelPrice, rates map[Currency]float64) (OverrideRow, error) {
	patch := map[string]float64{}
	amounts := map[string]float64{"input_cost_per_token": model.InputCost, "output_cost_per_token": model.OutputCost}
	if model.CacheReadInputCost != nil {
		amounts["cache_read_input_token_cost"] = *model.CacheReadInputCost
	}
	for key, amount := range amounts {
		value, err := toUSDPerToken(amount, model.Currency, rates)
		if err != nil {
			return OverrideRow{}, err
		}
		patch[key] = value
	}
	// 标准库 JSON 对 map 键排序，保证重启比较的字节稳定；业务根包不导入上游工具。
	data, err := json.Marshal(patch)
	if err != nil {
		return OverrideRow{}, err
	}
	return OverrideRow{ID: overrideID(provider, model.Model), Name: namePrefix + vendor.ID + "/" + model.Model, ScopeKind: ProviderScope, ProviderID: provider, MatchType: ExactMatch, Pattern: model.Model, RequestTypes: []RequestType{ChatCompletion, Responses, TextCompletion}, PricingPatchJSON: string(data)}, nil
}
