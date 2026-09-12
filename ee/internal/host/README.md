# 宿主认证接入

把 EE 的账号认证接到上游 Bifrost 上，替换上游自带的共享密码认证。实现上游定义的 `server.ConsoleAuthProvider` 接口。

## 做什么

- 管理接口的鉴权中间件：验 `ee_session` Cookie，只放行主管理员；通过后按上游规矩写 `is_local_admin` 标记，上游的通知、WebSocket 功能靠它判断权限。本包只通过两个自定义的窄接口拿会话线与旧管理员导入能力，建号、重置、恢复对它不可见。
- 接管旧的 `/api/session/*` 路由，前端不用改。
- WebSocket 连接每次推送前重新验证会话，撤销即断开。
- `GET /api/config` 把旧认证字段替换成只读投影；`PUT` 拒绝对它的修改。
- 启动时把上游旧配置里的管理员导入为主管理员（只在首次）。

## 不做什么

账号规则在 [identity](../identity/README.md)，装配和环境变量在 [app](../app/README.md)，推理链的鉴权完全是上游的。

## 文件

| 文件 | 内容 |
|---|---|
| [auth.go](auth.go) | 中间件、会话路由、WebSocket 重验、旧管理员导入 |
| [config.go](config.go) | `/api/config` 的读投影与写预检 |
| [auth_test.go](auth_test.go) | 真实路由链上的鉴权边界测试 |
