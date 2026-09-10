# 身份HTTP接口

`handler.go`集中维护本模块HTTP适配：路由声明、严格JSON解码、同源检查、Cookie、安全错误映射和业务调用。账号规则通过identity.Service执行，不在handler中操作数据库。

- 路由表同时供OwnsRoute和RegisterRoutes使用；每个端点显式绑定类型化operation、匿名能力及兼容空体规则，默认要求会话。路径仅在此声明，不按路径前缀推断业务行为。
- HTTP状态码/方法和删除Cookie的过期值复用fasthttp；请求体字节数、JSON深度就近命名；Retry-After秒数从identity.LoginRateWindow推导，与数据库限流共用口径。
- 命名响应类型固定字段契约，兼容成功消息集中定义；未知内部操作明确失败，不返回成功空响应。
- `handler_test.go`验证精确路由归属、所有POST的来源拒绝、匿名/会话边界、严格JSON限制、错误映射和序列化降级。业务全流程继续由宿主适配测试及真实HTTP冒烟覆盖。

新接口统一POST，保留上游会话兼容路径及旧登出/票据的空体能力。GET is-auth-enabled维持公开状态查询。Cookie绑定已验证的外部origin，非回环部署必须HTTPS；不信任代理头，不把会话token返回到JSON。
