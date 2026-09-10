# 品牌 HTTP 接口

本包负责 JSON 字段解析、路由、鉴权接入与响应。由 [app](../../app/README.md) 注入品牌 Service，依赖业务根包，不持有具体 Store 或数据库连接。

| 方法与路径 | 用途 |
|---|---|
| POST /api/branding/get | 公开读取设置，供登录页使用 |
| POST /api/branding/update | 保存指定图片，经过上游管理端鉴权 |
| POST /api/branding/reset | 清空两张图片，经过同一鉴权 |
| GET /api/branding/assets/{slot}/{hash} | 公开读取当前版本；旧版本 404，缓存验证可返回 304 |

写接口沿用上游鉴权的开关与白名单行为。字段、限制和错误码见 [功能指南](../../../../product/docs-zh/04-开发指南/08-Logo品牌设置.md)。

| 文件 | 职责 |
|---|---|
| [handler.go](handler.go) | 注入业务服务并注册路由 |
| [settings.go](settings.go) | 请求体保护、JSON 解析、服务调用、响应及错误映射 |
| [validation.go](validation.go) | 保留缺字段/空串/null 语义，调用业务图片构造函数 |
| [asset.go](asset.go) | 调用服务选择当前图片，处理 MIME、ETag 与缓存 |
| [branding_test.go](branding_test.go) | 验证路由、真实鉴权、原子拒绝、故障、三态与校验顺序 |

图片格式与内容规则位于 [业务包](../README.md)，本包只将错误映射为 HTTP 状态。
