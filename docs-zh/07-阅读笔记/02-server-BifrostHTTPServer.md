# server.go:BifrostHTTPServer

文件 2832 行,骨架只有五行:

```
type ServerCallbacks interface { 85 个签名 }   别人能让 server 干什么
type BifrostHTTPServer struct  { 22 个字段 }   server 握着什么
Bootstrap()                                    把 22 个字段填满(360 行)
Start()                                        开端口,收信号后按序停掉(120 行)
Reload*/Remove*/On*/Evict* ...                 85 个签名的实现(1700 行)
```

## 它是什么

一个正在运行的网关进程。8080 上的 `npx @maximhq/bifrost` 在内存里就是这个 struct 的实例。

它是引擎 `core.Bifrost` 的外壳:引擎是纯库,没有网络和数据库;server 给它加监听、配置存储、控制台、热更新。一个端口同时提供推理 API、管理 API、静态 UI。

## 三个职责

- **造部件**:按依赖顺序 New 出 Config、Client、二十个子件、所有 handler → `Bootstrap`
- **让部件一致**:某个部件的数据变了,通知相关部件同步 → 85 个回调
- **管生命周期**:开端口;收到信号后按启动的反序停掉 → `Start`

不做的事:不处理请求(handler)、不转发 LLM(core)、不读写库(store)、不鉴权限流(中间件、插件)。文件里没有一行业务逻辑。

它是上帝类型,但是"什么都握着、自己只转发"的那种。厂长办公室:建厂时定机器和顺序,运营时一条线改了通知上下游,下班按序关灯。

## 为什么装配是它的方法

因为**装配不是一次性的**。配置运行期随时被控制台改,改完立刻生效。`ReloadPlugin`、`RestartLiveModelRefresher` 这些回调就是单独重跑 Bootstrap 的某一步。Bootstrap 是第一轮,之后一直在被重新装配。拆成 Builder 的话 Builder 也得带 Reload,等于换个名字。

次要原因:企业版要在装配前后给字段赋值;main 的 `init()` 把 flag 直接绑到 struct 字段,对象先于装配存在。

## ServerCallbacks 为什么这么大

两层接口:

- **小接口**在 handler 侧,每个 handler 自己声明只列自己要的(`ModelsManager` 10 个、`MCPManager` 14 个)。原因是 handlers 包不能反向 import server 包。
- **大接口**是所有小接口的并集,只用在一处:`RegisterAPIRoutes(ctx, callbacks, ...)` 的参数。函数体把它塞进十几个 handler,每次隐式收窄。它不是抽象,是运输容器。

谁实现它:server 自己。`s.RegisterAPIRoutes(s.Ctx, s, ...)` 自己传自己。一圈:server 造 handler → 控制台改配置 → handler 写库后回调 server → server 更新内存。

绕这一圈是为了**企业版**:它传自己的包装对象进来,回调时先广播集群再转调 OSS。参数是接口才能替换。

handler 自己更新内存行不行:能,以前就是这样。搬到 server 是因为更新要跨子系统编排、协调锁在 server 上、同一更新有多个入口、企业版包一处比改每个 handler 省。约定:handler 读内存直接读,写内存经过 server。

## 企业版

闭源私有仓库,版本追随 OSS。接入靠覆盖目录:`ui/app/enterprise` 在 .gitignore 里,tsconfig 先查它再查 `_fallbacks`;Go 侧企业 struct 包 OSS server。

插座:`IsEnterprise` ctx 标记(跳过 OSS 治理和鉴权)、`ServerCallbacks` 参数、`callbacks.(XxxProvider)` 断言、struct 里注释 "nil on OSS" 的可选字段、`enterprisePlugins` 列表。OSS 里看起来多余的间接层基本都是这些。

自己二开:同仓库独立目录(`ee/` 模块 + `ui/app/enterprise/`),用现成插座,不改上游文件。`IsEnterprise` 别急着设。

## 热更新

普通 CRUD 每次请求问数据库,进程不记东西。Bifrost 每请求要查六七样配置,预算 11 微秒,必须常驻内存;而且内存里是**活对象**(带连接池的 client、队列、goroutine、MCP 连接),改库一行要重建对象。内存一份库一份要同步,这就是热更新。

判据:每请求都读、放内存、物化成带资源的对象。日志页是 CRUD,provider 页是热更新。

四招,Bifrost 全用了:

| 招 | 样本 |
|---|---|
| 换指针不改内容 | `handlers/middlewares.go:1059` `authConfig.Load()` |
| 读写锁加拷贝 | `lib.Config.Mu`,`server.go:1363` 上方注释 |
| 晚绑定不存指针 | `lib/config.go:5948` `FindPluginAs` |
| 停旧起新 | `server.go:1434-1490` refresher 三函数,`:1493` `beginKeyRefresh` |

原则:一个请求从头到尾只看一个版本。关键词 copy-on-write、RCU。

Redis 解决进程间传话,解决不了进程内换活对象;单二进制零依赖是卖点,外部存储全可选。

## 存储

没有内存数据库,内存里就是 Go 变量。持久层:ConfigStore(SQLite/Postgres,`~/.config/bifrost/config.db`)、LogsStore(SQLite/Postgres/ClickHouse,`logs.db`)、VectorStore、ObjectStore、KVStore(OSS 进程内 map)。

两个 SQLite 分开:写入模式相反、寿命不同、生产去向不同。

内存副本三处:`lib.Config`、`LocalGovernanceStore`(九个 sync.Map,热路径最热)、`ModelCatalog`。

单进程持有的代价:两个实例连同一个库,A 改了 B 不知道。企业版集群解决这个。

## 为什么这么大

Go 编译单元是包,拆文件零技术收益;三个上帝类型方法必须同包;每周发版靠小步追加长大。粗粒度架构干净,文件级卫生差。

成长史:2025-09 从 main.go 抽出 567 行 → 2025-11-15 引入 ServerCallbacks 收拢热更新(转折点)→ 2026-01 唯一瘦身,插件加载搬走 → 2026-08 现在 2926 行。199 个提交里 132 个净增不到 50 行,两个人写 68%。

**fork 里不要拆这些文件**,会和上游整文件冲突。

## 怎么读

`Cmd+Shift+O` 大纲,`@:` 分组,`@Reload` 过滤。按前缀归堆得到九类可改对象:MCP(约 20 个方法,最重)、MCP 凭据缓存、治理五实体、provider 与 key、实时模型、定价、插件、零散配置、装配与生命周期。

真正要读的约 600 行:struct、Bootstrap、Start、PrepareCommonMiddlewares,加一个 ReloadProvider 看套路。

## 未解决

- 启动时 `RefreshAllLiveModels` 同步拉上游,读 ModelCatalog 时看超时和失败处理
- `ReloadClientConfigFromConfigStore` 重建 Account,第 4 步看 Account 是否无状态
- `RegisterAPIRoutes` 里第二次 `NewWebSocketHandler`,第 5 步看
- 企业版怎么设 `IsEnterprise`,OSS 只有读取点

下一步:第 3 步 `lib/config.go:857 LoadConfig`。
