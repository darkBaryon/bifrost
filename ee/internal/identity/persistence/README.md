# 身份存储

用 GORM 在上游的配置数据库上实现 `identity.Repository` 与 `identity.Tx`。连接由宿主持有并关闭；SQL 日志静默，故障只记操作名、阶段、关联 ID 和驱动错误码。

## 做什么

- 迁移 v1 建表，v2 为账号增加可空的 last_login_at；旧记录不回填。新库的 v1 已含该列，v2 只登记版本。
- 账号删除只删账号行；业务入口显式调用 session 清理会话与票据，同事务保留密码事件。跨模块回滚由 rbac/persistence/accounts_test.go 验证。
- 保留六张 `ee_identity_*` 表的迁移行定义；账号、状态和事件仍由本包读写。改行结构等于改表，须走新迁移版本。
- 每个写事务先 `UPDATE state SET revision = revision + 1` 拿写锁（SQLite 库级、PostgreSQL 行锁），身份及通过本包接入的角色修改因此串行。
- 身份复验调用 auth/persistence 的单次 JOIN，不在这里保留第二套认证 SQL。登录限流与最近登录时间归 auth，会话、票据和过期清理归 session。
- SQLite 与上游共用文件时的 busy/locked 整笔重试；两种驱动的唯一键冲突统一成 `ErrConflict`。

运维自查行数：`SELECT COUNT(*) FROM ee_identity_sessions; SELECT COUNT(*) FROM ee_identity_ws_tickets;`——会话行数 ≈ `SessionTTL` 窗口内的登录次数（默认 24 小时、内部控制台约为两位数；TTL 可配 1 小时到 7 天，按部署重算），票据行数 ≈ `WSTicketTTL` 内的 WS 建连次数；超过万行再评估加索引。

## 不做什么

bcrypt 算法在 [auth/hasher](../../auth/hasher/README.md)，装配方通过同一个 auth.Passwords 实例限制所有密码计算；账号与密码规则仍在 [identity](../README.md)。

## 文件

| 文件 | 内容 |
|---|---|
| [rows.go](rows.go) | 迁移行结构与账号、事件转换 |
| [store.go](store.go) | 账号读取与写入、事务、分页；认证读取委托 auth |
| [scope.go](scope.go) | 向其他模块提供同一读取快照、写事务和不含凭据的账号查询 |
| [migration.go](migration.go) | `ee_identity_v1` 建表与索引、`ee_identity_v2_last_login` 增量加列，共用 PostgreSQL 迁移锁 |
| [diagnostics.go](diagnostics.go) | 排除业务拒绝后调用 database 共用安全故障日志，并复用 busy 重试 |
| [fixture_test.go](fixture_test.go) | 组合独立 auth/session 与账号服务；默认管理员策略的票据编排只用于测试 |
| [service_test.go](service_test.go) | 经真实 Store 验证账号、密码、登录竞态及跨模块回滚 |
| [accounts_page_test.go](accounts_page_test.go) / [cleanup_test.go](cleanup_test.go) / [diagnostics_test.go](diagnostics_test.go) | 分页、过期清理、诊断 |

测试默认用临时 SQLite；设置 `IDENTITY_TEST_POSTGRES_DSN` 后在专用 PostgreSQL 库的独立 schema 里跑，结束只清理自己的 schema。认证自身的登录时间/会话原子性与限流桶回滚另由 auth/persistence 测试覆盖，四槽共享与身份复验由 auth 测试覆盖；这里继续保留原有账号业务和故障诊断断言。
