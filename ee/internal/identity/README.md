# 迁移中的账号与密码

本包暂时保留账号 CRUD、初始化、旧管理员导入、密码变更和密码事件。登录与身份验证已迁到 [auth](../auth/README.md)，会话与票据生命周期已迁到 [session](../session/README.md)，这里不再创建 SessionService 或维护登录状态。

| 文件 | 职责 |
|---|---|
| [model.go](model.go) | 账号、密码事件、错误与尚未迁出的账号存储接口 |
| [core.go](core.go) | 账号/密码服务依赖、auth 复验结果转换和分页 |
| [access.go](access.go) | 旧账号权限策略、初始化/恢复检查和安全账号查询 |
| [accounts.go](accounts.go) | 初始化、导入、创建、启停和删除账号；显式调用 session 清理 |
| [passwords.go](passwords.go) | 本人改密、管理员重置、恢复和事件；显式调用 session 撤销 |
| [persistence/](persistence/README.md) | 账号与事件持久化、旧迁移和旧角色事务协作 |
| [http/](http/README.md) | 账号与密码 HTTP 入口；认证接口由 auth/http 独立注册 |

账号和密码服务接收与 auth 共享的密码哈希器。跨模块会话撤销、票据清理使用当前事务的 `Tx.DB()`；任何一步失败仍整体回滚。会话验证只复用 auth 的规则，不维护第二套判定。

账号/RBAC 旧 AccountPolicy 和共享 core 尚待下一批重写；本批不改变固定恢复账号、自身操作或最后管理员限制。登录 HTTP 入口已迁入 auth/http；公共认证与权限中间件仍待后续批次完成。
