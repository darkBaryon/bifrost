# 02 · core —— 核心引擎

`core/` 是整个项目的心脏：一个可独立使用的 Go 库（`go get github.com/maximhq/bifrost/core`），负责请求排队、Provider 调度、Key 选择、故障切换、流式处理和 MCP 工具调用。它**不依赖**仓库内任何其他模块。

## 目录结构

```
core/
├── bifrost.go          # 主结构体：请求排队、Provider 生命周期（约 3400 行）
├── inference.go        # 推理路由、fallback、流式分发（约 1900 行）
├── mcp.go              # MCP 集成入口
├── schemas/            # 全部共享类型定义（41 个文件）—— 整个仓库的"通用语言"
├── providers/          # 29 个 Provider 实现
├── pool/               # 泛型对象池 Pool[T]（生产/调试双模式）
├── mcp/                # MCP 协议实现（Agent 循环、客户端管理、工具管理）
└── internal/           # llmtests / mcptests 测试基础设施
```

## schemas/ —— 类型即契约

`core/schemas/` 定义所有跨模块共享的类型，是理解代码库的最佳入口：

| 文件 | 内容 |
|---|---|
| `bifrost.go` | `BifrostConfig`、`ModelProvider` 枚举、`RequestType` 枚举、context key 常量 |
| `provider.go` | **Provider 接口（30+ 方法）**、`NetworkConfig`、`ProviderConfig` |
| `plugin.go` | 4 类插件接口（详见 [04-plugins-插件体系.md](04-plugins-插件体系.md)） |
| `context.go` | `BifrostContext` —— 可变值的自定义 context |
| `chatcompletions.go` / `responses.go` / `embedding.go` / `images.go` | 各推理形态的请求/响应类型 |
| `batch.go` / `files.go` | 批处理与文件管理类型 |
| `mcp.go` / `trace.go` / `logger.go` | MCP 类型、Tracer 接口、Logger 接口 |

### BifrostContext：可变的自定义 Context

标准 Go context 值不可变，而 `BifrostContext` 支持创建后线程安全地写值（内部 RWMutex）：

```go
ctx := schemas.NewBifrostContext(parent, deadline)
ctx.SetValue(key, value)
```

- **保留键**（内部设置，插件勿动）：选中的 Key ID、治理信息、重试次数、fallback 序号、追踪 span 等。`BlockRestrictedWrites()` 会静默丢弃对保留键的写入。
- **用户可设键**：`x-bf-vk`（虚拟 Key）、`x-bf-api-key`/`x-bf-api-key-id`（显式指定 Key）、请求 ID、额外转发 Header、自定义 URL 路径、跳过 Key 选择、原始请求体直传。
- **硬性规则**：context 里只放小句柄（ID、布尔、指针），**禁止放随流内容增长的数据**（chunk 缓冲等）——那些必须放在以 RequestID 为键的顶层管理器里（参考 `framework/streaming.Accumulator`）。

## providers/ —— 29 家供应商实现

已支持：anthropic、azure、bedrock、bedrockmantle、cerebras、cohere、deepseek、elevenlabs、fireworks、gemini、groq、huggingface、mistral、nebius、ollama、openai、opencode、openrouter、parasail、perplexity、replicate、runware、runway、sarvam、sgl、vertex、vllm、wafer、xai（另有共享工具包 `utils/`）。

实现分**两类**：

1. **非 OpenAI 兼容**（anthropic、bedrock、gemini、cohere 等）：完整实现 —— `types.go`（专有结构体）、`errors.go`（错误转换）、`chat.go`/`embedding.go`/`speech.go`/`responses.go`（纯转换函数）。
2. **OpenAI 兼容**（groq、cerebras、ollama、perplexity、openrouter、xai 等）：极简 —— 构造函数 + 委托给 `openai.HandleOpenAI*`。

约定：

- 转换函数命名 `To<Provider><Feature>Request()` / `ToBifrost<Feature>Response()`，必须是**纯函数**（无 HTTP、无日志、无副作用）。
- 每个 Provider 持有**两个 fasthttp 客户端**：`client`（一元请求，30s 整体超时）和 `streamingClient`（SSE 流，清零超时，靠每 chunk 空闲计时器 `NewIdleTimeoutReader` 兜底）。漏用后者会导致流在 30 秒被掐断。
- 例外：Bedrock 用 `net/http`（需要 HTTP/2 应对 AWS 每连接 100 流的上限）。
- 错误统一转成 `*schemas.BifrostError`，携带 `Provider`、`ModelRequested`、`RequestType` 元数据。

### Provider 接口覆盖的能力

`ListModels`、`ChatCompletion(Stream)`、`Responses(Stream)`（OpenAI Responses API）、`TextCompletion(Stream)`、`Embedding`、`Speech(Stream)`、`Transcription(Stream)`、`ImageGeneration/Edit/Variation(Stream)`、`CountTokens`、`Batch*`、`File*`、`Container*` —— 不支持的操作返回 "not supported"。

流式方法接收 `PostHookRunner` 回调、返回 `chan *BifrostStreamChunk`。

## 请求调度模型

- 每个 Provider 一条独立队列 + worker 池（channel 隔离，互不拖累）。
- Key 选择为**加权随机**（约 10ns），支持一个 Provider 配多把 Key 按权重分流。
- fallback 链在 `inference.go` 中实现：主 Provider 失败后按序尝试备选；插件可通过 `BifrostError.AllowFallbacks` 阻止或放行。

## pool/ —— 泛型对象池

```go
p := pool.New[MyType]("name", func() *MyType { return &MyType{} })
obj := p.Get()
// 使用后必须手动清零所有字段再归还！
p.Put(obj)
```

- 默认构建：零开销 `sync.Pool` 包装（`pool_prod.go`）。
- `-tags pooldebug`：追踪双重释放、释放后使用、泄漏（带调用栈）（`pool_debug.go`），配套 `/dev/pprof` 调试端点。
- **最大坑点**：归还前必须清零全部字段，否则上一个请求的数据会泄漏给下一个请求，调试构建也查不出来。

## mcp/ —— MCP 工具网关

让静态聊天模型变成会调工具的 Agent：

| 文件 | 职责 |
|---|---|
| `agent.go` | Agent 编排循环（多轮工具调用：模型返回 tool_calls → 执行 → 回填结果 → 再问模型） |
| `clientmanager.go` | MCP 客户端连接生命周期管理 |
| `toolmanager.go` | 工具注册、发现、过滤 |
| `healthmonitor.go` | MCP 客户端健康监控 |
| `codemode/starlark/` | Code-mode：在 Starlark 沙箱里执行模型生成的代码 |

请求带工具时，core 在 PreLLMHook 之后注入 MCP 工具定义；响应含 `tool_calls` 时进入 Agent 循环执行并回灌，直到模型给出最终答案。
