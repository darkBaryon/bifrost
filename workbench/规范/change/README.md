# Change 模型

[← 返回仓库首页](../README.md)

Change 是工程账本的最小完整单元：**一次有意图的系统变化，从提出、实施、评估影响到知识沉淀的完整生命周期**。开发、重构、测试不再是三种平行的"案子"，而是同一个 Change 内部的流程类型。

## 为什么以 Change 为中心

以"开发需求"为中心时，重构没有需求可挂、测试结论悬在半空、一次功能引发的连锁治理（拆模块、补测试、发版）会散落成互不知晓的文档。以 Change 为中心后：

```text
CHG-014 增加账号搜索                    ← 一次系统变化，一个账本条目
├── DEV-021 实现功能                    ← 走开发规范
├── REF-008 收敛重复分页逻辑            ← 走重构规范，triggered_by: CHG-014
└── TST-014 多端兼容测试                ← 走测试规范
```

子 case 各走各自规范（[开发](../development/README.md) / [重构](../refactoring/README.md) / [测试](../testing/README.md)），现有三套规范原文不变，只是被 Change 引用。

## Change 类型

`feature / bugfix / refactoring / migration / dependency-upgrade / performance / security / maintenance`

类型决定默认关注点（如 migration 默认关注数据兼容与回滚），不决定流程重量——重量由[风险分级](risk-levels-and-gates.md)决定。

## 生命周期

```text
提出 → 定级 → [方案 → Gate 1]（按级别与拍板项，见风险分级与条件 Gate）
     → 实施 → 收敛评审 → Gate 2 交付 → 知识沉淀 → 归档
```

- **L1 的 Change 允许不挂任何子 case**：Change 文档本身（摘要 + 证据 + 交付摘要）就是全部记录。
- 子 case 可以在生命周期中途产生：[收敛评审](convergence-review.md)发现的结构债立 `REF-xxx, triggered_by: CHG-xxx`，进队列排期，不阻塞本 Change 交付。

## ID 与关联

- `CHG-###`：Change 唯一编号；子 case 用 `DEV-### / REF-### / TST-### / REL-###`。
- 关联只用两个字段：子 case 的 `change:`（属于哪个 Change）与 `triggered_by:`（由哪个 Change 的评审引发）。
- Finding 用 `FIND-###`，见 [Finding 台账](findings.md)。

## 知识沉淀（Change 收尾必查）

Workbench 记录"系统为什么变成现在这样"；主仓库长期文档记录"系统现在是什么样"。Change 归档前逐项过一遍，有则迁移、无则明写"无"：

| 本次产生的 | 沉淀到 |
|---|---|
| 新业务规则 | 自动化测试（测试即规则的长期形态） |
| API / Schema 变化 | OpenAPI / contracts / 类型定义 |
| 架构决策 | ADR |
| 模块职责变化 | 架构文档 |
| 部署 / 故障经验 | Runbook |
| Agent 反复踩的项目坑 | AGENTS.md / CLAUDE.md |
| 临时调查、实施过程 | 只留 Workbench，不外溢 |

沉淀不做完，Change 不算归档——这是防止"长期事实永远埋在过程文档里"的唯一闸口。
