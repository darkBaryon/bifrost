# 身份存储

复用宿主ConfigStore的GORM连接，连接关闭由宿主负责。本模块SQL日志静默；在操作边界记录安全driver码、操作阶段和关联ID，禁止凭据、SQL或driver原文。

- `migration.go`：ee_identity_v1版本化迁移、独立PG迁移锁、六张身份表和查询索引。
- `store.go`：身份专用事务、单例写锁、账号与会话联合读取、限流与事件分页；复用宿主bcrypt。
- `diagnostics.go`：安全故障分类与SQLite busy判断；`diagnostics_test.go`验证关联、脱敏和迁移有限重试。
- `service_test.go`：真实SQLite/PG生命周期、失败回滚、初始化竞争、幂等及会话撤销测试。

所有身份写事务先更新state.revision，SQLite取得写锁，PostgreSQL取得行锁；凭据校验后进入事务重新读状态。测试默认独立SQLite文件，设置IDENTITY_TEST_POSTGRES_DSN后使用专用PG测试库内独立schema，测试结束仅清理自己的schema。
