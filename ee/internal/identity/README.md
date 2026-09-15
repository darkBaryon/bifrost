# 账号认证

本地账号系统：账号、密码、会话、密码操作记录。只依赖标准库，不知道 HTTP、数据库和上游；存储与哈希由 [app](../app/README.md) 按本包定义的接口注入。

## 做什么

三条线共用一个 `core`（仓储、bcrypt 并发槽、假哈希、会话与权限判定），由 `New` 一次装配成 `Services`：

- `SessionService`：登录（限流、bcrypt 比对、事务内重读后签发）、Cookie 验证、WebSocket 重验、登出、一次性票据。
- `AccountService`：初始化、旧管理员导入、建号、启停、分页列表。
- `PasswordService`：本人改密、管理员重置（按 operation_id 幂等）、离线恢复、密码事件查询；三条路径共用 `replacePassword`。

贯穿规则：账号 `AuthVersion` 每次改密或改状态 +1，会话签发时记下版本，不一致即失效；所有写操作在锁定 `state` 的事务里进行；错误只有 7 个业务值，其余折叠为 `unavailable`；谁能管账号由可替换的 `AccountPolicy` 决定（现为主管理员）。

## 不做什么

HTTP 在 [http/](http/README.md)，SQL 在 [persistence/](persistence/README.md)，接上游在 [host](../host/README.md)。

`hasher/` 是单文件子包，把上游加密工具包里的 bcrypt 包成本包要求的哈希接口。它多做一件事：判断一个字符串是不是完整的 bcrypt 哈希（版本、cost、盐与摘要长度都对），导入旧管理员凭据时用它把残缺值挡在外面。它适配的是一个库而不是运行中的宿主对象，所以放在本模块下而不是宿主接入包。

## 文件

| 文件 | 内容 |
|---|---|
| [model.go](model.go) | 类型、枚举、限制常量、错误值、`Repository`/`Tx`/`PasswordHasher`/`AccountPolicy` 接口 |
| [core.go](core.go) | `core`、`Services`、`New`、共享判定与分页辅助 |
| [session.go](session.go) | 会话线 |
| [accounts.go](accounts.go) | 账号线 |
| [passwords.go](passwords.go) | 密码线 |
