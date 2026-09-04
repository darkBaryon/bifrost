package server

import "github.com/maximhq/bifrost/core/schemas"

// ProbePluginName 是骨架期探针插件的名字, 用于日志与冒烟断言.
const ProbePluginName = "ee-probe"

// probePlugin 是 no-op 的 LLMPlugin. 纯 BasePlugin 会被 InferPluginTypes 推断为无类型,
// 显示 active 但不进任何 hook 链; 实现 LLMPlugin 才真正挂进请求路径 (评审1 已核).
// 正式插件将来放 ee/plugins/<name> (镜像上游 plugins/), 这里只证明 SyncLoadedPlugin 可用.
type probePlugin struct{}

func (probePlugin) GetName() string { return ProbePluginName }
func (probePlugin) Cleanup() error  { return nil }

func (probePlugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

func (probePlugin) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}

func (probePlugin) PostLLMHook(_ *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return resp, bifrostErr, nil
}
