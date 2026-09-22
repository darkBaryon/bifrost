# 宿主认证接入

把 EE 的账号认证接到上游 Bifrost 上，替换上游自带的共享密码认证。上游留了一个控制台认证接口，谁实现它谁就接管管理端登录状态的判定，本包实现的就是它。

## 做什么

- 管理接口的鉴权中间件：验 `ee_session` Cookie，通过注入的权限组件检查管理接口；没有注入时调用身份模块的管理策略（默认仅初始化管理员）。通知和WebSocket按角色筛选，发送前重查权限。Users.Manage只用于账号与角色管理，不能据此放行其他功能。
- 接管旧的 `/api/session/*` 路由，并注册角色和基础状态接口；自管接口分别执行认证和业务判权。
- `POST /api/console/bootstrap` 只需有效个人会话和合法来源，不要求设置权限；仅输出外壳基础状态，重启读取失败时省略附属字段并记录固定脱敏诊断。完整配置权限不变。
- WebSocket 连接每次推送前重新验证会话，撤销即断开。
- `GET /api/config` 把旧认证字段替换成管理员名字和隐藏后的密码；`PUT` 拒绝对它的修改。
- 启动时把上游旧配置里的管理员导入为主管理员（只在首次）。

## 不做什么

账号规则在 [identity](../identity/README.md)，装配和环境变量在 [app](../app/README.md)，推理链的鉴权完全是上游的。

## 文件

| 文件 | 内容 |
|---|---|
| [auth.go](auth.go) | 中间件、会话路由、WebSocket 重验、旧管理员导入 |
| [console.go](console.go) | 基础状态精确路由、会话与协议检查、白名单输出及重启读取降级 |
| [console_test.go](console_test.go)、[consoleprotocol_test.go](consoleprotocol_test.go)、[consoleprojection_test.go](consoleprojection_test.go)、[consolepermissions_test.go](consolepermissions_test.go) | 基础状态的授权、协议、字段与降级边界，以及真实身份/角色服务的配置权限对照 |
| [config.go](config.go) | `/api/config` 的认证字段输出和保存前检查 |
| [auth_test.go](auth_test.go)、[rbac_test.go](rbac_test.go)、[permissions_test.go](permissions_test.go) | 真实路由链的鉴权边界，以及身份或权限查询失败时不能改用旧规则放行 |

接口契约见[工作台基础信息接口](../../docs/工作台基础信息接口.md)。基础状态通过既有身份HTTP协议处理，logger由app注入，不新增业务服务或独立数据库探测。
