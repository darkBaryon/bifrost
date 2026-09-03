# lib/account.go:Config 套上 Account 接口皮

105 行,一个 struct 一个字段 `store *Config`,三个方法全转发给 Config。它是 core 向外问"你有哪些 provider、各自的 key 和参数"时,transports 给的回答。

## 契约:core 只要三样

`schemas.Account`(core/schemas/account.go:828):

| 方法 | core 何时调 | 用来 |
|---|---|---|
| `GetConfiguredProviders()` | `Init` 一次(bifrost.go:319) | 每个 provider 建一条队列和一组 worker |
| `GetConfigForProvider(p)` | Init 和每次请求 | 并发数、缓冲、超时、代理 |
| `GetKeysForProvider(ctx, p)` | 每次请求选 key 前(:8371) | 候选 key 交给加权随机 |

预算、virtual key、路由 core 一概不问,那是插件的事。接口小到只剩引擎不可缺的三样,SDK 用户自己实现只要十五行(写死一个 key 即可)。

## 接口怎么工作(复习)

A 定义接口,B 实现接口,DI 把 B 传给 A:

```
core:  account schemas.Account            ← 字段是接口类型,core 不认识任何具体 struct
Bootstrap: bifrost.Init(BifrostConfig{Account: lib.NewBaseAccount(s.Config)})   ← 塞进去,方法齐了自动算实现
core 运行时: bifrost.account.GetKeysForProvider(ctx, p)  → 动态分发到 (*BaseAccount).GetKeysForProvider → 读 Config.Providers
```

编译期看谁 import 谁(transports → core),运行期看谁被塞进了谁的接口字段(core 调 transports 的对象)。仓库里同一套路的地方:Logger、LLMPlugin、Tracer、ModelInfoProvider 都在 `BifrostConfig{...}` 里塞;handler 的小接口由 server 实现,在 `RegisterAPIRoutes` 里塞。想知道某个接口调用真正跑的是谁,去 Bootstrap 搜赋值点。

## 三个方法各一个细节

**`GetKeysForProvider` 按 ctx 过滤**:治理插件在 pre-hook 里把这个 VK 允许的 key ID 列表塞进 ctx(`GovernanceIncludeOnlyKeys`),Account 取出来过滤。core 不知道治理存在却执行了治理的限制。空列表和没有列表是两个意思:没有全放行,有但为空一个都不给。这是插件通过 ctx 影响 core 的标准通道。

**`GetConfigForProvider` 做类型翻译**:`configstore.ProviderConfig`(framework,库的形状,字段可空表示"没配")→ `schemas.ProviderConfig`(core,字段实心),nil 填 `DefaultNetworkConfig`、`DefaultConcurrencyAndBufferSize`。同名两个类型分属两个模块,边界上翻译,只能在两边都 import 的 lib 做。

**`GetProviderConfigRaw` 返回共享引用**(config.go:5249):Config 读方法里唯一不拷贝的,注释 "CRITICAL: Never modify",因为在每请求热路径上。过滤时新建 slice 不原地改。

## 对热更新的推论

Account 每次调用现读 Config,不缓存。所以改 key、改超时不用通知 core,下个请求自动生效,`OnKeyUpdated` 里没有一行是通知 core 的。加 provider 要通知,因为队列是 Init 时建的。

Account 无状态,`ReloadClientConfigFromConfigStore` 重建它是无害的多余动作(笔记 02 的疑问已解)。

## 为什么在 lib

要同时 import framework 的 configstore 类型和 core 的 schemas 类型;它是"给 core 的东西",和 Config 放一起比和 HTTP 装配放一起顺。

下一步:第 5 步 `server/server.go:2054 RegisterAPIRoutes`,路由总目录。
