# core/bifrost.go:handleRequest → tryRequest,fallback 编排(骨架级)

## 两层循环

```
handleRequest (每请求一次, :5089)
  ├─ PreRequestHook        插件决定 provider/model/fallbacks,只跑一次,改动写进信封
  ├─ tryRequest(主)
  └─ for 每个 fallback:
        prepareFallbackRequest   浅拷贝信封,换 provider 和 model
        tryRequest(fallback)

tryRequest (每次尝试一次, :5372)
  ├─ RunLLMPreHooks        插件逐个跑,可短路
  ├─ 进 provider 队列,等 worker 回信
  └─ RunLLMPostHooks       逆序,只跑那些 pre 跑过的插件
```

插件接口注释(core/schemas/plugin.go:165-206)分"每请求阶段"(HTTPTransport pre/post、PreRequestHook)和"每尝试阶段"(PreLLMHook、PostLLMHook)。**换家重试时治理、缓存这些 pre-hook 会再跑一遍,路由决策不会。** 写插件前必须知道:hook 可能被调 1 + fallback 数次。

## 三个判断函数 = fallback 全部规则

- `shouldTryFallbacks`(:4943):没出错不换;错误类型 `RequestCancelled` 不换;`AllowFallbacks == &false` 不换(三态,nil 当 true);没配 fallbacks 不换。全过就换。
- `shouldContinueWithFallbacks`(:5071):只看取消和 AllowFallbacks。
- `prepareFallbackRequest`(:4976):先确认 Account 有这个 provider 的配置,没有就跳过;**浅拷贝**信封,换具体请求的 Provider/Model。只对 12 种类型做替换(聊天、文本、Responses、Embedding、Rerank、OCR、语音、转写、图片/视频生成等),其余 38 种不在列表里 = 不支持 fallback(批处理查状态、文件下载这类带上游 ID 的换家没意义,代码里就没写)。

## 短路:插件替代上游的两条路

`LLMPluginShortCircuit`(plugin_native.go:21)三选一:

| 设哪个 | 谁在用 | 之后 |
|---|---|---|
| `Response` | 语义缓存命中(semanticcache/search.go:345) | 跳过队列,对该响应跑 post-hook,返回成功 |
| `Error` | 治理拒绝(预算、限流、VK 无效) | 跳过队列,对错误跑 post-hook,走 shouldTryFallbacks |
| `Stream` | 缓存的流式版本 | 同 Response |

对称性:`RunLLMPreHooks` 返回 `executedPreHooks` 计数,第 i 个插件短路则 post 只跑前 i 个的逆序。先进后出。

## 两个观察(和自己写插件直接相关)

1. **治理拒绝没设 `AllowFallbacks=false`**(搜遍 governance 插件无一处)。后果:VK 没预算被拒后 core 去试每个 fallback,每次治理再拒一次,结果正确但白跑 N 次管线。内容安全插件拦截时**要设** `AllowFallbacks: &false`,违规请求换模型一样违规。
2. **PreLLMHook 对信封的修改能否带到 fallback,注释自认 "incidental"**:浅拷贝共享指针,改 `Input` 切片元素会带过去,改 `Model` 字段不会。想改动对所有尝试生效用 `PreRequestHook`;想每次尝试独立判断用 `PreLLMHook`;别依赖浅拷贝的巧合。

## 插件的口子与边界

口子:HTTPTransport pre/post(每请求)、PreRequestHook(每请求,路由)、PreLLMHook/PostLLMHook(每尝试)、StreamChunkHook(每 chunk)、MCP 各 hook、ObservabilityPlugin(挂 Tracer)、`KeySelector`/`KeyPoolFilter`(BifrostConfig 函数字段,只能从自己的 main 传)。覆盖十个决定里的 1/2/3/9/10 + 经 KeySelector 的 4。

没口子:队列/重试/fallback 循环的策略(5/7/8)、provider 适配器内部(6,加 header 可经 ctx ExtraHeaders)、控制面(加路由/页面/监听配置变更)、启动装配中间插步。

没口子时三条路:**包壳**(控制面全走这条,企业版做法)→ **给上游加小口子提 PR**(`SetLargePayloadHook`、`GovernanceRouteOverridesProvider` 都是他们为自家包壳加的,中性几十行大概率收)→ 直接改上游文件(最后手段,做成默认关闭的开关)。

**core 决定"一个请求怎么走",企业功能决定"谁能让请求走、走完记什么"**——后者全在请求前后,正好是包壳和插件够得到的位置。内容安全=PreLLM/PostLLM 插件;审计=包壳+API 中间件;RBAC/SSO=包壳替换 auth 中间件;集群=包壳覆盖回调+广播。

细节未读:`handleStreamRequest`(:5230)流式版本、错误脱敏、trace span 开合。需要时从 :5089 / :5230 进。

下一步:第 10 步 ProviderQueue + requestWorker,骨架级(只读 TOCTOU 注释和 key 池入口)。
