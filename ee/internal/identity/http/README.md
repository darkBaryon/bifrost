# 身份 HTTP 接口

把 `identity.Services` 翻译成 HTTP：路由表、JSON、Cookie、同源检查、错误码。不含业务判断，不碰数据库。

## 做什么

- 路由表是唯一来源，`RegisterRoutes` 注册、`OwnsRoute` 供宿主判断"这条路由是不是你的"；旧 `/api/session/*` 四条指向同样的端点。路由归属、所有 POST 的来源拒绝与匿名/会话边界的契约测试在 [host/auth_test.go](../../host/auth_test.go) 的 `TestIdentityRouteContract`（另一个包），改路由表要一并跑。
- 每个端点外套一层 `serve`：禁缓存、非 GET 查同源、非匿名端点验 Cookie；端点本身只做"解码 → 调服务 → 装响应"。
- JSON 严格：拒绝未知字段、重复键、过深嵌套、超 16KiB；请求与响应结构（DTO）和业务类型分开定义。宿主可通过 `DecodeJSONObject` 复用严格对象解码，空正文兼容只保留给已声明的旧会话接口。
- 7 个业务错误映射 7 个状态码，其余一律 503；限流附 `Retry-After`。
- token 只经 `Set-Cookie` 交付（HttpOnly、SameSite=Lax、HTTPS 时 Secure），响应体不含 token。

## 不做什么

账号规则在 [identity](../README.md)；宿主中间件与 `IsLocalAdmin` 标记在 [host](../../host/README.md)。

## 文件

| 文件 | 内容 |
|---|---|
| [handler.go](handler.go) | 路由表、`serve`、`OwnsRoute`、`RegisterRoutes` |
| [session.go](session.go) / [accounts.go](accounts.go) / [passwords.go](passwords.go) | 会话、账号（含删除）和密码端点 |
| [protocol.go](protocol.go) | origin 校验、同源检查、Cookie、严格 JSON、错误映射 |
| [dto.go](dto.go) | 请求/响应结构与转换，按共同/会话/账号/密码分组；账号的 last_login_at 可空 |
| [handler_test.go](handler_test.go) / [dto_test.go](dto_test.go) / [decode_test.go](decode_test.go) | 错误映射与 JSON 限制契约；账号与状态响应键集合 |
