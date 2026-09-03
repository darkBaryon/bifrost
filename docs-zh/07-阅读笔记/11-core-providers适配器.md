# core/providers:Provider 接口与适配器

适配器层回答一个问题:**Bifrost 内部格式怎么变成这家上游的格式,再变回来。** 契约是 `schemas.Provider` 接口(core/schemas/provider.go:631-748),55 个方法 = 50 种请求 + 9 个流式变体 + GetProviderKey + Passthrough——"信封有几格,接口就有几个方法",`handleProviderRequest` 的 50 个 case 在两份清单间映射。工厂 `createBaseProvider` 按名字 switch 出 `NewXxxProvider`。

## 为什么 Bifrost 格式 = OpenAI 格式

`BifrostChatResponse` 的 json tag 是 `id/choices/created/model/object/system_fingerprint/usage`,`ChatParameters` 是 `frequency_penalty/max_completion_tokens/response_format/service_tier…`,逐字照 OpenAI。理由:事实标准,所有 SDK 说这套话;只翻译少数派(Anthropic/Gemini/Bedrock/Cohere);历史起点就是 OpenAI。代价:是超集,各家特色字段渗入(`Speed`/`InferenceGeo` 是 Anthropic 的,`SearchResults` 是 Perplexity 的);OpenAI 改 API = 改中间格式。

## 三种方言,三个底座

Anthropic 和 OpenAI **不兼容**:system 单拎 vs 放 messages;content 是 block 数组(text/image/tool_use/tool_result/thinking)vs 字符串;工具调用是 block 对 vs tool_calls+role=tool;流式是事件类型 vs delta。两边互开残缺的 beta 兼容端点。市场格局:OpenAI 格式人人兼容,Anthropic 格式看 agent 流量(Claude Code 2025 爆发后国内厂商三个月内全加了 `/anthropic/v1/messages`),Gemini 格式只有 Google。

按 import 关系,29 家分层:

```
providers/utils                方言无关:MakeRequestWithContext、EnrichError、代理/TLS/拨号、流式取消超时   一份
openai / anthropic / gemini    三种方言各一份"六步往返 + 格式转换"(底座)                          三份
其余 26 家                     域名 + 鉴权 + 怪癖,借底座                                        各几百行
```

借用:openai 底座 20 家;anthropic 底座 bedrock/vertex/cohere/deepseek/fireworks/azure/vllm/sgl;gemini 底座只 vertex。bedrock、vertex 三个都借,自己写的是 SigV4/GCP OAuth 和独有模型。

"兼容 OpenAI"是数据形状相同,底座是"怎么送过去拿回来":序列化、组请求、发送计时、错误翻译、解析填 ExtraFields、流式 SSE 解析——每端点一份,20 家共用。openai.go 7641 行里 24 个 `HandleOpenAI*` 占 4421 行,大在广度(每个端点)不在翻译;anthropic 22k 才是翻译量大(`ToAnthropicChatRequest` 774 行、`ToBifrostChatResponse` 257 行)。

## 薄适配器怎么写(deepseek.go 为样板)

**公式:struct 存连接配置 → 构造函数建两个 HTTP 客户端 → 每个方法 = 怪癖处理 + 一次委托;不支持的一行报错。**

- struct 六个字段全是"怎么发 HTTP":logger、`client`(有 ReadTimeout)、`streamingClient`(无 ReadTimeout,空闲超时另管;开发指南"流式一律用 streamingClient 否则 30s 被掐")、networkConfig、两个 raw 开关。无状态,N 个 worker 并发用。
- 构造:`CheckAndSetDefaults` → 建 fasthttp 客户端(超时/连接数/保活,每家独立连接池)→ `ConfigureProxy/ConfigureDialer(AllowPrivateNetwork,SSRF 落地处)/ConfigureTLS` → `BuildStreamingClient` 克隆去掉 ReadTimeout → 默认域名。
- 55 个方法三堆:6 个真实现(ListModels/Text/Chat/Responses 及流式)、45 个 `NewUnsupportedOperationError`、1 个 GetProviderKey。618 行里有内容的不到 200。
- `ChatCompletion`:key 上 `UseAnthropicEndpoints` 开了就整个委托 anthropic 包(鉴权头换 x-api-key);否则设 PassthroughExtraParams、`applyDeepSeekThinkingCompatibility`、委托 `openai.HandleOpenAIChatCompletionRequest(ctx, client, url, request, BearerAuthHeader(key), extraHeaders, raw×2, providerName, 三个定制钩子 nil, logger)`。13 个参数四类:怎么连(适配器的全部专有知识)、请求本身、我是谁(共享函数按名删减)、定制钩子(响应解析/错误转换/签名,多数 nil)。
- 怪癖 `applyDeepSeekThinkingCompatibility`:tool_choice 强制或历史有 tool_call 却无 reasoning 时关思考(issue #5887),**不改原请求**——拷贝 Params/ExtraParams/request,因为指针和调用方及 fallback 共享(第 9 步的浅拷贝坑的正面处理)。四十行注释配一个怪癖是常态。
- 流式版三处不同:传 streamingClient、多传 StreamIdleTimeout/postHookRunner/postHookSpanFinalizer、返回 channel。共享函数起 goroutine 逐行读 SSE,每个 data: 转 chunk 调插件塞 channel。

厚适配器差在第 1 步(转请求)和第 6 步(转响应)换成真翻译,中间四步同。转换函数必须纯函数(测试覆盖 1800 行才能这样测),要联网的 `ResolveChatFileURLs` 拿到外面。

## 配置 vs 代码的边界

**配置能表达数据,表达不了行为。** 自定义 provider(docs/providers/custom-providers.mdx)四个字段:`base_provider_type`(7 个可选底座)、`allowed_requests`、`request_path_overrides`、`is_key_less`。机制:`createBaseProvider` 看到 CustomProviderConfig 就把目标换成 BaseProviderType 走对应 New,只换域名。

必须是代码的约 12 家:三个方言底座;签名鉴权(bedrock SigV4、vertex OAuth、azure deployment URL);异步任务模型(replicate、runway);有条件的怪癖(deepseek、xai、fireworks);独有端点(cohere rerank、elevenlabs、mistral OCR)。其余十几个薄包(groq、cerebras、parasail…)没有一行配置写不出来,存在是历史 + 控制台一等公民(图标/默认模型/定价)+ 类型常量,更合理的设计是内置预设配置。

**接国内厂商决策树**:怪癖能用配置表达?能就一条 JSON(Kimi/智谱/MiniMax/Qwen);不能就抄 deepseek.go 写薄包。优先走厂商的 OpenAI 端点,Anthropic 端点只在用户拿 Claude Code 类工具时开且预期怪癖多。永远不会写第四种方言。

## 找怪癖:测试名当索引

`grep "^func Test.*Vertex" core/providers/openai/*_test.go` 列出的就是对某家的全部特殊处理清单(`TestToOpenAIChatRequest_VertexDropsNoneReasoningEffort`…),每个测试带输入输出样例,且被 CI 保证正确。比读 `ToOpenAIChatRequest` 那个 switch 快。anthropic/gemini 同理。

细节未读:anthropic 774 行怎么处理 content block、gemini 翻译、providers/utils 的重试和代理。README 支线"翻译层剖析"即此,需要时按测试名索引进。

第二阶段结束。下一步:第 12 步 `core/schemas/plugin.go` 插件接口(写代码的地方)。
