// 本文件按固定顺序校验定价配置并装配启动同步，失败策略与方案 §9 的失败策略澄清一致。
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/darkBaryon/bifrost/ee/internal/pricing/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
)

const (
	// envPricingUSDCNY 为每 1 美元兑换的人民币数；未设置用文件汇率，空值或非有限正数拒启。
	envPricingUSDCNY = "EE_PRICING_USD_CNY"
	// envPricingVendorMap 为部署厂商名到官网厂商 ID 的对应表；未设置或空值时只按接入主机识别。
	envPricingVendorMap = "EE_PRICING_VENDOR_MAP"
	// envPricingFile 为替换价格 JSON 文件的路径；未设置或空值时使用随二进制嵌入的价格文件。
	envPricingFile = "EE_PRICING_FILE"
)

func pricingRate() (*float64, error) {
	raw, ok := os.LookupEnv(envPricingUSDCNY)
	if !ok {
		return nil, nil
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || !pricing.ValidRate(rate) {
		return nil, fmt.Errorf("%w: %s must be a finite positive number", pricing.ErrConfig, envPricingUSDCNY)
	}
	return &rate, nil
}

func assemblePricing(ctx context.Context, config configstore.ConfigStore, catalog *modelcatalog.ModelCatalog, log schemas.Logger) error {
	rate, err := pricingRate()
	if err != nil {
		return err
	}
	file, err := pricing.Load(os.Getenv(envPricingFile))
	if err != nil {
		log.Error("pricing: load failed; synchronization skipped: %v", err)
		return nil
	}
	mapping, err := pricing.ParseVendorMap(os.Getenv(envPricingVendorMap), file)
	if err != nil {
		return fmt.Errorf("%s: %w", envPricingVendorMap, err)
	}
	if catalog == nil {
		log.Warn("pricing: model catalog unavailable; synchronization skipped")
		return nil
	}
	service, err := pricing.New(file, pricing.Options{
		USDToCNY:  rate,
		VendorMap: mapping,
	}, pricing.Deps{
		Overrides: persistence.NewOverrides(config),
		Catalog:   persistence.NewCatalog(catalog),
		Providers: persistence.NewProviders(config),
		Log:       log,
	})
	if err != nil {
		return err
	}
	if _, err = service.Sync(ctx); err != nil {
		if errors.Is(err, pricing.ErrConfig) {
			return err
		}
		log.Error("pricing: synchronization failed: %v", err)
	}
	return nil
}
