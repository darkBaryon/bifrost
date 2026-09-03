# handlers/inference.go:chatCompletion 一条完整 handler 链

推理 handler 只做一件事:把 HTTP 世界翻译成 core 的世界,再翻译回来。`chatCompletion` 本体 35 行(:1010-1044),是 transports 和 core 的交接点。

## 五步骨架

```
① 解析 body   prepareChatCompletionRequest   → *schemas.BifrostChatRequest
② 转换上下文  lib.ConvertToBifrostContext    fasthttp ctx → *schemas.BifrostContext
③ 分叉        stream ? SSE 路径 : 同步路径
④ 调 core     h.client.ChatCompletionRequest(bifrostCtx, req)   ← 第二阶段从这行的另一侧开始
⑤ 翻译响应    ApplyBifrostResponseHeaders + SendJSON / SendBifrostError
```

薄的原因:三块重活抽成了 43 个推理方法共用的部件——泛型 `prepareRequest[T]`、540 行的 `ConvertToBifrostContext`、260 行的 `handleStreamingResponse`。`textCompletion`、`responses`、`embeddings` 和它几乎一样,只换 ① 的类型和 ④ 的方法名。"推理 handler 重是协议面大但骨架共用"在此验证。

## ① 解析(:973-1009)

`prepareRequest[ChatRequest]`:反序列化;从 `model` 字段解析 provider 和模型名(`openai/gpt-4o`);解析 `fallbacks`;按 `chatParamsKnownFields` 白名单把**不认识的字段**抠进 `ExtraParams`。类型特有部分只剩校验 messages 非空、老客户端 `max_tokens` 映射到 `max_completion_tokens`。

Bifrost 原生格式**就是** OpenAI 格式,这一步几乎一比一搬字段。真正的格式翻译在两处:请求进来走 `/anthropic/...` 时 integrations 层翻成原生;出去时 core/providers 翻成上游各家。

## ② 上下文转换(lib/ctx.go:172-716)

整条链最大的一块:把 header 翻译成 ctx 键,共设 28 个键。

| 类 | header | ctx 键 |
|---|---|---|
| 身份 | `x-bf-vk` `x-api-key` `authorization` `x-bf-direct-key` | VirtualKey、APIKeyID、DirectKey |
| 追踪 | `x-request-id` `x-bf-parent-request-id` `x-bf-session-id` `baggage` | RequestID、ParentRequestID、SessionID |
| 标签 | `x-bf-dim-*` `x-bf-maxim-*` `x-bf-prom-*` | Dimensions 等;prom 只给 Prometheus 插件读 |
| 透传 | `x-bf-eh-*` `x-bf-mcp-*` | ExtraHeaders、MCPExtraHeaders,过白名单 |
| 行为开关 | `x-bf-cache-*` `x-bf-compat` `x-bf-disable-content-logging` `x-bf-send-back-raw-*` | 对应开关键 |

写死的安全黑名单(cookie、host、proxy-authorization、各 api-key 头)无论白名单怎么配都不透传。**插件通过 ctx 影响 core 的源头在这里**:治理读 VirtualKey、缓存读 cache 键。Tracing 中间件已建过 BifrostContext 就复用,一个请求只有一个。

## ⑤ 翻译响应

成功:透传 provider 响应头,加 `x-bf-*` 响应头标明实际 provider、模型、路由;`SendJSON`。失败:`SendBifrostError` 先 `SanitizeBifrostErrorForClient` 脱敏再映射状态码。`streamLargeResponseIfActive` 是企业版大响应插座,OSS 永远 false。

## 流式为什么 260 行(:1904-2164)

所有流式请求都过这一个函数,注释里每条都是真实 bug:

- SSE 响应头要等 core 成功返回 channel **之后**再设,否则上游 401 时客户端收到 200 加 SSE 头
- 自己写 `SSEStreamReader` 绕过 fasthttp 管道,后者会把多个事件攒成一个 TCP 段
- 定时心跳:断线只能靠写失败发现,上游一口气发完就没有第二次写的机会,心跳制造写的机会
- fasthttp 回收 `RequestCtx`,handler 返回后 goroutine 不能再碰它,要提前拷出
- post-hook 完成回调和流结束有竞态,`atomic.Value` + 100ms 有界等待
- 发过 error 帧就不再发 `[DONE]`

第二阶段读 core 流式时这些已解决,core 只管往 channel 塞 chunk。

## 第一阶段结束

装配链闭合:main 起 server → server 装 Config 和 Client → Config 经 Account 喂 core → server 把 handler 挂上三张路由表 → 请求进 handler 五步翻译交给 core。六个问题的答案全在第 ④ 步那行调用的另一侧。

下一步:第 7 步 `core/schemas/bifrost.go`,BifrostRequest / BifrostResponse / RequestType 数据模型。
