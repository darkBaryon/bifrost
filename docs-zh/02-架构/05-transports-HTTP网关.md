# 05 · transports —— HTTP 网关

`transports/` 下唯一的传输实现是 `bifrost-http/`（Go main 二进制，也就是 `npx @maximhq/bifrost` 与 Docker 镜像里跑的东西）。它是整个系统的**组合根**：加载配置 → 构造 framework 各 Store → 装配插件 → 注入 core → 注册路由 → 起服务。

另有：`config.schema.json`（约 323KB 的 JSON Schema，是 `config.json` 的**唯一权威定义**）、三个 Dockerfile、版本与变更日志。

## 技术选型与启动流程

- HTTP 框架：**fasthttp**（`valyala/fasthttp` + `fasthttp/router`），Prometheus 处理器经 `fasthttpadaptor` 桥接。
- `main.go`：解析 `-host/-port/-app-dir/-log-level/-log-style` 旗标（env 回退 `BIFROST_HOST`、`LOG_LEVEL`）→ `//go:embed all:ui` 嵌入前端产物 → `NewBifrostHTTPServer` → `Bootstrap(ctx)` → `Start()`。
- `server/server.go`（`BifrostHTTPServer`，约 12 万行字节）的 `Bootstrap`：
  建应用目录 → `lib.LoadConfig` → WebSocket 与通知服务 → 动态插件加载器（SSRF 白名单）→ 日志保留清理器 → `LoadPlugins` → webhook 分发器 → **构造 core 的 Bifrost 客户端** → 模型目录刷新 → 注册三组路由（推理 / 管理 API / UI）→ fasthttp.Server。
- `Start`：监听 + SIGINT/SIGTERM 优雅停机（30 秒窗口）。

## API 表面

### 推理 API（OpenAI 风格，`/v1/*`）

`handlers/inference.go` 注册：`/v1/models`、`/v1/completions`、`/v1/chat/completions`、`/v1/responses`（含检索/删除/取消/input_items/compact）、`/v1/embeddings`、`/v1/rerank`、`/v1/ocr`、`/v1/audio/{speech,transcriptions}`、`/v1/images/{generations,edits,variations}`、`/v1/videos`、`/v1/batches`、`/v1/files`、`/v1/containers`。另有异步推理（`asyncinference.go`）、MCP 工具执行（`mcpinference.go`）、对外 MCP Server（`mcpserver.go`，SSE/streamable HTTP）。

### 管理 API（`/api/*`，供控制台使用）

providers、keys、models、governance（虚拟 Key/客户/团队/预算/限流/路由规则/模型配置）、logs、mcp、oauth2、plugins、prompt-repo、skills、webhooks、cache、notifications、session、feature-flags、pricing、scim、ws、dev/pprof 等。对应 `handlers/` 下 27 个处理器文件（`governance.go` 约 219KB 是最大的）。

### SDK 兼容层（drop-in，`integrations/`）

把各家 SDK 的原生请求格式转换为 Bifrost 内部格式（共享分发逻辑在 `integrations/router.go`）：

- 原生格式路由：`/openai/*`（含 Azure deployments、realtime）、`/anthropic/v1/messages`、`/genai/v1beta/models/{model}`（Google）、`litellm`、`/cohere/v2/rerank`、`langchain`、`pydanticai`、`bedrock`、`cursor`。
- 直通（passthrough）路由：genai / chatgpt / openai / anthropic / azure / runware 的透传变体。

这就是"只改 base URL 即可接入"的实现层。

## 配置加载

`lib/config.go` 的 `LoadConfig`：

- 读 `<app-dir>/config/config.json`（缺失则用默认值），外加 `config.db`（配置库）与 `logs.db`（日志库）。
- 所有敏感值支持 `env.VAR_NAME` 间接引用。
- `initStores`：未声明存储时默认 SQLite；可切 Postgres/ClickHouse。
- `source_of_truth` 决定 config.json 与数据库谁为准（按 Provider 配置哈希做调和）。

## Web UI 的托管方式

`ui/` 前端构建产物被复制到 `transports/bifrost-http/ui/`，由 `//go:embed all:ui` 打进二进制，`handlers/ui.go` 托管 `/` 与所有静态路径；开发模式下代理到本地 Vite（localhost:3000）。
（⚠️ 本地首次编译如果没有这个目录会报 embed 错误，`make dev` 会自动建占位目录，详见 [环境搭建与排错](../04-开发指南/01-环境搭建与排错.md)。）

## 中间件与实时通道

- `handlers/middlewares.go`：安全头、可热更新的 CORS、请求解压、`AuthMiddleware`（推理路由允许虚拟 Key 认证，管理路由走会话/令牌，另有 bootstrap/setup token 与白名单路由）、追踪中间件。
- 治理限流/预算不是原始中间件，而是 governance **插件**在 PreLLMHook 里执行。
- 实时通道：`/ws`（控制台事件流）、`/v1/realtime`（WebSocket + WebRTC 实时语音）、签名 WS ticket、SSE 流读取器（`lib/streamreader.go`）。
