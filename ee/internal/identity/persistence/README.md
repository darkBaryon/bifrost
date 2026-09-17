# 身份存储

用 GORM 在上游的配置数据库上实现 `identity.Repository` 与 `identity.Tx`。连接由宿主持有并关闭；SQL 日志静默，故障只记操作名、阶段、关联 ID 和驱动错误码。

## 做什么

- 六张 `ee_identity_*` 表的行结构与业务类型互转；改行结构等于改表，须走新迁移版本。
- 每个写事务先 `UPDATE state SET revision = revision + 1` 拿写锁（SQLite 库级、PostgreSQL 行锁），身份及通过本包接入的角色修改因此串行。
- 会话验证用一条 JOIN 同时读会话与账号；票据消费靠 `UPDATE ... WHERE consumed_at IS NULL` 保证只成功一次；登录限流桶原子预占。
- 写入新会话/票据前删除已到期的行（FIND-020）；限流桶写入时删除过期窗口。
- SQLite 与上游共用文件时的 busy/locked 整笔重试；两种驱动的唯一键冲突统一成 `ErrConflict`。

运维自查行数：`SELECT COUNT(*) FROM ee_identity_sessions; SELECT COUNT(*) FROM ee_identity_ws_tickets;`——会话行数 ≈ `SessionTTL` 窗口内的登录次数（默认 24 小时、内部控制台约为两位数；TTL 可配 1 小时到 7 天，按部署重算），票据行数 ≈ `WSTicketTTL` 内的 WS 建连次数；超过万行再评估加索引。

## 不做什么

bcrypt 在 [hasher](../hasher/README.md)；业务规则在 [identity](../README.md)。

## 文件

| 文件 | 内容 |
|---|---|
| [rows.go](rows.go) | 行结构与转换 |
| [store.go](store.go) | 读取、事务、限流、分页、过期清理 |
| [scope.go](scope.go) | 向其他模块提供同一读取快照、写事务和不含凭据的账号查询 |
| [migration.go](migration.go) | `ee_identity_v1` 建表与索引，PostgreSQL 迁移锁 |
| [diagnostics.go](diagnostics.go) | `Logger` 接口、安全故障日志、busy 重试 |
| [service_test.go](service_test.go) | 业务规则端到端（经真实 Store） |
| [accounts_page_test.go](accounts_page_test.go) / [cleanup_test.go](cleanup_test.go) / [diagnostics_test.go](diagnostics_test.go) | 分页、过期清理、诊断 |

测试默认用临时 SQLite；设置 `IDENTITY_TEST_POSTGRES_DSN` 后在专用 PostgreSQL 库的独立 schema 里跑，结束只清理自己的 schema。

增量迁移ee_identity_v2_last_login为账号增加可空last_login_at；旧记录不回填。store.go删除账号时同事务清理票据和会话，保留密码事件。rbac/persistence/accounts_test.go验证跨模块删除回滚及登录时间原子更新。
