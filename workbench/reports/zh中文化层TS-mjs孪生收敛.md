---
title: zh 中文化层 TS/mjs 孪生实现收敛
type: 需求
id: zh中文化层TS-mjs孪生收敛
status: 观察中
tier: 轻量级
triggered_by: [UI完整中文化]
created: 2026-09-02
---

## 背景

UI完整中文化 期1 收敛评审(收敛评审1.md,verdict 仅立债挂账)立债:运行时(TS)与扫描器(mjs)之间存在需人工同步的孪生实现。

## 目标(收敛评审处方原文)

1. 抽 `ui/lib/zhTranslate.mjs`(纯 JS:LOOKUP 构建 + translate 判定,约 25 行),zhLocale.ts 与 zh-coverage.mjs 共用,删除 makeTranslate 与 translateRaw 的重复查表逻辑;
2. slug 标题化同法抽共享模块,topbar.utils.ts 与扫描器共用(消 FIND-009);
3. (可选)RULES 改存数据文件,两侧各自编译正则,parseZhLocale 对账机器整体删除。

## 验收口径

- tsc/build/self-test/coverage-check 全绿;翻译行为零变化(抽样对拍);
- 反证条件(评审原文):若 ui 构建链无法从 .ts import 同目录 .mjs,或 fork 策略禁止 topbar.utils.ts 结构改动,则 2/3 作废,1 独立成立。

## diff 量级

约 ±150 行,预估半天。

## 处置历史

- 2026-09-02 立案(观察中,不排期;来源:UI完整中文化 期1 收敛评审立债出口)
