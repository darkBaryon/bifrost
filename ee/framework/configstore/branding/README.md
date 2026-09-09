# 品牌配置存储

## 功能

将当前部署的 Logo 和小图标保存在配置数据库中，提供读取、局部更新和恢复默认的能力。一个部署共用一条品牌记录，表名为 `ee_branding`，ID 固定为 `1`。

## 架构

[服务启动装配](../../../transports/bifrost-http/server/bootstrap.go)通过已有 ConfigStore 执行迁移，再把同一数据库连接传给 `BrandingStore`。[品牌 HTTP 包](../../../transports/bifrost-http/handlers/branding/README.md)调用存储方法；本包不处理 HTTP 请求或页面状态。

迁移负责建表和初始化记录，版本为 `ee_branding_v1`。表、初始记录和迁移版本一起提交或回滚；PostgreSQL 使用事务锁协调本功能的多节点迁移。

保存时只更新指定的图片，并在同一事务内读取结果。`BrandingPatch` 中 `nil` 表示保持原图，空图片表示清除，有图片字节则替换并计算内容哈希。数据库连接由调用方管理。

## 文件说明

| 文件 | 职责 |
|---|---|
| [table.go](table.go) | 定义品牌表的字段、固定表名和单例记录。 |
| [migration.go](migration.go) | 执行版本化迁移，处理 PostgreSQL 迁移锁及事务。 |
| [store.go](store.go) | 实现读取、局部更新和重置；生成图片内容哈希。 |
| [store_test.go](store_test.go) | 验证持久化、局部合并、清除、重复迁移及写后读取失败时的回滚。 |
| [migration_test.go](migration_test.go) | 验证 SQLite / PostgreSQL 迁移失败回滚、各自的锁冲突后重试和取消迁移。 |

## 数据库测试

默认运行 SQLite。对专用 PostgreSQL 测试库，设置键值格式的 `BRANDING_TEST_POSTGRES_DSN`（例如 `host=127.0.0.1 port=5432 user=test dbname=branding_test sslmode=disable`），在 `ee/` 下运行 `GOWORK=off go test -count=1 -v ./framework/configstore/branding`。测试会为每个测试路径创建独立 schema，并在结束后删除；账号需要创建 schema 的权限。SQLite 和 PostgreSQL 专用锁测试只在相应后端运行。
