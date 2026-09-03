# core/bifrost.go:ProviderQueue + requestWorker(骨架级,只记规则)

请求进队列之后的事:worker 取出、选 key、调上游、失败重试、按回信地址写回。这是 core 里最不会碰的部分,只记五条规则。

1. **队列满了默认等,可配丢。** `tryRequest`(:5484)先非阻塞试塞,满了看 `dropExcessRequests`:true 直接返回队列满错误;false 阻塞等,同时监听 `done`(provider 关了)和 `ctx.Done()`(客户端断了)。一家堵死只阻塞它的调用方,不拖别家 worker——故障隔离第二层。
2. **重试默认关,开了也只在同一家内。** `DefaultMaxRetries = 0`(schemas/provider.go:13),退避 500ms 起 5s 封顶指数增长,按 provider 配。重试=同家换 key 再来;fallback=换一家,两层。
3. **只有 per-key 错误才换 key。** `executeRequestWithRetries`(:5915,600 行):429/401/402/403 算"这个 key 的问题"下次换 key;5xx/网络算"上游的问题"同 key 退避重试;全试死返回 502 `upstream_credentials_exhausted`。退避只在"上次是 401/402/403 且这次真换到了不同 key"时跳过;429 换 key 照样退避(账号级限流各 key 共享额度)。每次尝试记进 ctx `AttemptTrail`,审计里"试了几次"的来源。
4. **写回带 5 秒超时和弃单计费。** `requestWorker` 尾部(:7007-7067)select 三路:写成功 / 客户端 ctx 已取消 / 5s 超时。客户端断线时 tryRequest 没人收,但上游已算 token,worker 调 `billAbandonedTerminal` 直接记账——不做预算就漏。
5. **provider 热更新先起新再退旧。** `UpdateProvider`(:3705-3904)八步:造新适配器(失败旧的继续)→ 新队列新 WaitGroup → CAS 换 providers 列表 → 发布新队列起新 worker → **把旧队列未取走的请求搬到新队列** → 登记退役等待 → 旧队列发 done → 异步等旧 worker 跑完在途后 drain(以前在锁里等,高负载下堵新请求)。Config 那招"停旧起新"的完整版,多了搬迁保证零丢失。

队列不 close(第 8 步)撑着规则 1 和 5:生产者 select 同时听 `queue <-` 和 `<-done`,worker 退出靠 `<-done`,残留靠 `drainQueueWithErrors`。

故障隔离到此三层:一家一队列一组 worker(结构)/ 满了只阻塞调用方(规则 1)/ per-key 换 key、上游退避、全死才放弃(规则 3)。

细节未读:重试函数里的 trace span、encrypted reasoning fail-soft、`calculateBackoff` jitter。需要时从 :5915 进。
