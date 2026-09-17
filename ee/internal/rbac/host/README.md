# Bifrost接口的角色权限接入

已有管理接口先检查账号角色，再执行原来的业务代码。登录由`ee/internal/host`检查，本包调用rbac根包读取权限。

## 请求怎么走

```text
请求 → host检查登录 → authorization检查接口权限
                       ↓
                  request检查特殊操作
                       ↓
                  Bifrost原handler执行
                  （保存前调用需要的检查）
                       ↓
                  response隐藏无权查看的字段 → 返回
```

通知长连接建立后，每条消息都经`notifications.go`重新读取权限：只发给当前账号能看的通知，登录或通知权限失效则断开。

## 文件分工

| 文件 | 做什么 |
|---|---|
| [authorization.go](authorization.go) | 请求处理入口：读取权限、调用前后检查、记录失败位置 |
| [routes.go](routes.go) / [routes.txt](routes.txt) | 登记每个方法和路径的权限，启动时检查是否漏了接口 |
| [request.go](request.go) | 把请求分给对应检查，并把保存前的检查传给原handler |
| [response.go](response.go) | 把返回内容分给对应处理，隐藏字段，拒绝无法识别的结构 |
| [providers.go](providers.go) | 隐藏厂商凭据，更新时保留原值；检查厂商和密钥配置的凭据目标变化 |
| [logs.go](logs.go) | 分开控制日志概要、正文、敏感查询和导出 |
| [webhooks.go](webhooks.go) | 控制向外部地址发送事件通知；包含日志正文时额外检查权限 |
| [plugins.go](plugins.go) | 检查插件操作影响哪些功能，更新时合并旧配置、恢复隐藏字段并检查凭据目标 |
| [settings.go](settings.go) | 比较设置真正改了什么，再检查对应功能的权限；按宿主规则解析代理地址并隐藏认证信息 |
| [credentials.go](credentials.go) | 比较旧凭据和新目标，统一检查敏感权限；含MCP的TLS/OAuth边界 |
| [plugincredentials.go](plugincredentials.go) | 处理内置上报插件的地址与凭据组合，兼容旧配置格式 |
| [notifications.go](notifications.go) | 按角色筛选通知，长连接每条消息重新检查权限 |

## 修改时要知道

- `managed`接口由本包检查；身份、角色、推理和协议接口仍由各自处理器负责。
- 清单登记818条已知路由，其中205条使用本包的角色授权；运行时只匹配实际注册的路由。漏登记会阻止启动，不按GET/POST猜权限。
- 清单选择检查和返回处理策略；具体字段及列表/详情/更新等分支仍在各功能文件中。新增接口要核对对应策略能否处理其请求与响应，不能只在清单增加一行。
- `/metrics` 需要 Logs.View，保留 Prometheus 文本格式；指标未启用时沿用上游 404。
- 检查必须早于保存或投递；无法理解的敏感响应不能直接返回。
- 携带旧凭据更换地址、代理或TLS信任设置需要 `Security.ChangeCredentialDestination`，同时仍需所属模块管理权限。主管理员默认拥有，普通角色可单独授予；不开放凭据明文读取。厂商密钥的地址覆盖与新建密钥继承旧请求头也检查；MCP的已保存用户令牌及令牌交换不能因换了可见密钥就视为全新凭据。
- `<redacted>`表示保留原值，不能直接保存为新密钥或地址；没有旧值时拒绝更新。
- 插件和设置可能影响其他功能，例如修改日志正文保存方式还需要日志权限。
- 新内置插件或新配置字段需要补上权限规则；路由、设置字段和内置插件目录有自动核对；Key字段由providers.go的keyConnection显式识别，上游升级时还须人工核对新增目标和凭据字段。

## 验证

从`ee`目录执行`GOWORK=off go test -race ./cmd/... ./internal/...`。完整隔离冒烟使用`ee/scripts/rbac-smoke.py`，覆盖SQLite和PG双节点HTTP/WS。PostgreSQL 模式通过 `RBAC_TEST_POSTGRES_DSN` 指定本地测试库；脚本创建和删除自己的临时数据库。历史期1/期2脚本已移除，需要复跑旧候选时分别从 `32730c2cb` / `4420d1b12` 读取。
