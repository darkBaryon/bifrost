# 品牌设置

本模块负责部署级 Logo 和小图标。保存流程是：HTTP 解析请求 → 校验两张图片 → Store 原子保存；不设中转 Service 或 Repository。

| 文件或目录 | 职责 |
|---|---|
| [image.go](image.go) | 普通图片结构、解码、格式/大小/尺寸校验及校验错误 |
| [image_test.go](image_test.go) | 图片解码、空串清除及损坏输入拒绝 |
| [http/](http/README.md) | 设置接口和图片响应 |
| [persistence/](persistence/README.md) | 品牌表、事务读写及迁移 |

HTTP 先完整校验 Logo，再校验 Icon，两图都通过才写入。存储参数中 nil 保持原图，空图片清除；图片字节是普通数据，不提供不可变对象包装。app 注入具体 Store，业务校验不依赖 HTTP 或数据库类型。
