# Logo品牌设置收尾验证（2026-09-09）

## 本次范围

修复图片校验错误仍可保存；补齐真实PostgreSQL验证。用户已确认功能测试通过，并授权安装本地PostgreSQL。保留原数据库与品牌配置，不提交或推送代码。

## 浏览器红绿验证

修复前：真实文件选择器先选有效 icon.jpg，再选损坏 invalid.png；错误可见，但保存仍可点。
修复后：同样操作出现错误，保存禁用，旧草稿文件名保留；点击移除清错后恢复保存。无旧图时选择损坏文件，移除按钮也可清除错误。测试仅操作草稿，最后刷新丢弃草稿，未保存覆盖用户图片。

实现：saveDisabled 由通用禁用状态及两位置 fileErrors 派生，提交函数同步拦截；选图和移除不因字段错误而禁用。未新建状态管理机制。

浏览器快照：修复前 `.playwright-cli/page-2026-09-09T14-43-01-181Z.yml`；修复后 `.playwright-cli/page-2026-09-09T14-45-33-965Z.yml`，无旧图清错 `.playwright-cli/page-2026-09-09T14-46-20-348Z.yml`。这些为本机测试产物。

## 自动检查

- make -C ee build-ui：通过。
- ee 下 GOWORK=off go build -o tmp/bifrost-branding-latest ./transports/bifrost-http：通过。
- ee 下 GOWORK=off go test -count=1 ./framework/configstore/branding ./transports/bifrost-http/handlers/branding：通过（默认SQLite）。
- Vitest useBranding、brandingApi、image：3文件18项通过。
- git diff --check：通过。
- 更新后原端口58965保持运行，POST /api/branding/get 返回200。

## PostgreSQL实测

安装 Homebrew postgresql@17（17.11），使用独立临时数据目录、Unix socket 和专用 branding_test 数据库，不启动登录自动服务、不暴露网络端口。

设置 BRANDING_TEST_POSTGRES_DSN 后复用存储及迁移测试，验证：迁移失败时DDL与版本记录回滚、重试幂等、取消迁移、事务迁移锁等待取消后重试、并发局部更新不丢字段、连接重开后持久化、单位置清除、重复重置、写后读失败回滚。SQLite专用锁测试在PG下跳过；PG专用锁测试在SQLite下跳过。

另用独立Go探针写入两个位置，实际重启 PostgreSQL 进程，再读回并逐字节比较图片、哈希及更新时间，结果一致；随后重置测试数据。与此前SQLite下HTTP/服务重启测试互补，不声称本次又执行了完整PG HTTP浏览器流程。

以下为实际PG测试日志：

```text
=== RUN   TestBrandingMigrationAtomicFailure
=== RUN   TestBrandingMigrationAtomicFailure/ee_branding

2026/09/09 22:49:16 [34;1m/Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/migration_test.go:31
[0m[35m[warn] [0mremoving callback `test:migration-failure` from /Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/migration_test.go:31
=== RUN   TestBrandingMigrationAtomicFailure/migrations

2026/09/09 22:49:16 [34;1m/Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/migration_test.go:31
[0m[35m[warn] [0mremoving callback `test:migration-failure` from /Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/migration_test.go:31
--- PASS: TestBrandingMigrationAtomicFailure (0.12s)
    --- PASS: TestBrandingMigrationAtomicFailure/ee_branding (0.09s)
    --- PASS: TestBrandingMigrationAtomicFailure/migrations (0.02s)
=== RUN   TestBrandingSQLiteBusyAndRetry
    migration_test.go:49: SQLite 专用锁测试
--- SKIP: TestBrandingSQLiteBusyAndRetry (0.00s)
=== RUN   TestBrandingCanceledMigration
--- PASS: TestBrandingCanceledMigration (0.01s)
=== RUN   TestBrandingPostgresLockAndRetry

2026/09/09 22:49:16 [31;1m/Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/migration.go:27 [35;1mtimeout: context deadline exceeded
[0m[33m[200.140ms] [34;1m[rows:0][0m SELECT pg_advisory_xact_lock(8342761901)
--- PASS: TestBrandingPostgresLockAndRetry (0.22s)
=== RUN   TestBrandingPersistenceAndMerge
--- PASS: TestBrandingPersistenceAndMerge (0.04s)
=== RUN   TestBrandingReadFailureRollsBackWrite

2026/09/09 22:49:16 [34;1m/Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/store_test.go:132
[0m[35m[warn] [0mremoving callback `test:read-failure` from /Users/xinyue/VSCode/ws_2026/bifrost-branding/ee/framework/configstore/branding/store_test.go:132
--- PASS: TestBrandingReadFailureRollsBackWrite (0.01s)
PASS
ok  	github.com/darkBaryon/bifrost/ee/framework/configstore/branding	0.447s

```

PostgreSQL server restart: bytes, hashes, updated_at unchanged
803e251a81fd8168e82fda80196409946db86b451063a23847e2cbccdaf61668 df81978af03849f480f5e04192fbe8047ce44e2b6388c1f6af57f8d3551e98d1 2026-09-09 14:46:26.980404 +0000 UTC

临时PostgreSQL实例测试后已停止；软件保留安装，网页测试服务继续运行。
