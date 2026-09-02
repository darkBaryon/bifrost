# UI完整中文化 期1 — 阶段C 浏览器复巡检(终态)

- 日期:2026-08-28
- 驱动:`/tmp/zh-audit/audit2.mjs`(Playwright 1.62.1 via npx 缓存 + 本机 Chrome 151,`channel: 'chrome'`;刻意不在 ui/ 内 npm install)
- 页面集:30 页 = 上轮 5 类盲区实例页 20 页(盲区A 日志、盲区B 告警×3/护栏/Edge/自适应路由、盲区C 连接器、盲区D 登录/集群/用户/审计日志、盲区E 技能仓库/模型限额/MCP注册表/自定义定价/提示词 等)+ 原巡检 10 页
- 判定口径:可见 DOM 叶子文本节点,排除 monaco/code/pre/svg-title;ALLOW 正则放行品牌与技术 token
- 完整输出存档:本文件末尾「终态残留全量清单」;三轮中间输出在会话内(/tmp/zh-audit/patrol2-full.txt、patrol3-full.txt)

## 三轮收敛过程

| 轮次 | 触发动作 | 结果 |
|---|---|---|
| 1 | 阶段C 首批 692 条消化后 | 暴露 4 类残余:版本发布横幅模板、sidebar 预约演示碎片、单词多行文本(here/Live)、**跨行三元字符串**(prettier 折行形态,通道⑦同行正则永不触发) |
| 2 | 机制级修复:新增通道⑦b(+2 自检)→ 新候选 79 条全部入典;版本横幅补 RULES 第 34 条;碎片升级入典 | 残余收窄到:供应商页 Configured/Add new、日志页 in / out / entries、MCP 日志空态碎片 |
| 3 | 回源码定位后补 9 条碎片词条(运行时按文本节点 trim 查表、保留原空白,碎片可安全独立成词条) | **终态:全部残留均属方案 §3 既定非目标**(下表) |

## 终态残留分类(30 页,9 个页面条目非零)

| 页面 | 条数 | 分类(全部非目标) |
|---|---|---|
| LLM日志(盲区A + 原巡检,两次计) | 6 | 模型名(deepseek-chat/gpt-4o-mini/qwen-* 等) |
| 连接器(盲区C + 原巡检,两次计) | 7 | 品牌(Open Telemetry/Datadog/BigQuery/Kafka/Pub/Sub/New Relic/bifrost) |
| 供应商 | 3 | 品牌(DeepSeek)+ 用户数据(qwen、deepseek-zshrc 为用户自命名的 key) |
| 登录页-盲区D / 仪表盘 | 各 11 | 模型名 ×6 + 技术单位(0~1 tok/s)×5 |
| MCP 日志 | 1 | 语言名(Python,代码示例标签页) |
| 日志设置 | 17 | 阶段B 已记录 Finding 的跨节点碎片(实施偏差记录第 3 条,方案非目标) |
| 其余 21 个页面条目 | 0 | — |

盲区B(告警×3/护栏/Edge/自适应路由)、盲区E(技能仓库/模型限额/MCP注册表/自定义定价/提示词)、盲区D(集群/用户/审计日志)全部 **0 残留**。

控制台错误 2 条(404 资源 + React forwardRef 告警)为存量问题,与中文化无关(阶段C 之前即存在)。

## 终态残留全量清单(逐条)

```
LLM日志: deepseek-chat / deepseek-fake-model / gpt-4o-mini / qwen-nonexist / qwen-plus / qwen-turbo
连接器: Open Telemetry / Datadog / BigQuery / Kafka / Pub/Sub / New Relic / bifrost
供应商: DeepSeek / qwen / deepseek-zshrc
登录页与仪表盘: 上述模型名×6 + 0 tok/s / 0.3 tok/s / 0.5 tok/s / 0.8 tok/s / 1 tok/s
MCP日志: Python
日志设置: 17 条跨节点碎片(When enabled…/header…/prefix are always captured automatically. 等,
          与 evidence/UI完整中文化-期1-行级旁路分流.md 中阶段B 白名单清单一致)
```
