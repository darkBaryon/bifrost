# 阅读笔记与进度

> 本目录是源码通读过程的**工作区**:进度打卡 + 逐文件细读笔记,按读的顺序编号。
> 与 `03-源码精读` 的分工:这里记"读到哪、看到什么";读完一个主题后沉淀出的结论性文章("怎么运作、为什么这样设计")才进 03,一个主题一篇。

## 主线阅读路线(进度打卡)

带着六个问题读(格式翻译 / key 选择 / fallback / 故障隔离 / 流式 / 插件切面),每步读完打勾并链接笔记。

### 第一阶段:装配与入口(transports)

- [x] 1. `transports/bifrost-http/main.go` — 入口、flag、Bootstrap/Start 两步 → [笔记](01-transports-main入口.md)
- [x] 2. `server/server.go:2355 Bootstrap` — 装配全景(LoadConfig → 插件 → bifrost.Init → Router) → [笔记](02-server-BifrostHTTPServer.md)
- [ ] 3. `lib/config.go:857 LoadConfig` — config.json 与 DB 怎么合并(SourceOfTruthSplit)
- [ ] 4. `lib/account.go` — 配置以 `schemas.Account` 接口形态喂给 core
- [ ] 5. `server/server.go:2040 RegisterAPIRoutes` — 所有路由的总目录
- [ ] 6. `handlers/inference.go:1010 chatCompletion` — 一条完整 handler 链

### 第二阶段:core 内核

- [ ] 7. `core/schemas/bifrost.go` — BifrostRequest/BifrostResponse/RequestType 数据模型
- [ ] 8. `core/bifrost.go:70` — Bifrost 主结构体 + Init
- [ ] 9. `ChatCompletionRequest(797) → handleRequest(5089) → tryRequest(5372)` — fallback 编排
- [ ] 10. `ProviderQueue(134) + requestWorker(6552)` — 队列/worker 并发模型(TOCTOU 注释必读)
- [ ] 11. `core/schemas/provider.go:631 Provider 接口` + `core/providers/openai/` 一个实现样板

### 第三阶段:插件与有状态层

- [ ] 12. `core/schemas/plugin.go:165-206` — 插件接口与生命周期注释
- [ ] 13. `server/plugins.go` — 插件装配;挑 governance 或 logging 读一个真实插件
- [ ] 14. `integrations/router.go` — 兼容层通用引擎(RouteConfig + 双向格式转换)
- [ ] 15. `framework/{configstore,logstore,vectorstore}/store.go` 三个接口 — 热更新链路

### 支线(按需)

- [ ] `core/mcp/` — MCP 工具管理与 agent 循环
- [ ] `core/keyselectors/` — key 加权选择
- [ ] `framework/routing/` — 路由规则引擎
- [ ] `core/providers/anthropic/` — 翻译层剖析(quirk 靠测试名索引)

## 笔记列表

| 编号 | 笔记 | 覆盖范围 |
|---|---|---|
| 01 | [transports-main入口](01-transports-main入口.md) | main.go 全文:embed UI、automaxprocs、init/flag、profiling、logger 注入 |
| 02 | [server-BifrostHTTPServer](02-server-BifrostHTTPServer.md) | server.go:三个职责、回调接口与企业版、热更新、存储、成长史、导航 |
