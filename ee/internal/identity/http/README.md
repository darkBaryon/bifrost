# 账号和密码 HTTP 接口

本包接收账号初始化、创建、删除、列表、状态更新，以及改密、重置密码、密码事件请求。登录、退出、本人资料、登录状态和票据入口在 [auth/http](../../auth/http/README.md)，由 app 分别注册。

| 文件 | 职责 |
|---|---|
| [handler.go](handler.go) | 八条账号与密码路由、同源和会话检查 |
| [accounts.go](accounts.go) | 初始化、创建、列表、启停和删除账号 |
| [passwords.go](passwords.go) | 本人改密、管理员重置、密码事件；改密成功清除 Cookie |
| [dto.go](dto.go) | 账号和密码的请求、响应及业务类型转换 |
| [protocol.go](protocol.go) | 账号错误到 HTTP 错误的转换、请求上下文 |
| [handler_test.go](handler_test.go)、[dto_test.go](dto_test.go) | 路由归属、安全错误映射、账号字段契约 |

接口路径保持既有契约。浏览器 Cookie 和同源规则由注入的 `auth/http.Browser` 统一提供，严格 JSON 解码和响应由 [httpx](../../httpx/README.md) 提供。本包不接受空正文，也不注册旧 `/api/session/*` 路径。账号规则仍在 [identity](../README.md)。
