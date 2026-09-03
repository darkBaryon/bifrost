# core/schemas/plugin.go:插件接口与生命周期

一个插件 = **一个实现了某几个接口的 Go 对象**,core 在固定时刻按固定顺序调它的方法。没有注册中心、事件总线、反射,就是接口加一个排好序的列表。server 靠类型断言(`InferPluginTypes`)判断实现了哪种,一个对象可同时是三种。

## 七个接口三层

| 层 | 接口 | 何时实现 |
|---|---|---|
| 必须 | `BasePlugin`:`GetName()`、`Cleanup()` | 所有 |
| 挂点 | `LLMPlugin` 三钩子 | 看内容的功能:安全、缓存、记忆 |
| | `MCPPlugin` + `MCPConnectionPlugin` | 管 MCP 工具权限 |
| | `HTTPTransportPlugin` 三钩子 | 只看 header/路径/原始 body |
| 附加 | `ObservabilityPlugin.Inject(trace)`(响应写完后异步,不许留 trace 指针) | 导出观测 |
| | `ConfigMarshallerPlugin`(存库前脱敏、API 响应打码,fail-closed) | 配置含密钥 |

## LLMPlugin 三钩子,返回值就是协议

| 钩子 | 跑几次 | 能 | 不能 |
|---|---|---|---|
| `PreRequestHook(ctx, req) error` | 每请求一次 | 改 provider/model/fallbacks,改动对所有后续阶段和 fallback 可见 | **不能拒绝**:返回 error 只记警告继续。要拒去 PreLLMHook 短路 |
| `PreLLMHook(ctx, req) (req, *ShortCircuit, error)` | 每尝试一次 | 改请求;`ShortCircuit{Response}` 当作上游回答;`ShortCircuit{Error}` 拒绝,`Error.AllowFallbacks` 控制换不换家 | 改动能否带到下次 fallback 不保证 |
| `PostLLMHook(ctx, resp, err) (resp, err, error)` | 每尝试一次,逆序 | 改响应;err 置 nil 给 resp = 恢复;resp 置 nil 给 err = 作废 | 假设 resp/err 非空,两个都可能 nil |

第三个返回值 `error` 三处语义一致:**插件自己出错**,core 记警告、跳过、继续,永远不变成用户响应。想影响用户只能靠前两个返回值——返回 error ≠ 拒绝请求。对称性:第 i 个的 pre 跑了 post 一定跑;pre 短路只有前 i 个的 post 逆序跑。

## HTTP 层钩子

`HTTPTransportPreHook(ctx, req) (*HTTPResponse, error)`:都 nil 继续,给响应短路,给 error 变错误响应。`HTTPTransportPostHook` 返回 error 短路,**不在流式响应上调用**。`HTTPTransportStreamChunkHook` 四种:改 chunk / `(nil,nil)` 丢这片 / `(chunk,err)` 记警告继续 / `(nil,err)` 断流。收的是可序列化的 `HTTPRequest{Method,Path,Headers,Query,Body,PathParams}`,不是 fasthttp 对象,为了 .so 和 WASM 都能用。

## 三条交付路

| 路 | 做法 | 适合 |
|---|---|---|
| 内置 | `server/plugins.go` `loadBuiltinPlugin` 加 case,调 `xxx.Init(ctx, config, logger, store…)`,store 由 server 注入 | 上游自己的 11 个;你改这里 = 改上游文件 |
| 动态 .so | 包级函数 `GetName`/`Cleanup` 必须,`Init(config any)` 可选,钩子函数有哪个挂哪个;`go build -buildmode=plugin`;config.json 写 `path`;loader 按符号名查(soloader.go) | 上游文档主推。硬约束:**同 Go 版本、同依赖版本、同平台,CGO 开** |
| **包壳里注册** | `ee/` main 里 new 一个实现接口的对象,Bootstrap 后调 `s.SyncLoadedPlugin(ctx, name, plugin, placement, order)`(server.go:1912):注册进 Config、排序、同步 core、更新状态 | **该用的**:不碰上游文件,不受 .so 版本约束,和包壳一起编译 |

WASM:`plugin_wasm.go` 只是 build tag 下的类型,OSS transports 无 WASM 加载器。

顺序:`PluginConfig{enabled, name, path, version, config, placement(pre_builtin/post_builtin), order}`,内置固定在中间。内容安全插件放 `pre_builtin`;排治理之前则违规不扣预算,之后则没预算的不浪费检测费,产品决定。

## 模板

`examples/plugins/llm-only/main.go` 147 行(.so 形状):包级 `Init` 从 map 取配置,`PreLLMHook` 往 messages 前插 system 消息、往 ctx 存值,`PostLLMHook` 取回、读 usage。`multi-interface` 是三种接口同时实现的。改成包壳注册只需把包级函数收进 struct 方法。

## 内容安全插件的形状

实现 `LLMPlugin`;`PreRequestHook` 返 nil;`PreLLMHook` 取 `req.ChatRequest.Input` 最后一条 user 内容调检测,违规返 `ShortCircuit{Error: &BifrostError{StatusCode: 200, AllowFallbacks: &false, Error: {Message: 拒答}}}`;`PostLLMHook` 取 `resp.ChatResponse.Choices[0]` 检测,违规换成拒答;检测超时两钩子都放行记日志(fail-open)。第三条路注册,`pre_builtin`。流式后加 chunk hook。

接口简单是因为难的事(翻译、流式拆帧、fallback、热更新)都在接口两边做完了。动手会碰到的:流式攒缓冲与替换、检测失败的产品决定与 error 计数、规则存储/页面/热更新(管理面那一半)、拒答在各方言下的形状与 finish_reason、外部检测服务的 mock 与集成测试。

下一步:第 13 步 `server/plugins.go` + governance 插件——"规则从哪来、怎么热更新、怎么落库"的范本。
