# EE 与上游接入重构：接入调研

日期：2026-09-28。基线：`develop@9e78d3bdb`，工作区 `bifrost-integration`，分支 `feat/ee-integration`。

这是设计调研，区分源码事实、离线实测和架构建议。未改生产代码，未启动或重启用户现有服务。三项并行调研不是 Workbench 的正式方案/代码评审。

## 1. 源码事实与入口

| 事实 | 可复核源码（路径相对仓库根；行号对应上述基线） |
|---|---|
| EE 将创建 identity/RBAC 放入 ConsoleAuthFactory；Bootstrap 后才 attach guardrails/branding/pricing 并包 RootGuard | `ee/internal/app/bootstrap.go:19`，`ee/internal/app/identity.go` |
| ConsoleAuthProvider 同时负责 middleware 与会话路由注册；Factory 接收完整 Server | `transports/bifrost-http/server/server.go:207`、`:249`、`:2746` |
| Bifrost 创建真实 Router，启用 SaveMatchedRoutePath；公共中间件转存匹配模板并删除原 router key | 同上 `:2688`、`:2444` |
| EE 另建路由 matcher；请求期 Prepare 再匹配、注入策略、最后改响应 | `ee/internal/rbac/host/routes.go:157`、`:193`、`:225`；`authorization.go:40`；`response.go:19` |
| 现有策略函数存在多个 context key，读取时反射检查 typed nil | `transports/bifrost-http/handlers/consolepolicy.go` |
| 原生推理注册有 RequestType middleware 的先后关系，不宜笼统外包一层共同认证 | `transports/bifrost-http/handlers/inference.go:791` 起；`server/server.go:2164` |
| OAuth issuance、skills serving、VK quota 存在故意不使用通用 middleware 的注册 | `handlers/mcpoauth2issuance.go:54`、`skills_serving.go:130`、`governance.go:1435` |
| UI 注册全局 GET wildcard | `transports/bifrost-http/handlers/ui.go:62` |
| 部分后台活动在加载配置和注册路由前已启动；关闭集中在 Start 的信号分支 | `transports/bifrost-http/lib/config.go:5186`、`:5195`；`server/server.go:2536`、`:2949` 起 |
| 当前生产 Bootstrap 调用方是上游 main、EE app；EE main 再调用 EE app | `transports/bifrost-http/main.go:154`；`ee/internal/app/bootstrap.go:33`；`ee/cmd/bifrost-http/main.go:113` |

来源归属：ConsoleAuthFactory / ConsoleAuthProvider 是本仓在 `fb578de64` 加入的扩展，本地 `upstream/dev@3a6d7936b` 没有。公开 Server/Router、分组注册入口和匹配模板能力是上游已有。不能把本仓补丁称为上游现成插件 API。

当前上游改动登记部分历史状态与源码不一致；此次以固定提交源码为实现事实，未改旧交付结论。

## 2. 注册接口的静态影响范围

当前注册函数仅需要 `GET`、`POST`、`PUT`、`DELETE`、`PATCH`、`HEAD`、`Handle`；实际调用不需要 Group、ANY、ServeFS 或 Router 配置字段。`*router.Router` 可自然满足这七个同签名方法。

- 仅 API 注册参数迁移：26 个 handler 文件及 server.go。
- 全部生产注册入口：36 个 handler 文件、integrations/router.go、server.go，共 38 个现有文件；另外需要新接口/适配文件和测试。
- `ExtensionRouter.RegisterRoutes`、`GovernanceRouteRegistrar` 等关联接口同步调整；EE 当前八个生产文件引用具体 Router，需要按最终保留的职责适配。
- 数量是类型引用/调用的静态盘点，不是总工时或变更行数估计。生命周期、类型化策略、响应 DTO 和 UI 兜底另计。

声明路径可作策略键；Lookup 接收的是请求路径而非注册模板，不能当 handler 按模板查询的 API。router v1.5.4 的 Handle 还会追加注册清单，Mutable 重绑不是合适的长期装配机制。

## 3. 必须保留的产品行为

产品来源：[账号认证已确认规则](../../product/需求/账号认证/方案讨论.md)、[角色权限产品方案](../../product/需求/角色权限/产品方案.md)。下表同时核对当前代码与测试，不将测试本身等同于完备证明。

| 范围 | 必须保留 |
|---|---|
| 管理接口 | 个人会话、账号状态、模块权限；下一次操作看到撤销/禁用；存储错误不能变允许 |
| 初始化与改密 | 部署令牌、旧管理员导入及旧会话失效；当前已实现的改密/会话撤销语义不在接入重构中顺手变更 |
| console bootstrap | 有效会话即可读取获准基本信息，含无角色/需改密账号；不依赖 Settings.View；字段白名单、空 JSON、Origin 和 Authorization header 约束保留 |
| 临时令牌与 OAuth | 现有六种 method/path 组合的范围、资源、期限和开关校验；不能扩展成管理员；401/403 的特定回退不得扩展到 503 |
| WS | 精确 Origin；Cookie 或一次性短期票据；连接期间按消息/心跳重验会话、Notifications.View 和受众；查询票据不进日志 |
| 敏感操作 | 编辑权限不自动包含查看秘密；改变凭据流向、Proxy/TLS/OAuth 边界需额外权限；检查在保存、试连、模型发现、发送之前 |
| 旧配置兼容 | 旧认证字段只读投影；原值可随普通设置回传；改变认证配置或增加白名单时整请求拒绝，无部分写入；正确恢复保留标记 |
| 推理、内容安全、定价 | Cookie 不替代 VK；保持协议认证；内容安全先于缓存且拒绝不回退；定价使用真实 DB/模型目录，保留手工覆盖优先级 |

## 4. 现有测试的证明边界

- `TestEveryManagedRouteAuthorization` 的最小权限断言主要覆盖 Prepare；包装链测试使用全权限和模拟 204 handler，不能直接证明真实 handler 在最小权限下可完成业务。
- `TestKnownProtocolAndSelfManagedRoutesRemainOwned` 证明 RootGuard 放行给模拟 handler，不证明后面的协议认证正确。
- 路由清单数量、hash 或 AST 检查可以防止遗漏，但不能证明副作用发生时机、敏感响应覆盖或角色撤销。
- 响应投影只对当前已知容器/字段有测试；迁移到 DTO 时仍须检查列表、详情、导出、嵌套与错误路径。

## 5. 离线路由可行性探针（实测）

Go 1.27.1，router v1.5.4，fasthttp v1.71.0；全部本地缓存、无网络、无监听端口。认证和 SPA 使用刻意简化的 stub，只验证 API 可行性及路由语义。

临时模块：`/private/tmp/bifrost-route-registrar-probe.qn3MJS`。源文件、依赖声明和原始输出保存在 [路由探针附件](EE与上游接入重构-路由探针/README.md)。部分传递依赖替换为本地已有版本，见附件；不是仓库锁定依赖下的完整构建。

实测五项测试全部通过：

1. 七方法接口由真实 Router 实现，包装器每个快捷方法都经过自己的 Handle。
2. 直接裸注册及 `/api/items/{id?}` 的两个 optional alias 均绑定同一访问策略；无模拟凭证 403，有凭证 200。协议自管 handler 不需要模拟控制台凭证。
3. 未分类的新接口触发启动验证错误，且没有安装该 handler。
4. 下表验证移除 SPA 全局 wildcard 后的实际 Router 行为。
5. 同 method 参数与 catchall 的 Lookup 模板字面量歧义及正则模板返回 nil 均已复现。这是合成例子，不表示当前 GenAI 存在该同 method 冲突。

| 请求 | 全局 GET wildcard | NotFound UI fallback |
|---|---:|---:|
| GET 已知 API | 200 | 200 |
| POST 已知只读 API | 405 | 405 |
| GET 未知 API | 200（SPA） | 404 |
| POST 未知 API | 405 | 404 |
| GET workspace 深链 | 200 | 200 |
| POST workspace 深链 | 405 | 404 |
| HEAD 未知 API | 405 | 404 |

探针使用示意性保留前缀判断 fallback，**没有证明架构稿所提完整前端路径范围已实现**。完整静态资源、前端深链、HEAD、编码路径、CORS、middleware 顺序、真实认证、流式传输与 WS 均须后续验证。

## 6. 结论与限制

注册时绑定方案的基础 API 可行；真实 Router 可以独自承担匹配、optional alias 与 405。显式生命周期和类型化敏感操作策略仍是基于源码的设计建议，没有运行 EE 集成验证，不能宣布重构完成。
