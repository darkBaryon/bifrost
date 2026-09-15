# 阿里云与 Bifrost：网关行为及检测能力对照

> 调研日期：2026-09-15；处置行为补充核实：2026-09-16。用途：对照检测能力、配置及各种情况下的处理方式，为框架和产品规则提供依据。
> 状态：官方资料核实；未调用检测 API、未实测中文效果、未运行商业版。本文“待确认”项尚未形成自有产品规则。
> 版本：在线文档以访问日为准；Bifrost 指上游企业版，其能力不代表本仓 EE 已实现。
> 前置材料：[第一轮功能地图](竞品调研.md)；本篇承接后续聚焦研究，其他竞品仅作补充。

源码补充：[Higress 源码研究](Higress源码研究.md)已固定提交核对阿里云接入、Qwen3Guard 和本地数据保护插件。其分批流式及失败处理是 Higress 该提交的行为，不能替代本篇阿里云托管产品的未知项。

## 检测项目目录

2026-09-15 用户明确按阿里云 AI 安全护栏与 Bifrost 企业版的检测能力取并集。以下按“查什么”去重，保留为调研目录。截至 2026-09-16，已确认 D1/D3/D4/D5 做、D2 首期暂缓、D6–D9 不做；最新决策见[当前功能范围](调研总结.md#当前功能范围)，详细规则及实现方式尚未确定。

检测项目统一使用下表的 D 编号及名称；策略、处置、日志等共用配套规则不另编号，见[统一口径](调研总结.md#统一口径)。

| 编号 | 检测项目 | 查什么 | 调研来源 |
|---|---|---|---|
| D1 | 违规与有害内容 | 色情、暴力、歧视、辱骂等风险内容 | [阿里内容合规](https://help.aliyun.com/zh/document_detail/2873209.html)、[BF 外部审核服务](https://docs.getbifrost.ai/integrations/guardrails/azure-content-safety) |
| D2 | 个人与企业敏感信息 | 手机号、证件号等个人信息，以及企业敏感数据；具体实体覆盖随检测器而异 | [阿里敏感内容](https://help.aliyun.com/zh/document_detail/2878231.html)、[BF 检测器](https://docs.getbifrost.ai/enterprise/guardrails) |
| D3 | 密钥与凭据泄漏 | API Key、访问 Token、私钥等凭据 | [BF 密钥检测](https://docs.getbifrost.ai/enterprise/guardrails/secrets-detection) |
| D4 | 提示词攻击 | 诱导模型忽略规则、越权或泄露信息的指令，包括藏在外部内容中的攻击 | [阿里提示词攻击](https://help.aliyun.com/zh/document_detail/2873209.html)、[BF 直接/间接攻击检测](https://docs.getbifrost.ai/integrations/guardrails/azure-content-safety) |
| D5 | 自定义业务规则 | 企业规定的禁用内容、固定词语/格式，以及需要理解语义的业务限制 | [阿里自定义检测](https://help.aliyun.com/zh/document_detail/2977210.html)、[BF 正则](https://docs.getbifrost.ai/enterprise/guardrails/custom-regex)、[BF 模型判官](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails) |
| D6 | 恶意链接 | 钓鱼网址、危险跳转等风险链接 | [阿里恶意 URL](https://help.aliyun.com/zh/document_detail/2873209.html)、[BF 外部服务能力](https://docs.getbifrost.ai/enterprise/guardrails) |
| D7 | 恶意文件 | 文件中隐藏的危险脚本、宏或攻击内容 | [阿里恶意文件](https://help.aliyun.com/zh/document_detail/2873209.html) |
| D8 | 模型幻觉 | 回答中可能虚构或不准确的信息；不代表保证回答真实 | [阿里配置](https://help.aliyun.com/zh/document_detail/2878231.html)、[BF 外部评估能力](https://docs.getbifrost.ai/enterprise/guardrails) |
| D9 | 受保护内容 | 输出中可能包含的受版权保护材料；检测边界取决于服务，不作为法律认定 | [BF Protected Material](https://docs.getbifrost.ai/integrations/guardrails/azure-content-safety) |

**相邻功能保留**：阿里的“数字水印标识”是在生成图像中加入来源标识，归入内容标识候选，不混入上述风险检测项。[阿里水印说明](https://help.aliyun.com/zh/document_detail/2873209.html)

归并规则：密钥是敏感信息的一类，因 BF 提供独立检测器且代码场景明确，保留为单独子项；自定义词库、正则和自然语言规则统一归 D5，其中正则也可用于 D2/D3。LLM/MCP、输入/输出、文本/图片/文件分别属于检测位置与内容形式；拦截、脱敏、代答属于处置动作。后续在每个 D 项下记录这些属性，避免把同一能力重复列成多个功能。

## 一、网关层：一张行为表

| 环节 | 阿里云 AI 网关资料 | Bifrost 企业版资料 | 我们需要定下的规则 |
|---|---|---|---|
| 哪些调用送检 | Model API 开启防护，按消费者匹配 | 规则按 VK、团队、模型等条件匹配 | 首期支持哪些条件；多规则命中怎么处理 |
| 检查哪个阶段 | 请求、响应分别配置 | 输入、输出或两者；LLM/MCP 分目标 | 首期覆盖的接口和阶段 |
| 使用哪个检测器 | 引用护栏 Service | 规则关联可复用检测档案 | 首期接哪些检测器 |
| 检查多少内容 | 网关接入页未充分定义历史消息范围 | 可配置输入历史轮数及整体/逐轮评价 | 当前消息、历史、system 和工具内容的范围 |
| 命中后怎么办 | 检查策略与拦截等级分开；支持观察 | 按检测器配置检测、拦截、脱敏等动作 | 处置建议是否可覆盖；拒答格式 |
| 检测异常怎么办 | 所读网关页面未明确完整异常矩阵 | 至少 Prompt Guardrails 明确运行异常放行 | 超时、限流、无权限、结果解析失败分别如何处理 |
| 如何查原因 | 检测结果进入日志 | 查看规则、阶段、档案及检测信息 | 必须记录的字段、正文是否保留 |

表中官方事实来源：[阿里网关接入](https://help.aliyun.com/zh/document_detail/2980055.html)、[Bifrost 护栏规则与档案](https://docs.getbifrost.ai/enterprise/guardrails)、[Bifrost 判官异常行为](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails)。最后一列是需求讨论项，不是已批准配置。

竞品的流式交付行为：BF 匹配可拦截的输出规则时，缓存完整流，生成和检测结束后才决定交付；默认连续释放缓存事件，不人为增加回放间隔。阿里 AI 网关文档则说明响应检查会使流式变为非流式。我方最新范围已由用户选择方案 1：本轮开启输出检查时，在调用模型前拒绝流式请求；仅输入检查允许流式。此前讨论的完整缓存后释放不在本轮实现。[Bifrost 流式说明](https://docs.getbifrost.ai/enterprise/guardrails#streaming-output-guardrails)、[阿里网关说明](https://help.aliyun.com/zh/document_detail/2980055.html)

### 各种情况怎么处理

本节为 2026-09-16 官方资料核对，未运行企业版、未调用付费 API。阿里“检测服务给出的建议”和“AI 网关执行的动作”分别标明；表中的厂商行为不直接成为我方默认规则。

| 情况 | 阿里云 | Bifrost 企业版 |
|---|---|---|
| 检测完成，没有风险 | 多模态检测 API 可返回 `none` / `pass`，由接入方继续业务。[接口](https://help.aliyun.com/zh/document_detail/2932956.html) | 检测允许后继续模型调用或返回回答。[总览](https://docs.getbifrost.ai/enterprise/guardrails) |
| 有风险，但只想观察 | 网关观察模式只检测记录、不拦截；护栏敏感标签也可配置观察。[网关](https://help.aliyun.com/zh/document_detail/2980055.html)、[护栏配置](https://help.aliyun.com/zh/document_detail/2878231.html) | `detect_only` 记录发现，不拦截、不改原文。[动作](https://docs.getbifrost.ai/enterprise/guardrails/redaction#actions) |
| 输入命中拦截条件 | 网关按消费者、防护维度和等级拦截请求；具体客户端错误格式在所读页面未明确。[网关](https://help.aliyun.com/zh/document_detail/2980055.html) | 输入拦截阻止模型调用；Prompt Guardrails 返回带简短原因的护栏干预结果。[判官](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails#decisions-and-failure-behavior) |
| 输出命中拦截条件 | 网关拦截模型返回；开启响应检查会改变流式交付。[网关](https://help.aliyun.com/zh/document_detail/2980055.html) | 可拦截输出规则持有完整流，命中后返回干预结果，原回答不交付。[流式](https://docs.getbifrost.ai/enterprise/guardrails#streaming-output-guardrails) |
| 命中敏感内容，但希望处理后继续 | 护栏标签支持掩码脱敏，API 有 `mask` 建议；多模态 API 的 `Ext` 未统一定义替换正文或位置，需按具体 Service 核对。[配置](https://help.aliyun.com/zh/document_detail/2878231.html)、[接口](https://help.aliyun.com/zh/document_detail/2932956.html) | 支持的检测器返回位置，由 BF 脱敏；部分外部服务直接返回改写正文。原位替换、掩码、哈希等随模式配置。[脱敏](https://docs.getbifrost.ai/enterprise/guardrails/redaction) |
| 不改业务内容，只处理日志里的敏感值 | 所读阿里材料未明确等价的“仅日志脱敏”模式。 | `logs_only` 保持运行内容不变，日志与相关导出内容脱敏；运行时脱敏也会影响日志。[模式](https://docs.getbifrost.ai/enterprise/guardrails/redaction#redaction-modes) |
| 拦截后想返回一段预设回答 | 内容合规可按风险标签绑定代答库。文本接口明确提供 `Advice.Answer`；不能把这个字段直接套到多模态 API，也未确认 AI 网关自动代答。[代答库](https://help.aliyun.com/zh/document_detail/2878233.html)、[文本接口](https://help.aliyun.com/zh/document_detail/2875414.html) | 判官的拦截理由属于错误说明；所读材料未发现等价的通用代答库。外部服务改写正文另有机制，不能当作统一代答能力。[判官](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails)、[外部改写示例](https://docs.getbifrost.ai/integrations/guardrails/crowdstrike-aidr) |

以上处置属于共用行为。D1/D4/D5 的语义风险通常由所选服务作出风险判断，D3/D5 的固定匹配可返回命中片段；是否启用脱敏或代答仍是独立的产品决定，不能把每种动作强制套给所有检测项目。

### 检测失败与无法处理的情况

| 情况 | 阿里云 | Bifrost 企业版 |
|---|---|---|
| 检测超时、连接失败、服务端失败 | 所读 AI 网关文档未明确最终放行还是拦截，不能从护栏 API 错误码推断。 | **Prompt Guardrails** 超时、被服务商拒绝或解析失败时记录失败并继续业务。其他检测器的默认策略未全面确认。[判官失败规则](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails#decisions-and-failure-behavior) |
| 没权限、限流等调用错误 | 多模态 API 列出 `403 NoPermission`；这表示检测调用未授权，不是内容违规。旧文本接口另有 QPS 限流说明，不能把旧接口额度当作新接口额度。[多模态接口](https://help.aliyun.com/zh/document_detail/2932956.html)、[文本接口](https://help.aliyun.com/zh/document_detail/2875414.html) | 判官请求被拒绝归入上述失败放行；外部 AIDR 文档将非 2xx 归入调用失败，但未在该页明确最终放行策略。[判官](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails)、[AIDR](https://docs.getbifrost.ai/integrations/guardrails/crowdstrike-aidr) |
| 返回格式错误、缺少结果、返回的改写内容数量不对 | 所读材料未明确 AI 网关如何处置这些异常。 | AIDR 将缺少结果、格式错误、改写数量不符记为调用失败；不能把缺失结果认作检测通过。[AIDR](https://docs.getbifrost.ai/integrations/guardrails/crowdstrike-aidr) |
| 多个实现都想改同一份正文 | 多模态接口有总体和分项建议，但所读材料未明确总体建议的完整优先级，也未明确多份改写的合并规则。 | 同阶段同时出现外部改写正文与 BF 脱敏结果，或多份外部改写正文时，拒绝处理并拦截，不猜测合并方式。[改写归属](https://docs.getbifrost.ai/enterprise/guardrails/redaction#bifrost-managed-vs-provider-managed-rewrites) |
| 检测器不能检查该类内容 | 按 Service 支持的内容形式调用；网关接入页限定文本生成和图片生成，未说明所有不支持输入的处置。 | 判官只检查提取出的文本，纯图片/文件不会调用判官；不能把没调用当作检查通过。[判官限制](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails#troubleshooting) |
| 客户端取消、生成中途失败、回答超过检测长度或缓存上限 | 本轮所读页面未形成完整处置约定。 | 本轮所读企业版页面未形成完整处置约定。 |

“未明确”表示缺少可核验的网关最终行为，不表示厂商没有处理；需要固定版本和真实调用补证。本轮不将普通模型请求的重试、fallback 机制直接解释为安全检测器的失败策略。

### 配置与多规则的处理

- **阿里网关**：检测开关、检查范围、拦截策略要配合生效；只配置拦截而没有检查不会工作。开启检查却未配置拦截时，有默认低防护等级。消费者匹配规则优先于“任意消费者”，同等命中再按配置顺序，不能理解为所有冲突一律“取最严”。低防护等级只拦高风险，中等级拦中高风险，高等级拦全部有风险内容。[网关生效规则](https://help.aliyun.com/zh/api-gateway/ai-gateway/user-guide/content-security-protection)
- **BF**：最新总览说明，同一规则关联的检测档案按配置顺序执行，某个档案干预或失败后停止该规则。不过配置部署页仍写多个 provider 并行，材料存在冲突；多条规则的全局停止顺序也未完整核实。[总览](https://docs.getbifrost.ai/enterprise/guardrails#creating-rules)、[配置部署页](https://docs.getbifrost.ai/deployment-guides/config-json/guardrails)
- **误报处理**：阿里可调整风险标签开关、词库和阈值；BF 密钥检测可忽略命中值中包含指定子串的发现。两者都是检测配置调整，不等同于“这次请求有一项白名单命中就跳过全部检查”。[阿里配置](https://help.aliyun.com/zh/document_detail/2878231.html)、[BF 密钥忽略配置](https://docs.getbifrost.ai/enterprise/guardrails/secrets-detection#false-positive-allowlist)

### 返回给客户端的错误格式仍需核实

BF 总览展示过 `446 / guardrail_violation` 的拦截示例，以及 `246` 的日志警告示例；AIDR 专页则明确写拦截返回 `400 / guardrail_intervention`。这些说明不能合并成统一的企业版 HTTP 契约，也不能当作所有 SDK/SSE 接入实测结果。[总览响应示例](https://docs.getbifrost.ai/enterprise/guardrails#guardrail-response-handling)、[AIDR 拦截响应](https://docs.getbifrost.ai/integrations/guardrails/crowdstrike-aidr#blocked-error-response)

阿里检测 API 的 HTTP 状态、响应体 `Code/Message` 与内容风险结论是不同信息；检测请求成功也可能返回阻断建议。AI 网关如何映射成客户端错误码、错误体，所读页面未明确。[检测接口](https://help.aliyun.com/zh/document_detail/2932956.html)

### 对我方框架的建议（待落实为产品规则）

检测执行情况、风险结论、处置动作分别表达：

- **执行情况**：已完成、未执行、失败。失败进一步记录超时、鉴权、限流、格式错误或能力不支持等原因；只有实际完成检测才填写风险结论。
- **风险结论**：是否命中、命中的 D 项和细分类别、等级及可用的片段位置。
- **处置动作**：继续原请求/响应、拦截、改写后继续；是否记录及是否仅日志脱敏另行配置。

先实现共同调用边界，不要求每个检测器都能脱敏或代答。后续需要确定：失败时放行还是拦截及是否按原因区分、首期动作范围、多结果汇总与改写冲突、客户端错误格式；流式输出按完整检测后交付细化容量、取消和失败行为。以上是框架设计建议，不是本轮已经写入代码的功能。

## 二、检测能力：按它判断什么来区分

| 能力 | 阿里云护栏 | Bifrost 企业版 | 需要验证的实际效果 |
|---|---|---|---|
| 内容审核 | 内容合规维度，识别违规内容类别 | 接外部审核引擎，也可使用模型判官 | 中文误拦/漏检、代码与正常知识讨论 |
| 敏感信息 | 个人及企业敏感内容检测，可按标签配置处置 | 正则 PII 模板及外部 PII 服务 | 中国手机号、身份证、地址及业务字段覆盖 |
| 密钥泄漏 | 归入敏感内容路线；本轮未核实完整密钥类型清单 | 专门的 Gitleaks 本地检测器 | 真实格式与测试占位值的区分、代码片段覆盖 |
| 提示词攻击 | 专门的提示词攻击维度 | 接具备该能力的外部服务 | 间接注入、长上下文及工具返回中的攻击 |
| 自定义语义规则 | 自定义检测 Agent：标签、描述和判定模型 | Prompt Guardrails：业务策略和判官模型 | 业务规则准确性、额外调用成本与耗时 |
| 自定义固定规则 | 自有词库及检测项设置 | Custom Regex，逐条配置匹配与动作 | 词语变体、边界误匹配、中文规则 |
| 扩展能力 | URL、幻觉、文件、水印等；按 Service 提供 | 能力随外部检测器变化 | 每项的发布状态、模态及返回信息 |

阿里能力分类依据[产品功能](https://help.aliyun.com/zh/document_detail/2873209.html)及[检测配置](https://help.aliyun.com/zh/document_detail/2878231.html)；Bifrost 外部能力依据[检测器总览](https://docs.getbifrost.ai/enterprise/guardrails)。本地密钥、正则和模型判官分别见第四节；此表不表示两家效果相等。

## 三、阿里云的配置具体是什么

### 3.1 Service 是一份可调用的检测配置

管理员先选择输入/输出及模态对应的 Service，再启用防护维度、调整细分标签。可以使用官方基准规则，也可以复制成独立 Service：前者随基准更新，后者保留独立配置。配置修改存在生效延迟。敏感内容与提示词攻击单独计费；配置页将恶意 URL、模型幻觉标为公测。[防护配置说明](https://help.aliyun.com/zh/document_detail/2878231.html)

调用时，通过 `Service` 指定服务，`ServiceParameters` 提交文本或图片/文件地址等参数。同一个 API 可返回多类检测结果；API 定义包含某种类型不代表所有 Service 都启用了它。[MultiModalGuard 接口](https://help.aliyun.com/zh/document_detail/2932956.html)

### 3.2 返回结果已经带有处置建议

接口区分调用状态与检测结论：`Code/Message` 表示调用状态，检测数据包含风险类型、等级、标签及部分置信信息；`Suggestion` 可为 `pass`、`block`、`watch`、`mask`。这些是供调用方执行的建议，成功调用也可能检出风险。[接口返回结构](https://help.aliyun.com/zh/document_detail/2932956.html)

**接入待验证项**：概览接口将 `Ext` 定义为扩展信息，不能仅凭 `mask` 推定每种检测都会返回可直接替换的正文或完整片段位置。首期若需要脱敏，应逐类确认片段定位、字符偏移及转换结果。

### 3.3 敏感内容可以按标签配置动作

阿里配置页说明，敏感数据标签可选择脱敏、阻断或观察。也就是说，不只是一个全局的“检测开关”，不同类型可有不同处理要求。[敏感内容配置](https://help.aliyun.com/zh/document_detail/2878231.html)

### 3.4 自定义检测 Agent 是模型判定业务规则

管理员选择模型，为每个标签写检测标准，可给出例子；配置后先测试，再发布。多个标签形成分类任务，服务组织提示词和输出格式。可选模型和自定义描述长度会影响计量；这类调用另行收费。[自定义检测 Agent](https://help.aliyun.com/zh/document_detail/2977210.html)

### 3.5 运营功能围绕规则维护与效果检查

词库用于补充固定匹配要求；代答库配置被拦截内容的替代答案；在线测试验证样本，结果查询及风险报表用于排查和观察趋势。[功能集](https://help.aliyun.com/zh/document_detail/2873210.html)

本轮未实测：所有细分标签、客户样本上的效果、检测服务的实际留存与收费，以及各地域/Service 的差异。

## 四、Bifrost 企业版补充的三类检测器

### 4.1 Secrets Detection：本地查密钥

在进程内使用 Gitleaks 规则扫描文本，无需外部检测账号。可选只检测、拦截、脱敏，并可设置误报忽略关键词。文档展示云凭据、代码托管 Token、模型 API Key 和私钥等类型。它不直接检查图片像素或二进制文件内容。[Secrets Detection](https://docs.getbifrost.ai/enterprise/guardrails/secrets-detection)

### 4.2 Custom Regex：本地查固定格式

每条正则可有描述、实体类型、匹配标志及处置动作。PII 模板主要列出邮箱、美国手机号/社保号、类银行卡号和 IPv4；官方明确这是格式匹配，其他国家身份证件需要补规则。不能把这个模板直接当作完整的中文 PII 方案。[Custom Regex](https://docs.getbifrost.ai/enterprise/guardrails/custom-regex)

### 4.3 Prompt Guardrails：用模型判断业务语义

选判官模型、写自然语言规则、验证配置，再关联流量规则。Verify 主要检查连通性和结构化结果；正式的业务判定效果需样本测试。运行超时或解析失败会记录失败并继续原请求；检测产生额外模型调用。[Prompt Guardrails](https://docs.getbifrost.ai/enterprise/guardrails/prompt-guardrails)

这与阿里自定义检测 Agent 都属于模型辅助判定，但不是同一产品：一个由 Bifrost 调已配置模型执行策略，另一个由阿里检测服务组织模型和分类标签。两者效果、本地部署可能性及成本应分别核实。

## 五、本轮可得结论与未决项

**事实归纳**：网关主要负责选择流量、组织检测、执行处置和留记录；检测服务可同时承担风险识别与处置建议。确定性本地检查、外部审核服务、自然语言模型判定是三种可区分的能力来源。

形成自有产品方案前，仍需明确：

1. 围绕已确认的 D1/D3/D4/D5，确定首批业务样本和具体检测类别。
2. 是否允许原文送到云检测服务。
3. 首期采用哪些能力来源；哪些配置留在检测服务控制台，哪些在本产品维护。
4. 输入历史范围、流式策略、脱敏范围，以及检测异常时的行为。

本轮没有将阿里全部标签、Bifrost 全部检测器或上述候选列为首期交付要求。
