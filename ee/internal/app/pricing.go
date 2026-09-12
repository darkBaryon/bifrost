// 本文件按固定顺序校验定价配置并装配启动同步，失败策略与方案 §9 的失败策略澄清一致。
package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/darkBaryon/bifrost/ee/internal/pricing/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
)

const (
	envPricingUSDCNY    = "EE_PRICING_USD_CNY"
	envPricingVendorMap = "EE_PRICING_VENDOR_MAP"
	envPricingFile      = "EE_PRICING_FILE"
)

func pricingRate() (*float64, error) {
	raw, ok := os.LookupEnv(envPricingUSDCNY)
	if !ok {
		return nil, nil
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
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
	store := persistence.NewStore(config, catalog)
	service, err := pricing.New(file, pricing.Options{USDToCNY: rate, VendorMap: mapping}, pricing.Deps{Overrides: store, Catalog: store.Catalog(), Providers: store.Providers(), Log: log})
	if err != nil {
		if errors.Is(err, pricing.ErrConfig) {
			return err
		}
		log.Error("pricing: initialization failed; synchronization skipped: %v", err)
		return nil
	}
	if _, err = service.Sync(ctx); err != nil {
		if errors.Is(err, pricing.ErrConfig) {
			return err
		}
		log.Error("pricing: synchronization failed: %v", err)
	}
	return nil
}
