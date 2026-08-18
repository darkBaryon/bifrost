# Finding 台账

[← 返回 Change 模型](README.md)

Finding 记录**看到了、但现在不修**的结构问题——防止"看到了结构问题，不修，之后就忘了"。它是[收敛评审](convergence-review.md)的记忆，也是技术债的聚合视图。

## 来源

- 收敛评审的"Finding"出口（主要来源）；
- 任何评审的建议栏；
- 用户或任何 agent 在任何时刻的观察。

## 条目要件

```yaml
id: FIND-021
status: 观察中            # 观察中 → 已确认 → 已转Case / 已接受
triggered_by: [CHG-014, CHG-017]   # 每次被再次观察到，追加一条
evidence: "detail_page.dart 连续 3 个 Change 被改，职责持续增长（CHG-009/014/017 各 +1 个分支）"
convert_when: "再有 Change 需要改动该文件的解析逻辑时，先转 REF 处理"
```

- **evidence 必须具体可查**（哪个文件、哪几个 Change、什么现象）；"感觉有点乱"不构成 Finding。
- **convert_when 是转化触发条件**：写清什么事实出现时该转 REF Case——这让"以后再说"变成一个可执行的判断，而不是无限期拖延。

## 生命周期

```text
观察中 ──再次观察到，追加 triggered_by──▶ 已确认 ──立 REF Case──▶ 已转Case
   │                                        │
   └────────── 用户裁决接受现状 ──────────▶ 已接受（豁免）
```

- **已接受（豁免）**：用户裁决"接受现状 + 理由"后，收敛评审**不再重提**，除非后续 Change 使其显著恶化（恶化事实要写进新的 triggered_by）。
- 同一问题被多个 Change 反复触发，本身就是最有力的转化证据——`triggered_by` 列表越长，越该转 Case。

## 台账即触发器

不需要定期巡检机制：挂账条目在台账里持续可见，攒到碍眼时，用户主动把几条相关的债打包成一个 refactoring 类型的 Change 清掉。债务的可见性本身就是治理的触发器。
