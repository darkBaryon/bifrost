# 身份 HTTP 接口

本包集中维护身份模块的 HTTP 适配：路由声明、严格 JSON 解码、同源检查、Cookie、安全错误映射和业务调用。账号规则通过 `identity.Service` 执行，不在这里操作数据库。

| 文件 | 职责 |
|---|---|
| [handler.go](handler.go) | 路由表（`OwnsRoute` 与 `RegisterRoutes` 共用）、端点共同的前置规则、同源与 Cookie、错误映射、各端点适配 |
| [dto.go](dto.go) | 请求与响应结构、业务类型到响应字段的转换 |
| [handler_test.go](handler_test.go) | 严格 JSON 限制、错误映射与序列化降级契约 |

每条路由直接绑定端点方法，并显式声明匿名能力与旧接口的空正文兼容；新增路由默认要求会话。精确路由归属、所有 POST 的来源拒绝及匿名/会话边界由 [../../bifrost/auth_test.go](../../bifrost/auth_test.go) 的 `TestIdentityRouteContract` 验证，业务全流程由宿主适配测试和真实 HTTP 冒烟覆盖。

新接口统一 POST，保留上游会话兼容路径及旧登出/票据的空正文能力；`GET /api/session/is-auth-enabled` 维持公开状态查询。部署 origin 由 `ValidateOrigin` 统一校验，非回环部署必须 HTTPS；不信任代理头，不把会话 token 放进 JSON。`limit` 省略取默认值，显式 0 或越界返回 400。
