# core/schemas/bifrost.go:BifrostRequest / BifrostResponse / RequestType

core 对外所有方法的入参出参归结到三个类型加一个流式类型:`BifrostRequest`、`BifrostResponse`、`BifrostError`、`BifrostStreamChunk`。一个思想:**用"只填一格"的信封包住 50 种具体请求,让编排层和插件只面对一种类型,让 provider 只面对具体类型。**

## 信封

```go
type BifrostRequest struct {
    RequestType      RequestType               // 标签:装的是哪种
    ChatRequest      *BifrostChatRequest       // 50 个指针字段,每次只填一格,其余 nil
    EmbeddingRequest *BifrostEmbeddingRequest
    ...
}
```

`BifrostResponse` 同构,48 格。`BifrostStreamChunk` 是流式信封,嵌入 8 个指针(含 `*BifrostError`),channel 里每个元素要么一片响应要么一个错。`BifrostError` 是结构化错误:状态码、`IsBifrostError`(网关的错还是上游的错)、两个给插件的旋钮 `AllowFallbacks`(这个错要不要 fallback)和 `StreamControl`。

信封的生命:`makeChatCompletionRequest` 从 `sync.Pool` 取一个装进去(注释:用完归池,goroutine 里别留引用)→ `handleRequest`/`tryRequest`/队列/插件全程只认信封 → `handleProviderRequest` 按 RequestType 拆开调 provider 具体方法。

## 类型归属(从 handler 到 provider)

| 归属 | 类型 | 活在哪 |
|---|---|---|
| transports | `handlers.ChatRequest` | 只在 handler 解析阶段,转完就丢 |
| core/schemas 公开 | `BifrostChatRequest`、`BifrostChatResponse` | handler 造/收,provider 拆到 |
| core/schemas 公开 | `BifrostRequest`、`BifrostResponse` | 信封,只在编排层和插件间流转,handler 看不到 |
| core/bifrost.go 私有 | `ChannelMessage` | 队列两头 |
| core/providers/x 私有 | 各家 wire 格式 | 适配器内部 |

transports 只交出 `BifrostChatRequest`,后面的装拆全在 core。响应方向 transports 没有自己的 DTO,直接序列化 core 的响应类型——原生格式就是 OpenAI 格式的代码体现。

## 为什么 50 种

镜像 OpenAI 全部 API 面 + 几家特色:聊天/文本/Responses(7,有状态所以有查删取消)/Embedding/Rerank(Cohere)/OCR(Mistral)/语音 2/图片 3/视频 6(Sora 异步任务)/批处理 6/文件 5/缓存内容 5(Gemini)/容器 9(代码解释器沙箱)/透传。加 9 种流式变体 = 68 个 `RequestType` 常量。

真推理的十来种;三十多种是**周边资源的 CRUD 代理**(批处理是把一万次调用打包成任务,任务要建查取消取结果)。全接的原因:"改 base_url 其他不动"——漏一个接口用户就绕过网关,治理计费日志全失效。

## 为什么 50 种流程能一样

网关处理的是信封不是内容:验 VK、查预算、选 provider 和 key、排队、重试换 key、fallback、记日志,没有一条要打开信封看是提示词还是视频参数(快递员不读信)。量出来:`handleRequest` 135 行、`tryRequest` 226 行,按类型分支 **0 个**。

差异被推到两头:

```
handler          吸收 HTTP 编码差异      JSON / multipart / 二进制 / SSE(chat 35 行,fileUpload 127 行,imageEdit prepare 154 行)
core 中段        无差异                  只看信封
handleProviderRequest   唯一分发点        50 个 case,每个三行:拆信封、调 provider 方法、装响应信封
provider 适配器   吸收上游格式差异        Provider 接口约 50 个方法,每家各实现一遍(第 11 步)
```

其他差异用数据而非分支表达:计费单位按 RequestType 查定价表不同列;视频"查任务"这类请求信封上没有 fallbacks,编排层看到空列表就不换;流式只 9 种走 `handleStreamRequest`。

## 这个设计丑在哪,为什么

Go 没有 sum type,"这几种之一"只有三条路:N 指针 union(现状)、接口(地道)、`any`。信封方案的税:5 个辅助方法各 14 到 50 分支 switch(`GetRequestFields` 50、`SetProvider` 29…),加一种请求改十几处,switch 漏分支编译照过(Rust enum 会报错,C++ 是 `std::variant` + `visit`)。接口方案 + 嵌入 `RequestBase` 写一次六个方法,代码更少不是更多;插件里 `req.(*BifrostChatRequest)` 换 `req.ChatRequest != nil`,一行换一行。

**为什么选了它**:历史。core/v1.0.0(2025-04)`BifrostRequest` 是扁平结构,`Input` 是两指针 union(文本/聊天)——两种时这是最简单正确的写法。v1.2.0 加到 6 种时第一次面临选择,改接口要动所有已发布插件签名,扩指针只要加四行,选了后者。之后 9→19→31→40→47→51,每次局部成本都低,没有任何一次值得停下重构,长到公开 SDK API + 插件 API 就改不动了。**不是 50 种时选了丑方案,是 2 种时选了当时对的方案,然后没有任何一个时刻重新选。** 补救:switch 关在 5 个辅助方法里,调用方不自己写。

**教训**:底层"这几种之一"按集合会不会长判断,不按现在几种;确定会长的用接口;底层类型少导出字段(50 个字段全导出、插件直接摸,表面大才是牵涉面广的根源)。Go 谚语:不要用接口去设计,去发现接口——不矛盾,判断依据是确定会长而非"万一"。

## 上游改 API 怎么办

大多数改动只碰 `core/providers/<vendor>/`,中间格式稳定两头吸收;加字段几乎零成本(`knownFields` 白名单 + `ExtraParams` 透传,不认识的参数原样转发);改语义才改适配器。例外:内部格式就是 OpenAI 格式,OpenAI 改等于改中间格式(Responses API 带来 7 种类型 + 3867 行);各家特色字段(`Speed`、`InferenceGeo` 是 Anthropic 的,`SearchResults` 是 Perplexity 的)会渗进公共响应类型,熵增不可避免。这是网关生意的全部维护成本,上游每周追 API,fork 不合并就落后。

## 顺带

`BifrostContextKey` 165 个(Governance 21、Large 15、Skip 7…),全定义在 core:ctx 是插件和 core 之间的公共总线,键名就是协议。`ExtraFields` 是响应的元数据槽(实际 provider/key、是否 fallback、延迟、chunk 序号),`RoutingInfo.ServerSideFallbackModel` 由 provider 写而非编排层写(只有 provider 能看到上游内部换了模型)。用到时再看。

下一步:第 8 步 `core/bifrost.go:70` Bifrost 主结构体 + Init。
