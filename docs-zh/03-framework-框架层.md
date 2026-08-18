# 03 · framework —— 框架层（持久化与公共服务）

`framework/` 是构建在 `core/schemas` 之上的共享服务层：数据库、缓存、模型目录、流式聚合、追踪、鉴权工具等。它被插件和 transports 消费，但**本身不是插件**；`core` 也从不反向依赖它。

模块入口：`framework/config.go`（`FrameworkConfig`）、`framework/list.go`（依赖枚举：`vector_store` / `config_store` / `logs_store`）。

## 存储类包

| 包 | 职责 | 支持后端 |
|---|---|---|
| `configstore/` | **主配置数据库**：Provider 与 Key、虚拟 Key/团队/客户/预算、限流、插件配置、Prompt 及版本、MCP 客户端与 OAuth2、模型定价覆盖、会话、功能开关、通知、后台任务（sidekiq）、webhook 任务、分布式锁（`dlock.go`）、列级加密（`encryption.go`）。表结构在 `configstore/tables/`（约 40 个文件），GORM 迁移在 `migrations.go` | SQLite、PostgreSQL |
| `logstore/` | **请求日志库**：批量写入（`writer.go`）、保留期清理（`cleaner.go`）、物化视图聚合与自愈（`matviews.go`）、异步导出任务；`HybridLogStore`（`hybrid.go`）把大负载卸载到对象存储、SQL 只留元数据 | SQLite、PostgreSQL、ClickHouse（+ 混合对象存储模式） |
| `vectorstore/` | 语义缓存用的通用向量库接口（`store.go`） | Weaviate、Redis、Qdrant、Pinecone |
| `objectstore/` | S3 兼容的 Blob 抽象（带 gzip），用于日志负载卸载 | S3（含 MinIO/R2）、GCS |
| `postgresconn/` | 共享 Postgres 连接器：连接池调优、IAM/密码命令认证、singleflight 复用 | PostgreSQL |
| `migrator/` | 基于 gormigrate 的迁移执行器，configstore 与 logstore 共用 | — |
| `kvstore/` / `lrucache/` | 进程内 TTL KV 存储、带索引的有界 LRU（DB 行的内存镜像） | 内存 |
| `queryscope/` | `QueryScope func(*gorm.DB) *gorm.DB` 原语，供企业版数据访问控制在不引入循环依赖的前提下约束查询范围 | — |

## 模型目录与定价

- `modelcatalog/` —— Bifrost 的"定价大脑"，由三个子包组成：
  - `datasheet/`：模型价格、参数、能力表；`sync.go` 每小时从远端数据表同步（同步期间持分布式锁）；`overrides.go` 支持自定义定价覆盖。
  - `live/`：按 `(provider, keyID)` 缓存实时 list-models 结果。
  - `keyconfig/`：从 Key 配置派生模型允许/屏蔽清单与别名。
- `mcpcatalog/` —— MCP 工具级计价（`{server}/{tool}` → 每次执行成本）。

## 流式与可观测

- `streaming/` —— **流式聚合器**：把 SSE 增量重组为完整 `BifrostResponse`，让 PostLLMHook 能看到完整响应。按模态分文件：`chat.go`、`responses.go`、`audio.go`、`images.go`、`transcription.go`、`passthrough.go`；`gate.go` 提供暂停/恢复门控。每个流的缓冲保存在以 RequestID 为键的管理器里，context 只存 `AccumulatorID`。
- `tracing/` —— 分布式追踪：`tracer.go` 实现 `schemas.Tracer`（对各观测插件的 `Inject` 做并发上限 1024 的调度）、`llmspan.go`、`propagation.go`（W3C traceparent）、`store.go`（流式请求的延迟 span）。

## 鉴权 / 密钥 / 基础设施工具

| 包 | 职责 |
|---|---|
| `oauth2/` | MCP 服务器的 OAuth2 全流程：RFC 8414 发现、PKCE 授权、令牌交换与刷新（singleflight）、按用户令牌缓存 |
| `mcp_headers/` | 按用户 Header 凭据的 MCP 认证存储（`MCPAuthTypePerUserHeaders`） |
| `temptoken/` | 短时效范围令牌（WebSocket ticket、OAuth 流程用） |
| `encrypt/` | AES-256-GCM 可逆加密 + argon2/bcrypt 哈希 |
| `envutils/` | `env.VAR_NAME` 引用解析（配置里到处在用） |
| `featureflags/` | 进程级功能开关，优先级：config.json > 数据库 > 代码默认值；支持集群 gossip 同步 |
| `sidekiq/` | 数据库支撑的后台任务执行器（认领/心跳/分区认领） |
| `webhooks/` | 出站 webhook 投递：任务队列 + 重试退避（`dispatcher.go`）+ HMAC 签名（`signer.go`） |
| `routing/` | CEL 表达式辅助（路由规则里 `headers[...]` 键归一化等） |
| `plugins/` | **动态插件加载框架**：`SharedObjectPluginLoader` 从本地路径或经 SSRF 加固的 HTTP 下载加载 `.so` 插件，自动探测其实现的接口类型 |

## 本地测试后端

`tests/docker-compose.yml` 一键拉起 framework 测试所需的 postgres、clickhouse、redis（4 种变体）、weaviate、qdrant、pinecone 模拟器：

```bash
docker compose -f tests/docker-compose.yml up -d
make test-framework
```
