# framework/{configstore,logstore,vectorstore}:三个 store 接口

framework 对上层的三份契约:

| 接口 | 位置 | 方法 | 管什么 | 后端 |
|---|---|---|---|---|
| `ConfigStore` | configstore/store.go:220-990 | **296** | 控制台能改的一切 | SQLite / Postgres |
| `LogStore` | logstore/store.go:23-157 | 79 | 请求日志写入、搜索、二十来种直方图排名 | SQLite / Postgres / ClickHouse |
| `VectorStore` | vectorstore/store.go | 12 | 语义缓存向量增删查 | Weaviate / Redis / Qdrant / Pinecone |

## 296 个方法只有一份实现

每个实体五六个 CRUD 乘五十来个实体。样本 `RDBConfigStore.GetBudget`:可选事务参数、`First(&x, "id = ?")`、`ErrRecordNotFound` 转 `ErrNotFound`,十行 GORM。SQLite 和 Postgres **共用** `*RDBConfigStore`,工厂只换驱动开连接,方言差异 GORM 吸收,只有迁移里几处 `if dialect == "sqlite"`。rdb.go 9203 行 = 296 个十行方法。LogStore 类似,ClickHouse 单独实现(分析型查询不走 GORM)。

## 一张表三件套(tables/budget.go 范本)

struct 加 GORM tag(同时带 json tag,API 直接返回它);`TableName()`(治理表带 `governance_` 前缀);钩子 `BeforeSave` 做不变量校验(预算只能有一个 owner)、`AfterFind` 读后处理,密钥字段在钩子里调 `tables/encryption.go` 的 `encryptSecretVar`/`decryptSecretVar`——第 3 步"加密在 BeforeSave 里"就是这。

## 迁移怎么加(migrations.go 12000 行的骨架三样)

有序列表 `configstoreMigrationSteps`(:272)启动顺序跑;每个迁移一个函数,模板 `migrationAddNotificationsTable`:给 ID,`Migrate` 里 `AutoMigrate(&tables.TableXxx{})`,`Rollback` 里 `DropTable`;`RunSingleMigration` 交 `framework/migrator`,库里维护已执行 ID 表,**幂等**;`triggerMigrations` 外套集群 advisory lock,多节点只一个跑。加表 = tables/xxx.go + migrationAddXxxTable + 列表末尾追加一项。

## 对 fork 最重要的一条

`ConfigStore` 接口**导出 `DB() *gorm.DB`**(store.go:909)和 `ExecuteTransaction`。`ee/` 模块可以 `s.Config.ConfigStore.DB()` 拿同一个连接,`AutoMigrate` 自己的表(或用 migrator 包做幂等迁移),读写自己的表——**不改上游任何文件**:不动 296 方法接口、不动迁移列表,表定义在自己包里。审计日志、内容安全规则表、拦截记录全走这条路。守两条:表名加自己前缀(`ee_`)防撞名;迁移用 migrator 包并给自己的 ID 前缀,与上游迁移表共存。

上游 `adding-a-configstore.mdx` 讲的是加一种数据库后端(新 `ConfigStoreType`、新工厂、返回 `RDBConfigStore`),用不到。

## 审计表的形状

照企业版八字段:时间、动作、结果、发起者、目标、方法路径、IP、耗时;加 `Signature`(HMAC)、`Payload`(JSON 改动前后)。写入走 API 中间件(包壳),读走 `/api/ee/audit`,保留清理照 `LogsCleaner` 起定时器(`LogRetentionManager` 只要一个 `DeleteLogsBatch`)。

主线到此走完(10、14 跳过)。
