# server.go RegisterAPIRoutes:路由总目录

server 注册完路由,剩下就是 handlers/ 下的具体实现。这一步只需要知道门牌怎么分。

## 三张表,三套中间件

| 组 | 注册函数 | 中间件 | 前缀 | 给谁 |
|---|---|---|---|---|
| 推理 API | `RegisterInferenceRoutes` | 公共三层 + Tracing + TransportInterceptor + 推理鉴权(直通,交治理插件) | `/v1/...`、`/mcp`、15 个 SDK 兼容前缀(`/openai` `/anthropic` `/genai` `/bedrock`…) | LLM 客户端 |
| 管理 API | `RegisterAPIRoutes` | 公共三层 + dashboard 鉴权 | `/api/...`、`/health`、`/metrics`、`/ws`、`/.well-known` | 控制台 |
| UI | `RegisterUIRoutes` | 无 | `/`、`/{filepath:*}` 兜底,**必须最后注册** | 浏览器 |

server 决定"组",handler 决定"条":server 只构造 27 个 handler、传对应组的中间件、调各自 `RegisterRoutes`;具体路径写在各 handler 文件里。总目录不存在于任何一个文件。287 条管理路由里治理 42、日志 39 占近三成;推理 43 条常规 + 22 条异步,路径照抄 OpenAI。

查某个页面调了什么:按前缀找 handler 文件,搜路径字符串。API 文档是机器维护的 `docs/openapi/`(93 个 paths 碎片 + 160 个 schemas,`bundle.py` 打包,CI 同步,`api-validator` 技能校验)。

## handlers/ 这个包

**model 拆在三处**,没有叫 model 的目录:领域模型 `core/schemas`(105 文件,含所有接口,故叫 schemas);数据库模型 `framework/configstore/tables`(46 文件,放 framework 因为治理插件要用);HTTP DTO 在各 handler 文件顶部(只有一个使用者的类型不单开包)。

**handler 重的两个原因不同**:推理 handler 重是协议面大(43 种请求),已抽 `baseRequest` 公共管道,不值得动;管理 handler 重是缺 service 层(`updateMCPClient` 744 行,九件事里七件是业务编排)。方案:每领域一个应用服务层(多入口共用、可测、回滚集中);治理五实体同形 CRUD 可表驱动。fork 里不动上游,`ee/` 里按三层写。

**handler 互调只有一处真的**:30 个 handler 类型,持有另一个 handler 的 3 处——`IntegrationHandler` 组合 4 个 ws/realtime handler 只为注册路由(正常);`NotificationService` 持 WebSocketHandler 调一次广播(应传函数值,Bootstrap 里 `EventBroadcaster = BroadcastEvent` 就是对照);`MCPHandler` 调 `OAuthHandler` 6 个业务方法(把 handler 当 service,应抽 `OAuthFlowService`)。规则:handler 只依赖 service/store/Config,handler 之间只允许父子组合注册路由。

**能不能拆子包**:能,税比想象小。跨文件小写函数 65 个但几乎全聚在家族内(realtime、mcp oauth、mcpsessions),跨家族只 `parseCommaSeparated` 一个;17 个公共工具已导出;`logger`/`version` 包级变量各子包注入一次。按家族拆(inference、realtime、mcp、governance、logging、providers…)+ `handlers/internal/httputil`,`internal/` 让导出只在子树可见(Go 版 pub(super))。上游没做是没人动手。理想的上游 PR,fork 里别碰。

**构造 handler 三种写法混用**:启动时 `FindPluginAs` 抓一次(logging、governance,插件热重载后握旧指针);闭包每请求现查(cache、metrics,修过 bug 才改的,这才是对的);插件没有就不注册路由。

## 企业版探测点

`callbacks.(LogRedactionMappingResolverProvider)`、`(MCPLogRedactionMappingResolverProvider)`、`(GovernanceRouteOverridesProvider)` 三处类型断言;`BifrostContextKeyGovernancePluginName` 允许换名的治理插件;`oauth2IssuanceHandler` 是唯一不带中间件注册的(MCP token 签发必须公开)。

## 笔记 02 疑问已解

第二次 `NewWebSocketHandler`(:2107)有 `== nil` 守着,Bootstrap 走过来必跳过,是给不经 Bootstrap 的启动路径兜底。

下一步:第 6 步 `handlers/inference.go:1010 chatCompletion`。
