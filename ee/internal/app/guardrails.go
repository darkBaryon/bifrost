// 本文件装配内容安全：建表、从配置库构建检查器、注册插件与三条管理接口；显式开发开关下改用假检测器。
package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/fake"
	guardrailshost "github.com/darkBaryon/bifrost/ee/internal/guardrails/host"
	guardrailshttp "github.com/darkBaryon/bifrost/ee/internal/guardrails/http"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	safetyplugin "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
	"github.com/maximhq/bifrost/core/schemas"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

const (
	// 未设置或为空时走生产装配；只有明确指定联调场景才注册假检测器，且与库中配置互斥。
	envGuardrailsFake = "EE_GUARDRAILS_FAKE"
	fakeInputBlock    = "input-block"
	fakeInputObserve  = "input-observe"
	fakeOutputBlock   = "output-block"
	// 以下为开发夹具的固定配置，不代表生产防护策略。
	fakeDetectorID   = "dev-fake"
	fakeMaxTextBytes = config.DefaultMaxTextBytes
	fakeTimeout      = time.Second
	fakeDenyStatus   = fasthttp.StatusBadRequest
	fakeDenyMessage  = "开发测试：内容命中假检测器，已拦截"
	// 在内置插件之前执行，覆盖可能提前返回的缓存等响应。
	guardrailsPluginOrder = math.MinInt
)

// assembleGuardrails 按顺序：建表 → 读库 → 构建（失败拒启）→ 注册插件 → 注册接口。
func assembleGuardrails(ctx context.Context, host *bifrostServer.BifrostHTTPServer, auth schemas.BifrostHTTPMiddleware, log schemas.Logger) error {
	mode := os.Getenv(envGuardrailsFake)
	if mode != "" {
		if err := validateFakeMode(mode); err != nil {
			return err
		}
	}
	if host == nil || host.Client == nil || host.Router == nil || host.Config == nil || host.Config.ConfigStore == nil {
		return errors.New("ee: guardrails require the bootstrapped Bifrost client, router and config store")
	}
	if err := host.Config.ConfigStore.RunMigration(ctx, persistence.Migrate); err != nil {
		return fmt.Errorf("ee: migrate guardrails: %w", err)
	}
	store := persistence.NewStore(host.Config.ConfigStore.DB())
	row, err := store.Read(ctx)
	configured := err == nil
	if err != nil && !errors.Is(err, persistence.ErrNotConfigured) {
		return fmt.Errorf("ee: read guardrails configuration: %w", err)
	}
	if mode != "" && configured {
		return fmt.Errorf("ee: %s cannot be combined with stored guardrails configuration; unset it or reset the configuration", envGuardrailsFake)
	}
	builder, err := guardrailshost.NewBuilder(host.Client, host.Config.ConfigStore, log)
	if err != nil {
		return err
	}
	// 未配置时 checker 为 nil，插件透传，不需要拒绝策略。
	var checker *guardrails.Checker
	var options safetyplugin.Options
	switch {
	case mode != "":
		if checker, err = fakeChecker(mode); err != nil {
			return err
		}
		options = safetyplugin.Options{StatusCode: fakeDenyStatus, DenyMessage: fakeDenyMessage}
	case configured:
		cfg, parseErr := config.Parse([]byte(row.Config))
		if parseErr != nil {
			return fmt.Errorf("ee: stored guardrails configuration is invalid (fix it or delete the ee_guardrails row): %w", parseErr)
		}
		if checker, err = builder.Build(ctx, cfg); err != nil {
			return fmt.Errorf("ee: build guardrails from stored configuration (fix the judge provider or delete the ee_guardrails row): %w", err)
		}
		options = safetyplugin.Options{StatusCode: cfg.Deny.Status, DenyMessage: cfg.Deny.Message}
	}
	plugin, err := safetyplugin.New(checker, options, log)
	if err != nil {
		return err
	}
	if err := host.SyncLoadedPlugin(ctx, plugin.GetName(), plugin, schemas.Ptr(schemas.PluginPlacementPreBuiltin), schemas.Ptr(guardrailsPluginOrder)); err != nil {
		return fmt.Errorf("ee: register guardrails plugin: %w", err)
	}
	handler, err := guardrailshttp.NewHandler(store, builder, plugin, log, func() bool { return mode != "" })
	if err != nil {
		return err
	}
	if err := handler.RegisterRoutes(host.Router, auth); err != nil {
		return fmt.Errorf("ee: register guardrails routes: %w", err)
	}
	switch {
	case mode != "":
		log.Warn("DEVELOPMENT ONLY: fake guardrails enabled (%s); matches [guardrails-test], provides no real safety protection", mode)
	case configured:
		log.Info("content safety: configuration version %d loaded", row.Version)
	default:
		log.Info("content safety: not configured; requests pass through until POST /api/guardrails/update")
	}
	return nil
}

func validateFakeMode(mode string) error {
	switch mode {
	case fakeInputBlock, fakeInputObserve, fakeOutputBlock:
		return nil
	}
	return fmt.Errorf("%s must be %s, %s or %s; unset it to disable", envGuardrailsFake, fakeInputBlock, fakeInputObserve, fakeOutputBlock)
}

func fakeChecker(mode string) (*guardrails.Checker, error) {
	rule := guardrails.Rule{
		ID: fakeDetectorID, DetectorID: fakeDetectorID, Category: guardrails.BusinessRule,
		Stage: guardrails.Input, Threshold: guardrails.High,
		OnMatch: guardrails.Block, OnError: guardrails.Block, Timeout: fakeTimeout,
	}
	switch mode {
	case fakeInputObserve:
		rule.OnMatch = guardrails.Observe
	case fakeOutputBlock:
		rule.Stage = guardrails.Output
	}
	return guardrails.New([]guardrails.Rule{rule}, map[string]guardrails.Detector{fakeDetectorID: fake.Detector{}}, fakeMaxTextBytes)
}
