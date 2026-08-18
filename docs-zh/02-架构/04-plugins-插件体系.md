# 04 · plugins —— 插件体系

插件是 Bifrost 扩展能力的统一机制：治理、日志、缓存、遥测全部以插件形式挂在核心管道上。接口定义在 `core/schemas/plugin.go`（另有 `plugin_native.go` / `plugin_wasm.go` 两种构建变体）。

## 四类插件接口

| 接口 | Hook 方法 | 调用时机 |
|---|---|---|
| `LLMPlugin` | `PreRequestHook`、`PreLLMHook`、`PostLLMHook` | 每次 LLM 请求（SDK 与 HTTP 两种形态都生效） |
| `MCPPlugin` | `PreMCPHook`、`PostMCPHook`（可选 `*MCPConnectionHook`） | 每次 MCP 工具执行 / MCP 连接事件 |
| `HTTPTransportPlugin` | `HTTPTransportPreHook`、`HTTPTransportPostHook`、`HTTPTransportStreamChunkHook` | 仅 HTTP 网关（Go SDK 形态不触发）；唯一能在进入 core 前**硬拒绝**请求的位置 |
| `ObservabilityPlugin` | `Inject(ctx, *Trace)` | 响应写回后异步调用（Trace 对象是池化的，不得持有） |

另有 `ConfigMarshallerPlugin`（配置脱敏存储/展示，脱敏失败则整体失败）。

### 关键行为约定

- **PreRequestHook**：每个顶层请求一次，是"路由阶段"——对 Provider/Model/Fallbacks 的修改对后续所有插件和每次 fallback 尝试可见。返回错误只记警告，**不能**中止请求。
- **PreLLMHook**：每次 Provider 尝试一次；可返回 `*LLMPluginShortCircuit` 短路（缓存命中、鉴权失败、限流）。
- **PostLLMHook**：响应和错误都可能为 nil；插件可以**从错误中恢复**（错误置 nil 给响应）或**作废响应**（响应置 nil 给错误）。`BifrostError.AllowFallbacks`（nil 视为 true）决定是否尝试 fallback。
- 执行顺序：Pre 按注册顺序、Post 严格**逆序（LIFO）**，且只对实际执行过 Pre 的插件跑 Post。
- `HTTPTransportStreamChunkHook` 逐 chunk 调用，可修改/跳过/中止流。
- 插件错误一律记为警告，永不直接抛给调用方。

### 位置与排序

`PluginConfig{Placement, Order}`：`pre_builtin` → `builtin` → `post_builtin`（默认）。内置插件在 `transports/bifrost-http/server/plugins.go` 中固定排序：

```
telemetry(1) → prompts(2) → logging(3) → governance(4) → otel(5)
→ semanticcache(6) → compat(7) → maxim(8) → modelcatalogresolver(始终最后)
```

## 11 个内置插件

| 插件 | 作用 |
|---|---|
| `governance/` | **最大的插件**：虚拟 Key（`sk-bf-` 前缀）、团队/客户层级、预算与预算周期、限流、模型/Provider 白名单、CEL 路由规则 + 加权负载均衡、按 Prompt 复杂度分层路由（`complexity/` 子包）、MCP 工具裁剪、用量记账 |
| `logging/` | 请求/响应审计日志写入 `framework/logstore`：流式增量更新、成本重算、内容脱敏/裁剪 |
| `telemetry/` | Prometheus 指标（`/metrics`）：HTTP 与上游调用延迟、token、成本、MCP 工具指标；多节点可用 Push Gateway 模式；附 Grafana 仪表盘样例 |
| `otel/` | OpenTelemetry 导出：OTLP over HTTP/gRPC、多导出 profile、GenAI 语义约定；唯一实现 `ObservabilityPlugin.Inject` 的插件 |
| `semanticcache/` | 语义缓存：xxhash 直接命中 + 向量余弦相似度匹配（可配阈值/TTL/命名空间）；无嵌入模型时退化为纯精确匹配；支持流式回放 |
| `maxim/` | 把 trace/generation 上报到 Maxim 观测平台（多日志仓、附件处理） |
| `prompts/` | 从配置库解析 Prompt 模板，按 `x-bf-prompt-id` / `x-bf-prompt-version` 头把模板消息前置到请求 |
| `compat/` | LiteLLM 风格兼容：丢弃目标模型不支持的参数（`dropparams.go`）、端点类型改写（text→chat、chat→responses） |
| `modelcatalogresolver/` | "没写 Provider 只写了模型名"时查模型目录反推 Provider；固定跑在路由链**最后**兜底 |
| `mocker/` | 测试/压测用 Mock 响应：按规则匹配、加权成功/错误、模拟延迟 |
| `jsonparser/` | 流式 JSON 修复：按请求 ID 累积残缺 JSON 并修复为合法 JSON |

每个插件都是独立 Go module，自带 `version`、`changelog.md` 和完整测试。

企业版另有闭源插件：`datadog`、`bigquery`、`pubsub`、`kafka`（OSS 构建只登记名字并跳过）。

## 第三方插件

- 运行时由 `framework/plugins` 的 `SharedObjectPluginLoader` 加载 `.so`（本地路径或 SSRF 加固的 HTTP 下载），自动探测实现了哪些接口。
- `examples/plugins/` 提供 hello-world 与 **WASM 插件模板**（Go/Rust/TypeScript），以及 LLM-only / HTTP-only / MCP-only / 多接口变体。
- 新增插件步骤（AGENTS.md）：建 `plugins/<name>/` 独立 go.mod → 实现接口 → 加入 go.work → 在 transport 层或配置注册 → Makefile 加测试目标。
