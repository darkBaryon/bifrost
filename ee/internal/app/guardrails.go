// 本文件按显式开发配置装配假检测器，并注册到宿主已有的插件管线。
package app

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/detectors/fake"
	safetyplugin "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
	"github.com/maximhq/bifrost/core/schemas"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

const (
	// 未设置或为空时关闭；只有明确指定联调场景才注册，不写入持久配置。
	envGuardrailsFake = "EE_GUARDRAILS_FAKE"
	fakeInputBlock    = "input-block"
	fakeInputObserve  = "input-observe"
	fakeOutputBlock   = "output-block"
	// 以下为开发夹具的固定配置，不代表生产防护策略。
	fakeDetectorID   = "dev-fake"
	fakeMaxTextBytes = 64 * 1024
	fakeTimeout      = time.Second
	// 开发夹具的拒绝状态。
	fakeDenyStatus = fasthttp.StatusBadRequest
	// 在内置插件之前执行，覆盖可能提前返回的缓存等响应。
	fakePluginOrder = math.MinInt
)

func assembleGuardrails(ctx context.Context, host *bifrostServer.BifrostHTTPServer, log schemas.Logger) error {
	mode := os.Getenv(envGuardrailsFake)
	if mode == "" {
		return nil
	}
	rule := guardrails.Rule{
		ID: fakeDetectorID, DetectorID: fakeDetectorID, Category: guardrails.BusinessRule,
		Stage: guardrails.Input, Threshold: guardrails.High,
		OnMatch: guardrails.Block, OnError: guardrails.Block, Timeout: fakeTimeout,
	}
	switch mode {
	case fakeInputBlock:
	case fakeInputObserve:
		rule.OnMatch = guardrails.Observe
	case fakeOutputBlock:
		rule.Stage = guardrails.Output
	default:
		return fmt.Errorf("%s must be input-block, input-observe or output-block; unset it to disable", envGuardrailsFake)
	}
	checker, err := guardrails.New([]guardrails.Rule{rule}, map[string]guardrails.Detector{fakeDetectorID: fake.Detector{}}, fakeMaxTextBytes)
	if err != nil {
		return err
	}
	plugin, err := safetyplugin.New(checker, safetyplugin.Options{
		StatusCode: fakeDenyStatus, DenyMessage: "开发测试：内容命中假检测器，已拦截",
	}, log)
	if err != nil {
		return err
	}
	if err := host.SyncLoadedPlugin(ctx, plugin.GetName(), plugin,
		schemas.Ptr(schemas.PluginPlacementPreBuiltin), schemas.Ptr(fakePluginOrder)); err != nil {
		return fmt.Errorf("ee: register development guardrails: %w", err)
	}
	log.Warn("DEVELOPMENT ONLY: fake guardrails enabled (%s); matches [guardrails-test], provides no real safety protection", mode)
	return nil
}
