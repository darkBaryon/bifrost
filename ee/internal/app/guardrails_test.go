// 本文件验证开发假检测器的场景开关，以及库中坏配置时装配拒绝启动并给出恢复说明。
package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	guardrailsstore "github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	"github.com/fasthttp/router"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDevelopmentGuardrailsOptIn(t *testing.T) {
	for _, mode := range []string{"true", "input-blok"} {
		t.Run(mode, func(t *testing.T) {
			if err := validateFakeMode(mode); err == nil || !strings.Contains(err.Error(), envGuardrailsFake) {
				t.Fatalf("invalid development mode accepted: %v", err)
			}
		})
	}
	for _, mode := range []string{fakeInputBlock, fakeInputObserve, fakeOutputBlock} {
		if err := validateFakeMode(mode); err != nil {
			t.Fatal(err)
		}
		if _, err := fakeChecker(mode); err != nil {
			t.Fatal(err)
		}
	}
}

// 库中已有配置但无法构建（坏 JSON、判官 provider 不存在）时拒绝启动，错误里带恢复方式。
func TestStoredConfigurationFailuresRejectStartup(t *testing.T) {
	for name, tc := range map[string]struct {
		stored string
		want   string
	}{
		"broken json":      {stored: "{not json", want: "delete the ee_guardrails row"},
		"missing provider": {stored: `{"deny":{"status":400,"message":"no"},"judge":{"provider":"gone","model":"m"},"harmful":{"enabled":true,"stages":["input"],"threshold":"medium","on_match":"block","on_error":"block"}}`, want: "fix the judge provider"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envGuardrailsFake, "")
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, _ := db.DB()
			t.Cleanup(func() { sqlDB.Close() })
			if err := guardrailsstore.Migrate(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if _, err := guardrailsstore.NewStore(db).Update(context.Background(), tc.stored, guardrailsstore.UnsetVersion); err != nil {
				t.Fatal(err)
			}
			client, err := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: noProviderAccount{}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Shutdown)
			host := &bifrostServer.BifrostHTTPServer{Ctx: schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), Client: client, Config: &lib.Config{ConfigStore: pricingAttachStore{db: db}, ModelCatalog: modelcatalog.NewTestCatalog(nil)}, Router: router.New()}
			err = assembleGuardrails(context.Background(), host, func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }, &pricingLogger{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("startup accepted a broken stored configuration: %v", err)
			}
		})
	}
}
