# 宿主认证接入

把 EE 的账号认证接到上游 Bifrost 管理接口上，替换上游自带的共享密码认证。应用装配层将本包的中间件接入宿主；身份、角色和基础状态接口分别注册，各自执行来源、认证和业务规则。

## 做什么

- 管理接口的鉴权中间件：认证服务验 `ee_session` Cookie，再由独立注入的权限组件检查管理接口；缺少组件时拒绝请求。通知和WebSocket按角色筛选，发送前重查权限。Users.Manage只用于账号与角色管理，不能据此放行其他功能。
- 注册基础状态接口。身份模块负责旧 `/api/session/*` 兼容入口，角色模块负责自己的接口；本包不再持有或注册其他模块的路由。
- `POST /api/console/bootstrap` 只需有效个人会话和合法来源，不要求设置权限；仅输出外壳基础状态，重启读取失败时省略附属字段并记录固定脱敏诊断。完整配置权限不变。
- WebSocket 连接每次推送前重新验证会话，撤销即断开。
- 票据签发、消费由本包的控制台入口组织认证、会话和权限调用；消费标记与身份、通知权限复验共用事务，任一步失败都回滚。
- `GET /api/config` 把旧认证字段替换成管理员名字和隐藏后的密码；`PUT` 拒绝对它的修改。
- 启动时把上游旧配置里的管理员导入为主管理员（只在首次）。

## 不做什么

账号规则在 [identity](../identity/README.md)，登录和身份验证在 [auth](../auth/README.md)，会话数据在 [session](../session/README.md)，装配和环境变量在 [app](../app/README.md)，推理链的鉴权完全是上游的。

## 文件

| 文件 | 内容 |
|---|---|
| [auth.go](auth.go) | BF 管理接口中间件、握手接入和旧管理员导入；认证、票据、权限分别注入，不承担模块路由分发 |
| [tickets.go](tickets.go) | 在同一事务组织票据操作、认证复验和通知权限检查；重试结束后记录带入口关联 ID 的安全故障日志 |
| [console.go](console.go) | 基础状态精确路由、会话与协议检查、白名单输出及重启读取降级 |
| [console_test.go](console_test.go)、[consoleprotocol_test.go](consoleprotocol_test.go)、[consoleprojection_test.go](consoleprojection_test.go)、[consolepermissions_test.go](consolepermissions_test.go) | 基础状态的授权、协议、字段与降级边界，以及真实身份/角色服务的配置权限对照 |
| [config.go](config.go) | `/api/config` 的认证字段输出和保存前检查 |
| [auth_test.go](auth_test.go)、[rbac_test.go](rbac_test.go)、[permissions_test.go](permissions_test.go) | 真实路由链的鉴权边界，以及身份或权限查询失败时不能改用旧规则放行 |
| [fixture_test.go](fixture_test.go)、[tickets_test.go](tickets_test.go) | 真实认证和权限测试装配，以及票据消费后权限撤回、会话失效的事务回滚 |
| [tickets_diagnostics_test.go](tickets_diagnostics_test.go) | 真实 SQLite 更新故障后的消费回滚、关联诊断和敏感信息不泄露 |

接口契约见[工作台基础信息接口](../../docs/工作台基础信息接口.md)。基础状态只接通用中间件，不接 BF 管理接口认证链，以保留无角色、需改密账号的个人会话访问。logger由app注入，不新增业务服务或独立数据库探测。Cookie/同源策略来自 auth/http，JSON 协议工具来自 httpx；不依赖账号 HTTP 包。
