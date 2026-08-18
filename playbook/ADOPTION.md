# Engineering Playbook 采纳记录

- **采纳版本**:1.1.0
- **来源**:darkBaryon/engineering-playbook @ `78d5a7ef`(feat/change-model)
- **采纳日期**:2026-08-18

## 本目录是什么

`playbook/templates/` 是从 Playbook 仓库按版本复制的记录模板(vendored,只读)。规范正文**不**复制——执行时直接读 Playbook 仓库对应版本;本项目只保存模板和落地规则。

| 内容 | 位置 |
|---|---|
| 落地规则(分支/命令/风险档位等项目专属约定) | [docs-zh/04-开发指南/06-Playbook落地规则.md](../docs-zh/04-开发指南/06-Playbook落地规则.md) |
| 任务记录(需求/方案/评审/验收) | [cases/](../cases/) |
| 收敛评审 skill | `.claude/skills/convergence-review/` |

## 升级方式

1. 在 Playbook 仓库读 `CHANGELOG.md`,确认目标版本的变化
2. 重新拉取 `templates/` 与 `skills/` 覆盖本目录,更新本文件的版本与 commit
3. 对照新版检查落地规则文档是否需要同步修订
