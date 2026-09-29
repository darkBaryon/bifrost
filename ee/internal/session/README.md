# 会话与一次性票据

负责会话和 WebSocket 握手票据的签发、撤销、消费与到期清理。账号是否启用、密码版本是否一致、是否拥有通知权限由调用方检查；本模块只处理会话生命周期。

本模块没有独立的 HTTP 接口，因此不建 `http/` 包。登录、退出和票据请求由 [auth/http](../auth/http/README.md) 接收，再调用对应业务入口。

| 文件 | 职责 |
|---|---|
| [session.go](session.go) | 身份句柄、会话数据、存储契约和使用调用方事务的业务操作 |
| [token.go](token.go) | 随机令牌、长度检查及兼容已有数据的摘要 |
| [persistence/store.go](persistence/store.go) | 事务绑定、会话与票据读写、条件消费和过期清理 |
| [persistence/rows.go](persistence/rows.go) | 已有会话与票据表的运行期行结构 |
| [persistence/lifecycle_test.go](persistence/lifecycle_test.go) | 真实 SQLite 的事务回滚、一次性消费、关联删除及故障原因保留 |
| [persistence/cleanup_test.go](persistence/cleanup_test.go) | 过期清理的事务边界、保留规则与到期消费检查 |

改动前需注意：

- 所有写操作必须显式传入非空事务；本模块不开始、不提交事务，也不回退到原数据库连接。
- 票据消费后，调用方仍须在同一事务检查账号、会话与权限；失败时一起回滚消费标记。
- 删除账号的会话时先删关联票据，再删会话。账号及角色记录由调用方在同一事务处理。
- 签发会话时只清理到期会话，签发票据时只清理到期票据；未到期的撤销会话及已消费票据继续保留。
- 保持 `ee_identity_sessions`、`ee_identity_ws_tickets` 的表名、字段和摘要格式；既有迁移仍负责建表，本模块不重复迁移。
- 存储故障原样交回事务拥有方，保留锁冲突重试和诊断能力；拥有方在事务结束后转换安全错误。原始令牌只能在事务提交后返回给客户端。
