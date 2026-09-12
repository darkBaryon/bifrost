# 品牌配置存储

本包实现 [品牌 Repository](../repository.go)，由 [app](../../app/README.md) 注入已有数据库连接。业务服务决定操作范围，本包用同一事务更新指定图片并回读结果。

数据库仍为 `ee_branding` 单例表，ID=1；迁移 ID 为 `ee_branding_v1`。表、初始记录和版本共同提交或回滚，PostgreSQL 使用既有事务锁协调多节点迁移。SQLite 仅对 busy/locked 回滚整笔后有限重试（次数与间隔见 `migration.go` 的 `busyRetries`/`busyRetryStep`，与身份迁移同一口径），非锁错误不重试；取消或耗尽仍明确失败。连接由上游管理，本包不关闭。

| 文件 | 职责 |
|---|---|
| [table.go](table.go) | 数据库行、固定表名及到业务 Settings 的转换 |
| [migration.go](migration.go) | 版本化迁移与 PostgreSQL 事务锁 |
| [store.go](store.go) | 原子局部更新、读取及图片 hash 派生 |
| [store_test.go](store_test.go) | 持久化、并发合并、清除、重连与写后读失败回滚 |
| [migration_test.go](migration_test.go) | 迁移失败回滚、锁冲突重试与取消 |
| [compatibility_test.go](compatibility_test.go) | 验证旧库字段、数据、hash、时间和迁移版本不变 |
| [testdata/legacy.sql](testdata/legacy.sql) | 迁移前代码生成的 SQLite 快照，仅含合成数据 |

## 数据库测试

默认使用临时 SQLite。专用 PostgreSQL 测试库通过键值格式的 `BRANDING_TEST_POSTGRES_DSN` 指定；在 `ee/` 下执行 `GOWORK=off go test -count=1 -v ./internal/branding/persistence`。

测试会为每个测试路径建立独立 schema，并在结束后删除；测试账号须有创建 schema 权限。只在相应后端运行专用锁测试，SQLite 历史快照测试在 PostgreSQL 模式下跳过。
