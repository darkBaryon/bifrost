# 品牌配置存储

本包提供品牌 Store，由 [app](../../app/README.md) 注入已有数据库连接。表、读取结果和修改参数与读写方法放在一起；用同一事务更新指定图片并回读结果。

数据库仍为 `ee_branding` 单例表，ID=1；迁移 ID 为 `ee_branding_v1`。表、初始记录和版本共同提交或回滚，PostgreSQL 使用既有事务锁协调多节点迁移。连接由上游管理，本包不关闭。

| 文件 | 职责 |
|---|---|
| [migration.go](migration.go) | 版本化迁移与 PostgreSQL 事务锁 |
| [store.go](store.go) | 表与修改参数、读取/原子更新/重置及图片 hash 派生 |
| [store_test.go](store_test.go) | 持久化、并发合并、清除、重连与写后读失败回滚 |
| [migration_test.go](migration_test.go) | 迁移失败回滚、锁冲突重试与取消 |
| [compatibility_test.go](compatibility_test.go) | 验证旧库字段、数据、hash、时间和迁移版本不变 |
| [testdata/legacy.sql](testdata/legacy.sql) | 迁移前代码生成的 SQLite 快照，仅含合成数据 |

## 数据库测试

默认使用临时 SQLite。专用 PostgreSQL 测试库通过键值格式的 `BRANDING_TEST_POSTGRES_DSN` 指定；在 `ee/` 下执行 `GOWORK=off go test -count=1 -v ./internal/branding/persistence`。

测试会为每个测试路径建立独立 schema，并在结束后删除；测试账号须有创建 schema 权限。只在相应后端运行专用锁测试，SQLite 历史快照测试在 PostgreSQL 模式下跳过。
