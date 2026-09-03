# lib/config.go:Config 仓库与 LoadConfig

文件 7335 行,这一步只读一件事:`LoadConfig` 怎么把 `s.Config` 装出来。其余六千行是运行期读写方法,按名字查。

## 三个类型,一个名字起错了

| 类型 | 行 | 是什么 | 活多久 |
|---|---|---|---|
| `ConfigData` | 164 | config.json 解析出来的样子,纯数据 | 只在 LoadConfig 期间 |
| `Config` | 542 | 运行期仓库:5 个 store 连接 + 配置的内存副本 + 按配置建出的活对象(插件、OAuth worker、模型目录) | 整个进程 |
| `ServerConfig` | 150 | 文件里 `server` 那一节 | 挂在 Config 上 |

`Config` 不是配置,是配置的运行期宿主,上游文档叫它 in-memory store。纯配置是 `ConfigData`。

**为什么 Config 带方法而不是纯 struct**:因为它会变。变一个 key 是五步(加锁、改 map、写库、重建派生缓存、放锁),封成 `UpdateProviderKey` 调用方才不会漏;几千个 goroutine 读、控制台写,裸读 map 会 panic,`GetProviderConfigRaw` 里读锁加拷贝把纪律藏起来;`BasePlugins` 变了三份派生缓存要跟着重建。规则:不变的配置可以是纯数据,会变的必须是带方法的对象。

## 两个来源,谁说了算

config.json 是**种子和漂移检测源**,数据库是**运行期真相**。启动时文件灌进库,之后一切看库,控制台改的只写库不写文件。

文件可选,三种情况都合法:

| 情况 | 行为 |
|---|---|
| 没有 config.json | 全默认,provider 从环境变量自动探测(`autoDetectProviders`,:7063),控制台改的存 config.db。本机就是这种 |
| 有文件,有库 | 启动时文件灌库,之后库为准;文件改了下次重启文件覆盖 |
| 有文件,`config_store.enabled: false` | 只有文件,改配置要重启 |

为什么文件只进不出:热更新逼着库当真相(多节点共享、事务、行级更新,文件做不到);文件不能回写(人拥有的文件、密钥不落明文、多节点各有副本)。

路径写死:目录由 `server/utils.go:19` `GetDefaultConfigDir` 定(`--app-dir` 优先,否则 `~/.config/bifrost`),三个文件名 config.json / config.db / logs.db 在 LoadConfig 开头三行。配置文件的位置本身没法从配置读,最底层必须是代码常量。

## LoadConfig 主干(857-1023)

前半段读文件:自定义 `UnmarshalJSON`(422)先把顶层 key 记进 `presentSections`,以区分"没写这节"和"写了空的";文件不存在不算错,零值往下走。

后半段 16 步,值得读的:

- **1 加密**最前,store 写库的 BeforeSave 钩子要用 key
- **2 initStores**(1027)三分支:文件写了且 enabled 按文件建;没写这节建默认 SQLite;写了但 false 留 nil。后面所有 `if ConfigStore != nil` 源于第三支
- **4 loadClientConfig**(1218)hash 合并的最小完整样本,60 行
- **5 loadProviders**(1346)同一套逻辑两层:provider hash 相同还要进 key 层比,因为控制台可能单独加了 key;不同时 `mergeProviderKeys` 文件 key 保留、只在库的删掉但保留 ID 和状态
- **8 governance** 500 行,套路同上,不读

## 合并规则:hash 判"文件变没变"

场景:文件写 sk-A 灌库;控制台改成 sk-B(库变文件没变);重启,文件 A 库 B,听谁的?总是文件赢则控制台形同虚设,总是库赢则文件形同虚设。根子是看两边的值分不出**谁变的**。

灌库时把文件那一节的 hash 存进同一行。重启重算文件 hash 和存的比:

| 文件 hash | 判断 | 结果 |
|---|---|---|
| 相同 | 文件没动过,差异是控制台造成的 | 保留库 |
| 不同 | 文件被人编辑过 | 文件覆盖库,存新 hash |

hash 回答的是"文件和上次同步时的自己一不一样",只看文件一边。两边同时变则文件赢,设计者接受的代价。

为什么 hash 不存副本:只需要一个 bit 的答案;hash 存在每一行上(`config_hash` 列),副本要每行一个 JSON;副本含密钥明文要再加密;hash 比较一行代码;副本能做字段级合并和 diff 展示,但 Bifrost 是整节覆盖,用不上。哪天要做字段级合并就得换副本。

## source_of_truth:文件独裁开关

split 模式的洞:控制台改了库 hash 不更新,hash 相同只证明文件没变、证明不了库没变。这是有意的,为了保留控制台修改。GitOps 要反过来:重启后库收敛回文件。`source_of_truth: "config.json"` 跳过 hash,文件无条件覆盖,**并删库里文件没有的行**(`syncAuthoritativeProvidersInStore` :1402,事务里先删再写),split 永远不删。按节生效,靠 `presentSections`:没写的节不动库,写了空数组清空库。

## 热更新的三段分工

控制台改 key 时 handler 调两个东西:`inMemoryStore.UpdateProviderKey`(Config)和 `modelsManager.OnKeyUpdated`(server)。

| | Config | server |
|---|---|---|
| 管 | 值本身的一致性:锁、map、库、派生缓存 | 值变了谁要跟着动:治理插件缓存、模型目录、实时模型失效 |
| 知道 | 自己的 map 和 store | 所有子系统 |
| 位置 | lib,最底层,handler 和插件都能直接调 | server,最顶层,handler 只能靠接口回调 |

不能合到 server:handler 和插件调不到(import 环);不能合到 Config:编排要认识所有人,底层认识不了上层。

**热更新的税**:没有它 Config 是几百行纯 struct,server.go 约 500 行,没有 ServerCallbacks,没有 hash 和 source_of_truth。transports 一半代码是这一个需求交的。但控制台"改了立刻生效"就是产品卖点,税买的是产品本身。对自己的项目:配置静不静态是最早要定的决定,静态简单五倍,不要留"以后可能热更新"的口子。

## 读的地图

| 段 | 行 | 读法 |
|---|---|---|
| 类型 | 164-200, 332-420, 422-540, 542-658 | 读 |
| LoadConfig | 857-1023 | 读,列出 16 步 |
| 子函数 | 1027-1150, 1218-1322, 1346-1400, 1511-1540 | 读这四段,其余 load* 同套路跳过 |
| 运行期方法 | 3383-7335 | 不读;例外 `FindPluginAs`(5948)晚绑定样本,`autoDetectProviders`(7063) |

## 未解决

- 文档 `docs/architecture/transports/in-memory-store.mdx` 是空文件,本该讲这个
- 上游架构文档的代码片段是示意不是摘录(`KeySelector` 文档写 struct,代码是函数类型),文档定行为、代码定机制

下一步:第 4 步 `lib/account.go`,Config 套上 `schemas.Account` 接口皮喂给 core。
