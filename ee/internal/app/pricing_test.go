// 本文件验证汇率独立校验及无效文件跳过手工映射的启动失败顺序。
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/pricing"
	"github.com/fasthttp/router"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type pricingLogger struct {
	schemas.Logger
	errors, warns []string
}

func (l *pricingLogger) Error(f string, a ...any) { l.errors = append(l.errors, fmt.Sprintf(f, a...)) }
func (l *pricingLogger) Warn(f string, a ...any)  { l.warns = append(l.warns, fmt.Sprintf(f, a...)) }
func (l *pricingLogger) Info(string, ...any)      {}

func TestPricingConfigurationOrder(t *testing.T) {
	for _, raw := range []string{"0", "-1", "garbage", "NaN", "+Inf", ""} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(envPricingUSDCNY, raw)
			t.Setenv(envPricingFile, "/missing/prices.json")
			if e := assemblePricing(context.Background(), nil, nil, &pricingLogger{}); !errors.Is(e, pricing.ErrConfig) || !strings.Contains(e.Error(), envPricingUSDCNY) {
				t.Fatal(e)
			}
		})
	}
	t.Setenv(envPricingUSDCNY, "7.2")
	t.Setenv(envPricingVendorMap, "q=unknown")
	t.Setenv(envPricingFile, "/missing/prices.json")
	log := &pricingLogger{}
	if e := assemblePricing(context.Background(), nil, nil, log); e != nil || len(log.errors) != 1 {
		t.Fatalf("%v %+v", e, log)
	}
	path := filepath.Join(t.TempDir(), "valid.json")
	if e := os.WriteFile(path, []byte(`{"pricing_rule":"lowest-tier-standard-rate","vendors":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(envPricingFile, path)
	if e := assemblePricing(context.Background(), nil, nil, log); !errors.Is(e, pricing.ErrConfig) || !strings.Contains(e.Error(), envPricingVendorMap) {
		t.Fatal(e)
	}
	t.Setenv(envPricingVendorMap, "")
	if e := assemblePricing(context.Background(), nil, nil, log); e != nil || len(log.warns) != 1 {
		t.Fatalf("%v %+v", e, log)
	}
}

// 嵌入未使用的接口方法；若多命中后仍访问覆盖存储，测试会直接失败。
type pricingAttachStore struct {
	configstore.ConfigStore
	db            *gorm.DB
	providerError error
}

func (s pricingAttachStore) DB() *gorm.DB { return s.db }
func (s pricingAttachStore) RunMigration(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	return fn(ctx, s.db)
}
func (s pricingAttachStore) GetProviderConfig(context.Context, schemas.ModelProvider) (*configstore.ProviderConfig, error) {
	return nil, configstore.ErrNotFound
}
func (s pricingAttachStore) GetProvidersConfig(context.Context) (map[schemas.ModelProvider]configstore.ProviderConfig, error) {
	if s.providerError != nil {
		return nil, s.providerError
	}
	return map[schemas.ModelProvider]configstore.ProviderConfig{"custom": {NetworkConfig: &schemas.NetworkConfig{BaseURL: "https://api.vendor.example"}, CustomProviderConfig: &schemas.CustomProviderConfig{}}}, nil
}

func TestAttachPricingFailureCategory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	data := `{"pricing_rule":"lowest-tier-standard-rate","vendors":[{"id":"one","endpoint_hosts":["vendor.example"]},{"id":"two","endpoint_hosts":["api.vendor.example"]}]}`
	if e := os.WriteFile(path, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(envPricingFile, path)
	t.Setenv(envPricingUSDCNY, "7.2")
	t.Setenv(envPricingVendorMap, "")
	for _, failStore := range []bool{false, true} {
		t.Run(fmt.Sprint(failStore), func(t *testing.T) {
			db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
			if e != nil {
				t.Fatal(e)
			}
			sqlDB, e := db.DB()
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { sqlDB.Close() })
			store := pricingAttachStore{db: db}
			if failStore {
				store.providerError = errors.New("provider storage unavailable")
			}
			// attach 现在总会注册内容安全插件，需要一个真实的网关客户端。
			client, e := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: noProviderAccount{}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError)})
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(client.Shutdown)
			host := &bifrostServer.BifrostHTTPServer{Ctx: schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), Client: client, Config: &lib.Config{ConfigStore: store, ModelCatalog: modelcatalog.NewTestCatalog(nil)}, Router: router.New()}
			log := &pricingLogger{}
			e = attach(context.Background(), host, func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }, log)
			if failStore {
				if e != nil || len(log.errors) != 1 {
					t.Fatalf("storage failure should continue: %v %+v", e, log)
				}
			} else if !errors.Is(e, pricing.ErrConfig) {
				t.Fatalf("multiple matches must reject attach: %v", e)
			}
		})
	}
}

// noProviderAccount 供装配测试构造网关客户端；任何 provider 调用都会失败。
type noProviderAccount struct{}

func (noProviderAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) { return nil, nil }
func (noProviderAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return nil, errors.New("test must not call a provider")
}
func (noProviderAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	return &schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{BaseURL: "http://127.0.0.1:1"}}, nil
}
