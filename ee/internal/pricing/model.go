// Package pricing 管理国内官方模型价格与启动期覆盖同步，不依赖宿主或存储类型。
package pricing

import "errors"

// ErrConfig 标识必须拒绝启动的部署配置错误，与存储故障和价格文件无效区分。
var ErrConfig = errors.New("invalid pricing configuration")

// Currency 是官网价格使用的币种。
type Currency string

// 支持的币种；汇率表示一美元对应多少该币种。
const (
	CNY Currency = "CNY"
	USD Currency = "USD"
)

// PricingRule 记录单价不能表达阶梯时采用的取价口径。
type PricingRule string

// 两种口径均不取时段或限时折扣。
const (
	LowestTierStandardRate PricingRule = "lowest-tier-standard-rate"
	HighestTierPeakRate    PricingRule = "highest-tier-peak-rate"
)

// PriceFile 是校验后的价格目录，空厂商列表用于清理本功能覆盖。
type PriceFile struct {
	Version        string
	PricingRule    PricingRule
	Rates          map[Currency]float64
	RatesCheckedAt string
	Vendors        []Vendor
}

// Vendor 描述一家官网厂商及其对话模型。
type Vendor struct {
	ID, Name, PricePage string
	EndpointHosts       []string
	Models              []ModelPrice
}

// ModelPrice 的价格单位为每百万 token；nil 缓存价表示官网未提供。
type ModelPrice struct {
	Model                 string
	Currency              Currency
	InputCost, OutputCost float64
	CacheReadInputCost    *float64
	CheckedAt, Note       string
}
