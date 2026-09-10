# 品牌业务

本模块负责部署级 Logo 与小图标的业务规则，按 [EE 后端架构](../../../product/架构/EE后端架构.md) 组织。

`http/` 解析字段并调用业务包；`Service` 通过 `Repository` 操作存储；`persistence/` 实现该接口。具体依赖由 [app](../app/README.md) 注入，业务根包只使用标准库。

| 文件 | 职责 |
|---|---|
| [settings.go](settings.go) | Settings 与局部修改 Patch |
| [asset.go](asset.go) | 图片值对象、Base64/data URI 解码、格式/MIME/容量/尺寸校验 |
| [errors.go](errors.go) | 校验错误与图片不存在错误 |
| [repository.go](repository.go) | 读取与原子局部更新的窄接口 |
| [service.go](service.go) | 设置读取、空操作检查、重置与当前版本图片选择 |
| [service_test.go](service_test.go) | 验证有效图片不可变、空操作、原子重置、故障与资源版本边界 |

Patch 中 nil 表示保持，零值 Asset 表示清除；非空图片由 DecodeAsset 校验后构造，Data 返回副本。HTTP 按 Logo、Icon 顺序构造两张图片，全部通过后才保存，保留原有错误优先级与两图原子性。

[HTTP 接口](http/README.md) · [存储与迁移](persistence/README.md)
