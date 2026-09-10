# 品牌 HTTP 接口

本包解析请求、调用图片校验和 Store，再构造响应。Store 由 [app](../../app/README.md) 注入，HTTP 不直接操作 GORM。

| 文件 | 职责 |
|---|---|
| [handler.go](handler.go) | 路由、设置查询/保存/重置、JSON 字段语义及错误响应 |
| [assets.go](assets.go) | 当前版本图片、MIME、ETag 与缓存 |
| [branding_test.go](branding_test.go) | 真实鉴权、原子拒绝、故障、三态、校验顺序和图片缓存 |

POST /api/branding/get 与 GET /api/branding/assets/{slot}/{hash} 公开；POST /api/branding/update、POST /api/branding/reset 复用上游管理端鉴权，保留其开关与白名单行为。字段和限制见 [功能指南](../../../../product/docs-zh/04-开发指南/08-Logo品牌设置.md)。
