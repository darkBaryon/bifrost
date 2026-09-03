# core/bifrost.go:Bifrost 引擎与 Init

`core/bifrost.go` 8779 行,第 70 行 `type Bifrost struct` 是 core 的引擎对象,transports 里的 `s.Client`。一句话:**每家 provider 一条流水线的引擎,Init 建流水线,请求带着回信地址进流水线,worker 处理完按地址写回。**

## 编排在干什么(先看这个)

一个请求进来要按顺序做十个决定,编排就是做决定的顺序:

| 顺序 | 决定 | 谁答 | 代码位置 |
|---|---|---|---|
| 1 | 这个 VK 是谁、有预算吗、限流到了吗 | 治理插件 | `tryRequest` 的 `RunLLMPreHooks`,可短路 |
| 2 | 最近有人问过一样的吗 | 语义缓存插件 | 同上 |
| 3 | 真发给 openai 吗,有路由规则吗 | 路由插件 | `handleRequest` 开头 `PreRequestHook`(只跑一次,fallback 不重跑) |
| 4 | 三个 key 用哪个 | key 池 | `requestWorker` 建池 |
| 5 | 队列满了等还是拒 | 队列 | `tryRequest` 往 `ProviderQueue` 塞的几个 select |
| 6 | 翻译成上游格式发出去 | 适配器 | `handleProviderRequest` 拆信封 |
| 7 | 429 换 key 再试? | 重试 | `executeRequestWithRetries` |
| 8 | 500 换家? 这个错允许 fallback 吗 | fallback | `handleRequest` 的 for 循环 + `shouldTryFallbacks` |
| 9 | 用了多少 token 记到谁账上 | 治理/日志插件 | `RunLLMPostHooks`,逆序 |
| 10 | 翻译回来,缓存存一份 | 适配器/缓存插件 | 同上 |

顺序九成是被两条规则逼出来的:便宜能拒绝的在前、贵的在后;后一步要前一步的答案(选 key 要先知道 provider)。人定的只有插件之间的次序(`server/plugins.go`:telemetry 1、prompts 2、logging 3、governance 4、otel 5、semanticcache 6、compat 7、maxim 8,resolver 垫底)和 post-hook 逆序(先进后出)。nginx 十一阶段、Envoy filter 顺序本质是同一序列。

## 结构体 30 个字段分四类

| 类 | 字段 |
|---|---|
| 注入的接口 | `account`、`logger`、`tracer`、`modelCatalog`、`keySelector`、`keyPoolFilter`、`kvStore`、`mcpCredStore`(A 定义 B 实现,这里是 A 收货) |
| 热更新原子指针 | `llmPlugins`、`mcpPlugins`、`providers`(换整个 slice 读方无锁)、`dropExcessRequests` |
| 每 provider 一份的运行时状态 | `requestQueues`、`waitGroups`、`providerMutexes`、`retiredWorkerWaits` 四个 `sync.Map` |
| 六个对象池 | channelMessage、responseChannel、errorChannel、responseStream、pluginPipeline、bifrostRequest,Init 预热 5000 |

## Init(219-379)五步

补默认值(Logger/Tracer/KeySelector 都有默认,SDK 用户只传 Account 就能跑)→ 建六个池预热 → 问 Account 有哪些 provider → 有 MCP 配置建 MCPManager 但**不连接** → 每个 provider `prepareProvider`。

`prepareProvider`(4453):建 `ProviderQueue{queue: make(chan, BufferSize), done}` → `createBaseProvider` 造适配器 → CAS 追加进 providers 列表 → 起 Concurrency 个 `requestWorker`。两个数字来自 Account 的 provider 配置。启动时没有的 provider,第一个请求来时 `getProviderQueue`(4504)懒建:读锁查、升写锁、双重检查、现场 prepare。

```
tryRequest ──塞──→ openai    → chan → N 个 worker → OpenAI      一家慢只堵自己那条(故障隔离第一层)
                   anthropic → chan → 一组 worker → Anthropic
```

## 两种 channel

`ChannelMessage`(60):嵌入信封 + ctx + 三条**这个请求自己的**回信 channel(`Response`/`ResponseStream`/`Err`,长度 1,池化)。进的方向一家一条共享,回的方向一请求一组私有。worker 处理完往回信 channel 写,`tryRequest` 在另一头 select 等。

## 队列永远不 close(100-133 注释,README 标的必读)

往关闭的 channel 发送会 panic;"检查关没关"和"发送"之间总有时间窗;`select` case 求值顺序随机,同一 select 放 `<-done` 也救不了。解法:不 close 队列,只 close 旁路的 `done` 信号(从关闭的 channel **接收**永远安全),队列等没人引用靠 GC。Go 并发经典坑的教科书处理。

## Shutdown(8712)

cancel ctx → 每条队列 `signalClosing` → 等每组 worker WaitGroup → 等热更新退役的老 worker → 队列残留请求全部回错误 → 清 MCP、tracer、插件。server 的 Start 关闭段先停 HTTP 再调它。

## 它是上帝类吗

136 个方法:68 个是按类型的公开入口(信封变体税,各三行:装信封、调 handleRequest、拆信封);约 11 个池取放;真正逻辑约 55 个,分三堆——provider 生命周期(prepare/getQueue/Update/Remove)、请求编排(handle/try/stream/fallback)、key 池(select…WithPool/executeRequestWithRetries)。已拆出去的:`PluginPipeline`(同文件独立类型)、`ProviderQueue`、`core/mcp`、`keyselectors`、`providers/*`。

形状对(管道形产品天然有驱动者,nginx/Envoy 同),尺寸没管(驱动者应只握指针只写顺序,每站独立类型;这里三堆逻辑和它们的字段全在一个 struct 上,8779 行一个文件)。粗粒度解耦做了(模块单向依赖、接口全在 schemas),细粒度没做。竞品里自研单二进制的(LiteLLM、New API、Portkey)都长了上帝对象,建在 Envoy 上的形状是 Envoy 给的。

## 读法

大纲按名字分组,68 个 `*Request` 入口只看 `ChatCompletionRequest` 一个,池方法跳过,剩下按三堆读。**编排细节按功能触发,不按顺序补**:写插件只需要知道管道几站、挂哪站、拿到什么、返回什么会怎样。第二阶段重心从 9/10/11 挪到 12/13/15。

下一步:第 9 步 fallback 编排,骨架级(`shouldTryFallbacks`、`AllowFallbacks`、pre-hook 短路的两条路)。
