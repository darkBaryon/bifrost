# 密码哈希适配

把上游 `framework/encrypt` 的 bcrypt 包装成 `identity.PasswordHasher`，由 [app](../../app/README.md) 注入身份服务。放在 `identity/` 下而不是宿主接入包，因为它适配的是一个库，不触及运行中的宿主对象。

## 做什么

- `Hash` / `Compare`：直接转调上游 bcrypt。
- `ValidHash`：判断一个字符串是不是完整的 bcrypt 哈希（版本、cost、22 字节盐、31 字节摘要），用于导入旧管理员凭据时把残缺值拒之门外。

## 文件

| 文件 | 内容 |
|---|---|
| [bcrypt.go](bcrypt.go) | `Bcrypt` 类型与哈希形状校验 |
