# 身份存储

复用宿主 ConfigStore 的 GORM 连接，连接关闭由宿主负责。SQL 日志静默；在操作边界记录安全 driver 码、操作阶段和关联 ID，禁止凭据、SQL 或 driver 原文。

| 文件 | 职责 |
|---|---|
| [store.go](store.go) | 六张表的行结构及与业务类型的转换、读取、锁定 state 的事务、限流预占、分页、bcrypt 适配 |
| [migration.go](migration.go) | `ee_identity_v1` 版本化迁移、独立 PG 迁移锁、建表与索引 |
| [diagnostics.go](diagnostics.go) | 安全故障分类日志、SQLite busy 判断与整笔重试 |
| [service_test.go](service_test.go) | 真实 SQLite/PG 生命周期、失败回滚、初始化竞争、幂等及会话撤销 |
| [diagnostics_test.go](diagnostics_test.go) | 诊断关联与脱敏、迁移有限重试 |

行结构与业务类型分开定义：改业务字段不会改变表结构，改行结构须走新的迁移版本。唯一键冲突按驱动错误类型识别（PostgreSQL `23505`、SQLite `ErrConstraintUnique`）。所有写事务先更新 `state.revision`，SQLite 取得写锁，PostgreSQL 取得行锁；限流桶由服务给出键、阈值与窗口，本包原子预占。

测试默认独立 SQLite 文件，设置 `IDENTITY_TEST_POSTGRES_DSN` 后使用专用 PG 测试库内独立 schema，结束只清理自己的 schema。
