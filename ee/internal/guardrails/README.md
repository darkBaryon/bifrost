# 本地内容安全框架

本包在模型调用前检查用户输入、在返回用户前检查模型输出，按配置决定放行、拦截或仅记录。检测器有两种：密钥规则检测器在本地识别 API Key、云凭据和私钥；判官检测器把文本交给网关里已配置的大模型判断有害内容、提示词攻击和管理员写的业务规则。配置存在配置数据库里，通过 `POST /api/guardrails/*` 读写并立即生效；**没有配置时所有请求透传**，也可用环境变量开启开发假检测器验证链路。

## 整体如何实现

整个功能由四部分配合完成：

| 部分 | 负责什么 |
|---|---|
| 插件入口 | 接入 Bifrost（本项目内嵌的网关）的请求前、响应后钩子，提取文本，记录检查结果，将拦截决定变成客户端错误 |
| 规则 | 描述检查输入还是输出、使用哪个检测器、风险达到什么等级算命中，以及命中和检测失败时怎么办 |
| 规则检查器 | 按当前阶段选取规则，并行调用各自的检测器，比较风险阈值，按规则决定处置；任一规则拦截即取消其余 |
| 检测器 | 实现具体检测算法，接收文本并返回风险等级；没有发现风险返回空结果，无法完成检测返回错误 |

检测器负责发现风险，检查器负责根据规则作决定，插件负责让决定在网关中生效。所有检测器共用同一个接口；有害内容、凭据、提示攻击、业务规则这些类别用于标识检测项目，实际调用哪个实现由规则绑定的检测器 ID 决定。

### 一次请求如何经过检查

下面展示输入、输出检查均已配置时的路径；没有配置某阶段规则时，跳过该阶段的检测。

```text
用户请求
  ↓
插件检查请求是否受支持，提取全部历史消息中的纯文本
  ↓
检查器执行输入规则 → 检测器 → 风险结果 → 按规则决定处置
  ├─ 拦截：返回安全错误，不调用模型
  └─ 放行（包括仅记录命中）
       ↓
     Bifrost 调用模型
       ↓
     插件取得完整回答，逐个提取候选回答的纯文本
       ↓
     检查器执行输出规则 → 检测器 → 风险结果 → 按规则决定处置
       ├─ 拦截：返回安全错误，不把回答交给用户
       └─ 放行（包括仅记录命中）：返回原回答
```

输入和输出共用一套检查器，只是使用不同阶段的规则。输出检查目前要求完整回答，因此配置输出规则后，插件会在调用模型之前拒绝流式请求；只有输入检查时允许流式请求。模型调用本身失败时，保留上游错误，不检查错误正文。

### 一条规则怎样决定处置

例如，配置一条“检查输入中的凭据，风险达到中等级就拦截，检测超时也拦截”的规则，并绑定一个凭据检测器。检查器调用该检测器后，取其发现的最高风险等级与规则阈值比较：

| 检查情况 | 处置 |
|---|---|
| 没有风险，或风险低于阈值 | 继续执行后续规则 |
| 达到阈值，命中策略为拦截 | 取消仍在进行的其他规则，由插件拒绝请求或回答 |
| 达到阈值，命中策略为仅记录 | 记录命中；其他规则照常完成，仍可拦截 |
| 检测器报错、超时、返回非法风险等级或声明文本超过自身上限 | 记录为检测失败（失败类别分别为 detector_error、detector_timeout、invalid_result、text_too_large），按规则明确指定的放行或拦截策略处理 |

每条已完成的规则生成一个 `RuleEvaluation`，记录该规则的风险等级、命中或失败情况以及处置动作。一次文本检查返回 `CheckResult`，包含最终放行或拦截决定、决定拦截的那条评估 `Blocking`（插件据此选择客户端错误码），以及已完成规则的 `RuleEvaluations` 列表（因拦截被取消、未完成的规则不在其中）；插件通过注入的日志接口输出这些记录。检测失败后放行也会记为失败，不能记成检查通过。框架输入非法、文本超限或父请求取消属于无法继续检查，直接返回错误，不套用检测器的失败放行策略。

## 代码从哪里读

先读插件入口了解请求路径，再读检查器和单条规则执行；改检测算法看 `detectors/`，改配置与接口看 `config/`、`http/`、`host/`。

| 文件 | 职责 |
|---|---|
| [plugin/plugin.go](plugin/plugin.go) | 请求前与响应后入口，固定每个请求的检查器快照，调用检查器并记录规则执行结果；`Swap` 热替换检查器 |
| [plugin/text.go](plugin/text.go) | 支持范围判断、输入和输出文本提取、长度限制 |
| [plugin/deny.go](plugin/deny.go) | 客户端错误映射、拒绝响应构造，禁止回退其他模型重试 |
| [checker.go](checker.go) | 保存规则快照，按阶段并行执行检查，汇总结果与拦截来源 |
| [evaluation.go](evaluation.go) | 调用单条规则绑定的检测器，处理超时、阈值、失败策略与取消 |
| [rule.go](rule.go) | 规则定义及校验 |
| [detector.go](detector.go) | 检测器接口、检测发现和风险等级 |
| [detectors/secrets/](detectors/secrets/) | 密钥规则检测器：`rules.go` 加载嵌入的规则数据，`scan.go` 扫描；规则数据与来源见 [data/README.md](detectors/secrets/data/README.md) |
| [detectors/judge/](detectors/judge/) | 判官检测器：`detector.go` 组织提示词与重试，`parse.go` 解析等级，`prompts/` 三份提示词 |
| [detectors/fake/detector.go](detectors/fake/detector.go) | 开发用标记检测器，只匹配 `[guardrails-test]` |
| [config/](config/) | 管理员配置的解析、校验、默认值与规则展开（只用标准库） |
| [host/](host/) | 判官通过网关发内部子请求的适配器；`Builder` 把配置变成检查器并校验判官 provider |
| [persistence/](persistence/) | 配置表 `ee_guardrails` 的迁移与带版本号的读写 |
| [http/](http/) | `POST /api/guardrails/get|update|reset`；更新顺序是校验 → 构建 → 写库 → 热替换 |
| `../app/guardrails.go` | 启动装配：建表 → 读库 → 构建（失败拒启）→ 注册插件与接口 |

## 配置与接口

配置是一个 JSON 对象，字段与默认值见 [config/config.go](config/config.go)，示例：

```json
{
  "deny": {"status": 400, "message": "内容未通过安全检查"},
  "judge": {"provider": "deepseek", "model": "deepseek-v4-flash", "retries": 3, "timeout_ms": 10000},
  "secrets":       {"enabled": true, "stages": ["input"], "threshold": "medium", "on_match": "block", "on_error": "block", "ignored_keywords": []},
  "harmful":       {"enabled": true, "stages": ["input", "output"], "threshold": "medium", "on_match": "block", "on_error": "block"},
  "prompt_attack": {"enabled": true, "stages": ["input"], "threshold": "medium", "on_match": "block", "on_error": "block"},
  "business_rules": [{"id": "pricing", "rule": "不得透露内部底价与渠道折扣", "enabled": true, "stages": ["input", "output"], "threshold": "medium", "on_match": "block", "on_error": "block"}]
}
```

- 数值字段写 `0` 与省略等价，都取默认值；`judge.retries` 不能低于 3，写 0 得到的是默认的 3 次。
- 每个项目、每条业务规则在每个阶段各是一条规则，各配阈值（`low`/`medium`/`high`，达到即命中）、命中动作（`block`/`observe`）和检测失败动作（`block`/`allow`）。判官类项目共用 `judge`：provider 必须已在网关配置；`retries` 3–5，重试和调用都在 `timeout_ms` 内，到期按检测失败处理。
- 两级文本上限：`max_text_bytes`（默认 64KB）是插件提取文本的总上限，超过直接拒绝；`judge.max_text_bytes`（默认等于前者，可下调）是送判官的上限，超过记为 `text_too_large` 失败并按该规则的失败动作处理。
- 接口都是 POST、都需管理端登录：`get` 返回 `{config, version}`（未配置为 `null` 和 `0`）；`update` 带 `{config, version}`，`version` 须等于当前值（首次为 0），校验或构建失败返回 400、版本不符返回 409，成功后整份配置（含 `deny` 的状态码与文案）只对之后到达的请求生效；`reset` 删除配置并停止检测。开发假检测器模式下 `update` 返回 409。权限登记在 `../rbac/host/routes.txt`（读 `Settings.View`，写 `Settings.Manage`）。
- 启动时库中已有配置则立即构建；构建失败（例如判官 provider 已被删除）拒绝启动，日志说明修复方式：改回 provider，或直接删除 `ee_guardrails` 表的行后重启。

## 检测器要点

- **密钥（D3）**：规则数据源自 Gitleaks 并按本仓核实补充（国内外厂商前缀、钉钉 / 飞书 / 企业微信 webhook、中文字段名）。厂商专用规则报 `high`，无法归属厂商的通用规则报 `medium`；命中值以 `sk-bf-` 开头（网关自身的虚拟 key）一律不报；`ignored_keywords` 供管理员止血误报。
- **判官（D1/D4/D5）**：每个项目一个实例，每条业务规则一个实例；判官请求是跳过插件管线的内部子请求，不会再经过本插件，也不进上游日志与计费（本包只记 token 数）。判官返回的理由不记录；判官调用失败时，provider 的错误消息也不进日志（可能回显送检内容），只留状态码、类型与代码，网关自身产生的错误消息保留前 200 字节。判官 provider 自带的重试会与检测器重试相乘，装配时记 Warn。

## 开发联调

设置 `EE_GUARDRAILS_FAKE` 后，启动时注册假检测器而不是库中配置；库中已有配置时拒绝启动，避免两套配置并存。未设置或空值时走生产装配；其他非法值拒绝启动。

| 值 | 验证场景 |
|---|---|
| `input-block` | 输入含 `[guardrails-test]` 时拦截，不调用模型；允许流式请求 |
| `input-observe` | 输入含该标记时记录 `action=observe`，请求继续 |
| `output-block` | 模型回答含该标记时拦截；使用非流式请求，流式在调用模型前被拒绝 |

自动验证使用本地模型替身（同时充当判官：待审文本含 `[judge-high]` 返回 high）、临时 SQLite 和随机端口，覆盖配置接口、密钥与判官拦截、热更新、版本冲突、重启加载、重置和假检测器场景。在仓库根执行：

```bash
python3 ee/scripts/guardrails-smoke.py
# 启动隔离环境供现有 Playground 联调；Ctrl+C 只关闭本脚本创建的进程。
python3 ee/scripts/guardrails-smoke.py --serve input-block
```

先用 `make -C ee build` 构建带 UI 的程序。脚本输出访问地址和证据目录；临时登录信息位于该目录权限为 0600 的 `browser.json`。模型选择 `openai/guardrails-test-model`，它回显最后一条用户消息。

## 接入时需要保留的约束

- **支持范围：** 当前只检查规范化的纯文本 Chat。未知参数、工具、多模态、推理或拒答正文等未支持内容会被拒绝；完整判断见 [plugin/text.go](plugin/text.go)。超大请求或响应的原样透传模式也不支持，配置大响应透传阈值同样会被拒绝。
- **文本边界：** 输入包含全部历史消息；输出逐个检查候选回答，所有候选共享总字节预算。文本必须是有效 UTF-8，空文本仍送检。
- **检测器契约：** 实现必须并发安全，被取消时返回 `ctx.Err()`，文本超过自身上限时返回 `ErrDetectorTextTooLarge`，不得把失败伪装成无风险。
- **超时与并发：** 同阶段规则并行执行，检测同步进行，超时和拦截取消都依赖检测器响应 context 取消并返回 `ctx.Err()`，框架不强行终止检测器。实现必须并发安全并及时响应取消。规则和注册表会复制，检测器实例由装配方管理生命周期。
- **热更新：** 插件通过 `Swap` 替换检查器；每个请求在请求前钩子固定一份快照，响应后钩子只用这份快照（缺失或为空则透传），所以替换只影响之后到达的请求，进行中的流式请求不会被改判。
- **日志与响应：** 不记录正文、命中原文或检测器异常原文；拦截返回错误，不替换为正常模型回答，也不允许回退其他模型重试。
- **插件顺序：** 安全插件须排在可能提前返回响应的插件之前；其他插件、缓存或旁路不得在检查完成后改写受检正文。当前集成测试覆盖安全插件包住模型替身的 Bifrost 管线，生产中的插件组合仍需在实际装配时验证。

## 验证与参考

在 `ee/` 执行 `GOWORK=off go test -race ./internal/guardrails/...`。检查器和插件测试覆盖处置行为与并行取消；密钥测试用构造的假密钥，判官与宿主适配器测试用替身模型；`plugin/schema_test.go` 提醒上游字段变化后的复审，`plugin/integration_test.go` 使用真实插件管线和模型替身，不调用外部服务。

实现参考 [Higress 源码研究](../../../product/需求/内容安全/Higress源码研究.md)中的职责拆分、阈值判断和处置流程。
