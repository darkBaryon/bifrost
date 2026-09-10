# 应用装配

本包连接业务服务、存储与 Bifrost 宿主，不承担运行期业务规则。

| 文件 | 职责 |
|---|---|
| [bootstrap.go](bootstrap.go) | 上游 Bootstrap → attach：品牌迁移、注入品牌服务、注册路由 |

品牌依赖为 `HTTP → Service → Repository`，由本包创建具体 Store 并注入。品牌见 [branding](../branding/README.md)。双方复用同一个 Server、Router 和数据库连接；本包不重复创建或关闭共享资源。

## 账号认证接入

bootstrap.go在宿主注册路由前注入ConsoleAuthFactory，迁移并装配identity；品牌写入使用相同EE账号认证。recovery.go提供离线recover-admin子命令，密码仅从隐藏终端/stdin读取，要求操作者预先停止全部实例。
