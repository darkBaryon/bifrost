# 应用装配

本包连接业务服务、存储与 Bifrost 宿主，不承担运行期业务规则。

| 文件 | 职责 |
|---|---|
| [bootstrap.go](bootstrap.go) | 接收入口配置好的宿主 logger，注入身份装配工厂 → 上游 Bootstrap → attach：品牌迁移、注入品牌存储、注册路由 |
| [identity.go](identity.go) | 解析 `EE_*` 部署环境变量、迁移身份表、构造身份服务与宿主认证适配，并把宿主 logger 注入存储与适配 |
| [recovery.go](recovery.go) | 离线 `identity recover-admin` 子命令：打开现有配置库，用同一套选项构造服务并重置主管理员 |

身份依赖为 `HTTP → Service → Repository`，本包创建具体 Store 与 Handler 并注入 [bifrost](../bifrost/README.md) 的适配；品牌不设中转服务，本包创建 Store 并直接注入 HTTP Handler，图片校验由品牌根包的函数负责，见 [branding](../branding/README.md)。品牌写入路由与管理路由使用同一 EE 账号认证。双方复用同一个 Server、Router 和数据库连接；本包不重复创建或关闭共享资源。离线恢复的密码只从隐藏终端或 stdin 读取，要求操作者预先停止全部实例。
