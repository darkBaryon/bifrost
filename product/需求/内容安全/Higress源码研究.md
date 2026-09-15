# Higress 内容安全源码研究

> 用途：核实安全插件如何送检、判定和处置，补充[阿里云与 Bifrost 功能对照](阿里云与Bifrost功能对照.md)中的文档证据。
> 日期：2026-09-16。状态：固定提交源码阅读；测试范围见末节。未运行完整网关、未调用阿里云审核服务或真实判官模型。
> 仓库：[higress-group/higress](https://github.com/higress-group/higress)，旧地址 `alibaba/higress` 已重定向。
> 固定提交：`faccaad586a3cdc9e85dc7fa39358ff31a6453b2`，提交日期 2026-09-10。下列代码链接均固定到该提交，不代表已发布插件镜像或阿里云托管网关版本。

## 一、开源到哪一层

Higress 仓库公开，根目录采用 [Apache-2.0 许可证](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/LICENSE)。本次相关实现分成三个插件：

| 插件 | 具体做什么 | 检测逻辑在哪里 |
|---|---|---|
| `ai-security-guard`（Go） | 抽取输入输出，调用阿里云服务，按风险等级放行、拦截或应用请求脱敏结果 | 网关接入与处置代码公开；阿里云检测引擎不在这个插件里 |
| `qwen3guard`（Go） | 调 Qwen3Guard-Gen，解析安全等级，再决定是否拒答 | 插件通过 OpenAI 兼容接口访问独立部署的模型服务 |
| `ai-data-masking`（Rust） | 敏感词检查、正则/Grok 匹配、请求替换及可选响应还原 | 规则处理在插件本地执行 |

源码入口：[阿里云接入插件](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/main.go)、[Qwen 调用与结果解析](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/qwen3guard/guard.go)、[本地数据保护](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-rust/extensions/ai-data-masking/src/ai_data_masking.rs)。

**Higress 的行为不能直接补成阿里云托管产品的事实。** 两者的版本、配置和插件装配未建立对应关系。阿里云在线文档的未知项仍保留。

## 二、阿里云接入插件怎样工作

以下以 `MultiModalGuard` 的 OpenAI 文本请求路径及共用文本响应路径为主；图片、MCP、Embedding 等其他路径不能套用同一张表。

```text
请求/响应进入 Hook
  → 检查开关、接口类型等条件
  → 提取指定 JSON 字段中的文本
  → 暂停转发，调用外部审核服务
  → 解析风险等级，结合消费者配置与阈值判定
  → 放行 / 拒答 / 请求正文替换
  → 记录检测事件、耗时和处置结果
```

| 环节 | 固定提交中的实际行为 | 源码 |
|---|---|---|
| 提取内容 | 默认请求路径是 `messages.@reverse.0.content`，即最后一条消息；不能说成“最后一条 user”。输入输出路径可配置，响应另有备用提取路径 | [默认配置](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L68) |
| 调用服务 | 生成含 `content`、`sessionId` 的参数，使用 AK/SK 签名后发 HTTP 请求；输入输出可以使用不同审核 Service | [请求构建](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/common/request_builder.go#L176) |
| 分段送检 | 普通文本长内容按 `LengthLimit=1800` 切段，逐段调用；这里切的是 Go 字符串字节，不是 1800 个汉字 | [请求分段](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/multi_modal_guard/text/openai.go#L58)、[响应分段](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/common/text/openai.go#L285) |
| 选择处置配置 | 消费者维度配置 → 消费者总配置 → 全局维度配置 → 全局总配置 → 默认 `block`；`mask` 只适用于敏感数据维度，其他维度会转为 `block` | [动作解析](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L787) |
| 合并风险 | 判定结果为 `Pass / Mask / Block`；有阻断风险优先阻断，其次才是脱敏，否则通过。依据风险等级与阈值判断，不能只读服务返回的一个 `Suggestion` 字段 | [风险判定](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L852) |

代码按 `Action`、API 类型与 Provider 类型分派到具体处理器。本次没有看到把这些插件统一成一套通用 `Detector` 接口的实现；我方是否定义统一接口仍是自己的架构选择。

### 各种情况怎么处理

| 情况 | 具体处理 |
|---|---|
| 未开启检查、提取不到内容 | 相应路径跳过检测；普通响应在 HTTP 状态不是 200 时也会跳过 |
| 请求检测通过 | 所有文本段检查完成后恢复请求，继续调用业务模型 |
| 请求命中阻断 | 直接返回拒答，不继续业务模型调用 |
| 请求命中脱敏 | 从检测结果提取 `Ext.Desensitization`，替换请求对应字段后继续；没有可用脱敏文本或替换失败则退回阻断 |
| 非流式响应通过/阻断 | 通过则恢复原响应；阻断则发送拒答 |
| 普通文本请求或非流式响应的检测调用失败、业务 Code 非 200、结果解析失败 | 记录错误并恢复转发，即“检测失败放行”；这不代表内容被判安全 |
| 构造拒答正文失败 | 所读文本路径会恢复转发；流式路径有明确重新注入原缓存的分支 |
| 输出检测返回 `Mask` | **共用文本响应处理器把它视为可接受并放行，没有应用脱敏文本。不能据请求实现推定输出也会脱敏** |

依据：[请求处理及异常分支](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/multi_modal_guard/text/openai.go#L58)、[响应处理及异常分支](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/common/text/openai.go#L209)、[`Mask` 被视为可接受](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L952)。

拒答由插件构造，默认状态码 **200**、文案“很抱歉，我无法回答您的问题”，可配置。OpenAI 格式有 legacy 和 structured 两种：legacy 把拒答信息 JSON 放进 content 字符串，structured 将可读文案和 `x_higress_guardrail` 元数据分开；流式拒答包含 SSE 内容、结束事件与 `[DONE]`。因此不能只用 HTTP 200 判断有没有拦截。[默认值](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L55)、[拒答生成](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/config/config.go#L982)。

### 流式实际是分批检查、分批释放

`ai-security-guard` 的流式文本响应路径：

1. 缓存上游 SSE 片段，满足缓冲条件或收到流结束标记时送检。
2. 这一批通过，就把这一批原 SSE 注入下游，然后继续检查后续批次。
3. 某批阻断，注入拒答并结束下游流，抑制后续内容；此前已经发出的片段无法收回。

这与 BF 文档中“匹配可阻断规则时等完整回答检查完再释放”不同，也不同于阿里云托管网关文档的“响应检查使流式变成非流式”。`BufferLimit` 同时用于队列条数触发和本批文本 rune 数停止条件，不能简化成“严格每 1000 字检查一次”。[流式实现](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/ai-security-guard/lvwang/common/text/openai.go#L45)。

**尚未运行验证的边界：** 流式检测失败分支与非流式不同：部分错误仅在已收到结束标记时调用 `ResumeHttpResponse`；同步发起调用失败的分支也未在当地重置 `during_call`。仅凭该文件不能确认缓存能完整释放、连接能正常结束，因此暂不归纳成“所有流式异常都会正常放行”。需要结合 WASM SDK 与真实网关验证。

## 三、另外两个插件可参考什么

### Qwen3Guard：换一个检测服务，流程仍然类似

- 组装模型请求，调用独立部署的 `/v1/chat/completions`；默认模型名 `Qwen/Qwen3Guard-Gen-4B`。
- 解析 `Safety` 等字段，根据 `Safe / Controversial / Unsafe` 与配置阈值决定阻断。
- 输出检查把原始提示词与回答一起送检。流式累计回答文本，达到未检测字符阈值等条件后检查，再释放待发 SSE；并非整段回答生成结束后才开始交付。
- 所读调用或结果解析错误路径放行。流式超过 `max_body_bytes` 也会转为放行，默认上限 10 MiB。

依据：[配置](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/qwen3guard/config.go)、[模型请求和判定解析](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/qwen3guard/guard.go)、[请求响应与流式处理](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-go/extensions/qwen3guard/main.go#L61)。

### 本地数据保护：规则匹配、替换与还原

`ai-data-masking` 的 `replace_request_msg` 遍历替换规则，对匹配值执行替换或哈希；启用 `restore` 时记录“替换值 → 原值”，在模型响应中尝试还原。它与“审核服务返回一个风险等级”是不同的执行方式。[替换与映射](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-rust/extensions/ai-data-masking/src/ai_data_masking.rs#L431)、[响应处理](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-rust/extensions/ai-data-masking/src/ai_data_masking.rs#L665)。

插件 README 明示：流式跨片段可能还原失败，也可能先返回部分敏感词。故存在缓冲窗口不等于已经解决所有跨片段问题。[已知限制](https://github.com/higress-group/higress/blob/faccaad586a3cdc9e85dc7fa39358ff31a6453b2/plugins/wasm-rust/extensions/ai-data-masking/README.md#L145)。本节仅作实现参考，不把 D2 或响应还原加入我方首期。

## 四、对当前开发讨论的意义

源码足以参考四块代码职责：**内容提取、检测调用、结果判定、动作执行**。网关侧主体确实是这些前后置处理；外部检测引擎另行接入，本地词语/正则则可直接执行。

目前仍沿用[已确认方向](调研总结.md#当前开发与处置方向)：先公共框架、后具体检测器；用户随后确认本轮普通请求接入，输出流检查明确不支持，完整流检测后交付留待后续。Higress 的分批流式、默认失败放行、默认 200 拒答和脱敏能力都只是竞品实现事实，不能自动成为我方规则。

## 五、验证记录

- 已 clone 官方仓库并固定上述提交，逐路径阅读代码及相关测试。
- 在两个 Go 插件各自模块执行 `GOWORK=off go test ./...`，均通过：`ai-security-guard` 主包约 141 秒，配置、工具与共用处理包也通过；`qwen3guard` 约 0.01 秒。这些是仓库现有单元测试，使用模拟宿主/调用结果，不是云端联调。
- 未执行 Rust 插件测试，未启动 Envoy/WASM 完整网关，未实测检测准确率、延迟和云端故障。因此单元测试不能证明上述流式边界在部署环境中正确。
