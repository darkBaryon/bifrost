# 应用装配

把 EE 的业务服务、存储和宿主接入，装到上游那个内嵌的 HTTP 服务对象上（上游用它持有路由、配置与数据库连接）。本包只做构造与连线，不含运行期业务规则。

## 做什么

- `Bootstrap`：先埋一个身份装配工厂，上游在建好数据库、注册管理路由之前会回调它；再跑上游自己的装配；最后 `attach` 接入品牌路由（复用同一份 EE 鉴权中间件）并同步一次国内定价。
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
| [pricing.go](pricing.go) | 定价环境配置、启动同步与错误分类 |
| [pricing_test.go](pricing_test.go) | 配置顺序、目录缺失与 attach 失败策略测试 |

## 改之前要知道什么

- **装配顺序是有讲究的**。身份工厂必须在上游装配之前埋好，品牌与定价必须在上游装配之后、开始监听之前接入，否则要么工厂不被回调，要么写进去的数据被上游启动流程覆盖。
- **失败分两类**。部署配置错误直接返回错误拒绝启动；业务模块自身不可用（价格文件坏了、目录没装配）记日志后继续启动。各模块的具体分类见模块自己的 README。
- **共享资源不归本包**。Server、Router、数据库连接由上游创建与关闭，本包只借用。
