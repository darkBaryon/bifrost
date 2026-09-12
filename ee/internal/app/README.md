# 应用装配

把 EE 的业务服务、存储和宿主接入装到上游 `BifrostHTTPServer` 上。只做构造与连线，不含运行期业务规则。

## 做什么

- `Bootstrap`：先把身份装配工厂埋进 `ConsoleAuthFactory`，再跑上游 `Bootstrap`（它在建好数据库、注册路由前回调工厂），最后 `attach` 品牌路由并复用同一份 EE 鉴权中间件。
- `assembleIdentity`：读 `EE_*` 环境变量并校验、跑身份表迁移、`identity.New` 装配三条线、导入旧管理员、构造 HTTP 层和宿主适配。适配只拿会话线，导入只拿账号线。
- `RecoverAdmin`：离线 `identity recover-admin` 子命令，不起 HTTP，用同一套装配重置主管理员密码；密码从隐藏终端或 stdin 读。

## 不做什么

品牌图片校验在 [branding](../branding/README.md)；宿主中间件在 [host](../host/README.md)；共享的 Server、Router、数据库连接由上游创建与关闭。

## 文件

| 文件 | 内容 |
|---|---|
| [bootstrap.go](bootstrap.go) | `Bootstrap` 与 `attach` |
| [identity.go](identity.go) | 环境变量、`newIdentity`、`assembleIdentity` |
| [recovery.go](recovery.go) | 离线恢复子命令 |
