# 品牌配置存储

本包提供品牌 Store，由 [app](../../app/README.md) 注入已有数据库连接。表、读取结果和修改参数与读写方法放在一起；用同一事务更新指定图片并回读结果。

品牌数据是一张单例表，全部设置放在同一行里。

改之前要知道：建表、写初始记录和记版本必须一起成功或一起回滚；多节点同时启动时，PostgreSQL 靠事务锁协调，SQLite 只在数据库忙时整笔重试，其他错误一律不重试。数据库连接由上游创建与关闭，本包不碰。

| 文件 | 职责 |
|---|---|
| [migration.go](migration.go) | 版本化迁移与 PostgreSQL 事务锁 |
| [store.go](store.go) | 表与修改参数、读取/原子更新/重置及图片 hash 派生 |
| [store_test.go](store_test.go) | 持久化、并发合并、清除、重连与写后读失败回滚 |
| [migration_test.go](migration_test.go) | 迁移失败回滚、锁冲突重试与取消 |

## 数据库测试

默认使用临时 SQLite。专用 PostgreSQL 测试库通过键值格式的 `BRANDING_TEST_POSTGRES_DSN` 指定；在 `ee/` 下执行 `GOWORK=off go test -count=1 -v ./internal/branding/persistence`。

测试会为每个测试路径建立独立 schema，并在结束后删除；测试账号须有创建 schema 权限。只在相应后端运行专用锁测试。
