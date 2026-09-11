# 应用装配

本包连接业务服务、存储与 Bifrost 宿主，不承担运行期业务规则。

| 文件 | 职责 |
|---|---|
| [bootstrap.go](bootstrap.go) | 上游 Bootstrap → attach：品牌迁移、注入品牌存储、注册路由 |

本包创建品牌 Store 并直接注入 HTTP Handler，图片校验由品牌根包的函数负责。品牌见 [branding](../branding/README.md)。双方复用同一个 Server、Router 和数据库连接；本包不重复创建或关闭共享资源。
