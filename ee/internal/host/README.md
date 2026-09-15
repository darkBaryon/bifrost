# 宿主认证接入

把 EE 的账号认证接到上游 Bifrost 上，替换上游自带的共享密码认证。实现上游定义的 `server.ConsoleAuthProvider` 接口。

## 做什么

- 管理接口的鉴权中间件：验 `ee_session` Cookie，通过注入的权限组件检查已接入接口，其余仍只放行初始化管理员。已有通知、WebSocket入口保留管理员标记和会话重验。Users.Manage只用于账号与角色管理，不能据此放行其他功能。
- 接管旧的 `/api/session/*` 路由，并注册角色接口；自管接口分别执行认证和业务判权。
- WebSocket 连接每次推送前重新验证会话，撤销即断开。
- `GET /api/config` 把旧认证字段替换成管理员名字和隐藏后的密码；`PUT` 拒绝对它的修改。
- 启动时把上游旧配置里的管理员导入为主管理员（只在首次）。

## 不做什么

账号规则在 [identity](../identity/README.md)，装配和环境变量在 [app](../app/README.md)，推理链的鉴权完全是上游的。

## 文件

| 文件 | 内容 |
|---|---|
| [auth.go](auth.go) | 中间件、会话路由、WebSocket 重验、旧管理员导入 |
| [config.go](config.go) | `/api/config` 的认证字段输出和保存前检查 |
| [auth_test.go](auth_test.go)、[rbac_test.go](rbac_test.go)、[permissions_test.go](permissions_test.go) | 真实路由链的鉴权边界，以及身份或权限查询失败时不能改用旧规则放行 |
