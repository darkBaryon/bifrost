# server/plugins.go 装配 + governance 真实插件

看两件事:插件怎么被装上,一个真实插件长什么样。governance 是"规则从哪来、怎么热更新、怎么落库"的完整范本。

## 装配(server/plugins.go,190 行)

`LoadPlugins` 三行:`loadBuiltinPlugins` → `loadCustomPlugins` → `SortAndRebuildPlugins`。

- **内置**(:174-285)是手工展开的八段,不是 for:telemetry 1、prompts 2、logging 3、governance 4、otel 5、semanticcache 6、compat 7、maxim 8,resolver 垫底。每段条件不同(telemetry 默认开、prompts 要 ConfigStore 且非企业版、logging 要 EnableLogging 且 LogsStore),各调 `xxx.Init(ctx, config, logger, 需要的 store…)`,store 由 server 从 Config 取了注入。
- **自定义**(:286-342)是真 for:遍历 `PluginConfigs`,跳过内置名,disabled 的只 `VerifyBasePlugin` 记状态不实例化,enabled 的 `InstantiatePlugin`;企业版插件名加载失败静默跳过。
- `InstantiatePlugin(name, path, config, bifrostConfig)`:**每个插件调一次**,只回答"代码在哪"——`path` 有值走 `.so` 加载器按符号找函数,nil 按 `name` 在 switch 里找内置 `Init`。产出都是 `schemas.BasePlugin`,后续注册排序不区分来源。"内置"是代码在仓库里,不是默认开;两个维度:代码在哪、开不开。包壳里 `SyncLoadedPlugin` 注册的插件不经过它。
- `registerPluginWithStatus` 对内置 `failOnError=false`:初始化失败只记 `PluginStatusError`,不阻断启动。

## governance 骨架(21k 行)

| 文件 | 行 | 角色 |
|---|---|---|
| store.go | 4914 | 内存副本:九个 sync.Map + 69 方法的 `GovernanceStore` 接口 |
| main.go | 1758 | 钩子 + Init |
| routing.go | 685 | CEL 规则引擎(加权多目标、scope 链、chain rule) |
| resolver.go | 526 | 纯决策:VK 在此 provider/model 上预算限流过不过 |
| tracker.go | 417 | 记账:内存累加、10s 落库、过期重置 |

`Init`(main.go:134-243)按依赖序:`NewLocalGovernanceStore` 从库读八张表(customer/team/VK 带关联/budget/rate limit/model config/provider/routing rule)进 sync.Map + CEL env → resolver → tracker → routing engine。中间用**分布式锁**做启动重置,多实例只一个执行,失败非关键。

## 三个钩子各干一件事

- **PreRequestHook(路由)**:有 VK 或有规则才干活。路由规则(CEL 命中换 provider/model)→ 发布 VK 对该模型的 provider 白名单到 ctx → 负载均衡 → 算 VK 允许的 MCP 工具进 ctx。只改信封写 ctx,不拒绝。
- **PreLLMHook(准入)**:查必填 header → 取 VK → `EvaluateGovernanceRequest` → 不允许 `ShortCircuit{Error}`。决策序(:927-1147):**VK 身份(存在/激活/provider 允许/model 允许/VK 级预算限流)→ Customer 预算 → Team 预算 → User(企业版)→ MCP 工具白名单**。VK 必须最先:否则吊销的 key 挂在超预算 team 下会返回"team 超预算"而非"key 无效",泄漏团队结构——安全角度定的顺序。
- **PostLLMHook(记账)**:算 token 和费用,goroutine 异步记,带 recover。失败但上游已处理 token 的也记(`BilledUsage`,Anthropic 照收);`RequestID + attempt` 去重保证每次物理调用只记一次;受影响的预算 ID 放 ctx 给 logging 插件关联。费用 `modelCatalog.CalculateCost`。

## 规则从哪来 / 怎么热更新 / 怎么落库(照抄的部分)

- **从哪来**:启动从 ConfigStore 八张表读进内存;运行期只查内存(sync.Map 无锁),永不查库。
- **热更新**:控制台改 VK → handler 写库 → `server.ReloadVirtualKey`(server.go:514)从库重读带关联 → `GetGovernanceStore().UpdateVirtualKeyInMemory(...)`。69 个方法一半是 `Update*/Delete*/Create*InMemory`,给 server 回调用。插件不监听库,等被通知。
- **落库**:反方向。`tracker.UpdateUsage` 内存累加 VK/team/customer/provider/model 各级 token 和请求数,不写库;10s worker 四件事:重置过期限流窗口、重置过期预算周期、全部计数 dump 到库、清理去重 key;Cleanup 最后 dump。库最多落后 10s,重启最多丢 10s,换热路径零写库。

## 两个可抄模式

- **Config 传指针**:`RequiredHeaders *[]string`、`RoutingChainMaxDepth *int` 指向 live config,server 改了插件自动看到,连回调都省。适合简单开关。
- **小接口给调用方**:`BaseGovernancePlugin`(GetName/EvaluateGovernanceRequest/HTTPTransportPreHook),server 用 `FindPluginAs[BaseGovernancePlugin]` 拿,企业版实现同接口即可替换,`GovernancePluginName` ctx 键允许换名。自己的插件要被别的组件调也这样。

## 内容安全插件照抄的形状

`store.go` 规则内存副本(检测档案、阈值、按 VK 绑定,从自己的表加载);`main.go` PreLLM/PostLLM;一组 `Update*InMemory` 给包壳回调;拦截事件像 tracker 内存攒定时落库。规模 governance 十分之一。

下一步:14 integrations 引擎(可跳),15 三个 store 接口。

## 补:prompts 插件,governance 的最小版本(611 行一个文件)

功能:请求头带 `x-bf-prompt-id`(可选 `x-bf-prompt-version`),插件把该版本的模板消息插到用户消息前,模板的模型参数当默认值(请求自带的优先)。

五个部件:`InMemoryStore` 接口只要 ConfigStore 两个方法(消费者定义小接口);`PromptResolver` 接口 + 默认 `headerResolver` 从 ctx 读两个 header,`InitWithResolver` 给企业版换按用户/团队决定的 resolver;`Plugin` struct = store + logger + resolver + 读写锁 + 两个 map;`loadCache`/`Reload`;`PreLLMHook`。

规则从哪来、怎么热更新四十行讲完:`loadCache` 两次查库在**局部变量**建好新 map,加写锁换 struct 上的指针——"换指针不改内容"最小实现;`Reload` 就是再调一次,handler 增删改完提示词调 `PromptCacheReloader.Reload`(全量重载,表小不值得增量)。

`PreLLMHook` 六步:resolver 拿 ID 和版本 → 查内存 map(版本 0 = latest)→ 选中的 ID/名/版本写 ctx 给 logging → 版本参数合并进请求(请求优先)→ 模板消息转 ChatMessage → 拼到 `Input` 前。每步失败 `Warn` 后 `return req, nil, nil`:**找不到模板不拒绝,原样放行**,它是增强不是门禁。PostLLMHook、Cleanup 空实现。

没有的:落库、后台 worker、拒绝、层级决策。"读配置改请求"这类插件的标准形状;内容安全插件比它多两样——PreLLMHook 里拒绝(第 12 步 ShortCircuit)、拦截记录落库(governance tracker),其余照它抄。
