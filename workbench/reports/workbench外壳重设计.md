---
title: workbench 外壳重设计
type: 需求
id: workbench外壳重设计
status: 开发中
tier: 标准级
created: 2026-09-02
---

## 背景

UI完整中文化 期1 走完整个流程后暴露出工作台自身的结构问题:老外壳(xhs-recon,六类型状态机)与新内核(playbook v1.1.0 Change 模型)只合并了一半——SCHEMA 无「收敛评审」类型(本期只能借代码评审类型落档)、AGENTS.md 流程锚点漏列收敛评审(直接导致漏做)、Finding 台账只有规范页无实体文件(8+ 条 Finding 散落 4 份文档正文)、CHG/REF 编号体系从未用过;"讲流程"的文档分理念/规范/操作三层互相重叠且不同步;57 个 md 服务 12 篇实际文档,生成视图与模板有冗余。用户裁决(2026-09-02):按 B 档重设计外壳。

## 目标

1. 外壳升级为 Change 模型原生:SCHEMA 收录「收敛评审」类型;tier ↔ L1/L2/L3 映射写正;明确 case 名即账本主键(放弃 CHG/REF 编号);需求增可选 `triggered_by`
2. Finding 实体台账 `findings.md`,迁入期1 全部挂账条目(带 status/triggered_by/evidence/convert_when)
3. 入口收敛为 3 页:README(门牌)/ AGENTS.md(唯一操作页,吸收 本仓操作规范)/ 规范.md(流程 + 压缩后的理念节);Harness Engineering 归档
4. 生成视图砍到 index + all(+SUMMARY/nav 站点管线保留);未用模板(gate-card/preflight-facts)删除,跨切模板中文命名
5. 期1 收敛评审记录转正为正式类型

## 非目标

- 不改 `规范/` 18 页 playbook 只读副本的内容(本仓操作规范.md 属本地外壳,占位化不算)
- 不向上游 playbook 提修订(C 档,另案)
- 不动 tools/preflight.py 与既有 case 文档正文(收敛评审1.md 的类型转正除外)

## 定档提议

标准级(L2)——多文件、动 SCHEMA 状态机与生成器,但不动业务代码、可整体回滚。

**用户裁决**:B 档即定档确认(2026-09-02 会话)。

## 验收口径

- `build_views.py --check` 全绿(含收敛评审类型往返校验);`pytest workbench/tests` 全绿;`build_site.py` 跑通
- 收敛评审1.md 以 `type: 收敛评审` 通过校验;views/ 下不再生成 active.md/by-case.md;生成页无断链
- findings.md 台账条目 ≥ 期1 散落 Finding 数,每条四要件齐全
- AGENTS.md 单页可独立指导完整流程(锚点含收敛评审)

## 处置历史

- 2026-09-02 立案(诊断见会话;用户三档裁决选 B)
