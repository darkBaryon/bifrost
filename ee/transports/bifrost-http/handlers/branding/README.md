# 品牌 HTTP 接口

## 功能

提供品牌设置的查询、保存、恢复默认和图片读取接口，负责请求校验及 HTTP 响应。

## 架构

[服务启动装配](../../server/bootstrap.go)注入[品牌存储](../../../../framework/configstore/branding/README.md)和已有管理端鉴权中间件，再注册路由。

设置请求依次经过路由、设置处理、输入校验和存储调用，最后转换为 JSON 响应。图片请求直接读取存储中的当前版本，由图片处理器返回图片字节和缓存头。本包不自行创建数据库连接。

| 方法与路径 | 用途 |
|---|---|
| `POST /api/branding/get` | 公开读取设置，供登录页等未登录页面使用。 |
| `POST /api/branding/update` | 保存指定图片，经过已有管理端鉴权中间件。 |
| `POST /api/branding/reset` | 清空两份图片，经过同一鉴权中间件。 |
| `GET /api/branding/assets/{slot}/{hash}` | 公开读取当前 Logo 或小图标；旧版本返回 404，缓存验证可返回 304。 |

鉴权关闭或路径在已有白名单中时，写接口沿用中间件的放行行为。请求字段、上传限制和错误码详见[功能指南](../../../../../docs-zh/04-开发指南/08-Logo品牌设置.md)。

## 文件说明

| 文件 | 职责 |
|---|---|
| [handler.go](handler.go) | 定义处理器，接收依赖，注册设置和图片路由。 |
| [settings.go](settings.go) | 处理查询、保存和重置；限制请求体、解析 JSON，并转换响应。 |
| [validation.go](validation.go) | 区分保持、清除和替换；校验 Base64、图片格式、内容与大小和尺寸限制。 |
| [asset.go](asset.go) | 按图片位置和内容哈希读取当前图片，处理 MIME、ETag 和缓存响应。 |
| [branding_test.go](branding_test.go) | 准备测试数据库、路由和图片；验证路由方法、鉴权、设置操作、无效输入及图片缓存。 |
