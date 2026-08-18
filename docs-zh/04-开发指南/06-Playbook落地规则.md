# Engineering Playbook 落地规则

> Playbook 规定"分支、环境、命令以业务项目自己的规则为准"——本文就是那份项目侧规则。
> 采纳版本见 [playbook/ADOPTION.md](../../playbook/ADOPTION.md);规范正文读 Playbook 仓库,此处只写本项目的具体化。

## 基线与分支(对应:立案与定档)

- **基线表述**:`develop @ <sha>`,并注明最近一次上游同步点(`upstream/dev @ <sha>`)
- **隔离实施**:`feature/<slug>` 从 develop 切出;完成后合回 develop 推送 fork
- 上游同步(水流②)本身按 [02-开发工作流](02-开发工作流.md) 执行,不占用 case 流程

## 风险定档默认值(对应:risk-levels-and-gates)

| 改动类型 | 默认档位 | 理由 |
|---|---|---|
| 新增文件(插件/文档/翻译词条/工具脚本) | 低 | 零 merge 税,失败模式温和 |
| 改 `ui/` 源码、`transports/handlers` | 中 | 有同步税,影响交付面 |
| 改 `core/`、`framework/` 存储层、热路径 | 高 | 全局影响,需完整方案+复核 |
| 拿不准 | **按高档准备**(Playbook 底线) | |

## 机械检查清单(对应:预飞)

```bash
make lint                                    # Go + UI 静态检查
go build ./core/... ./framework/... ./transports/...
cd ui && npx tsc --noEmit                    # UI 类型检查
go test ./<受影响模块>/...                    # 相关单测
make build                                   # 高风险档:完整构建含 UI
```

全绿是事实证据的最低线;证据(命令+输出摘要)写进 case 的 acceptance.md。

## Gate 实践

- **Gate 1**(方案确认):中/高风险任务,方案落盘 `cases/<case>/plan.md` 后,在会话中向用户明示"范围/取舍/方向",获得明确确认再动代码;低风险任务口头确认即可
- **Gate 2**(交付放行):展示测试与证据,用户明确同意后才合入 develop 并推送
- 实施偏离已确认范围时:**先停、记录进 case、请用户裁决**(Playbook 底线,不例外)

## 收敛评审与 Finding

- 收敛评审用 `.claude/skills/convergence-review`(已安装,按比例原则:成本与改动规模成比例)
- 看到但暂不修的结构问题 → 记入 case 的 findings 段落,并汇总到 `cases/README.md` 台账,不许"记在脑子里"

## 与既有实践的衔接

本仓已运行的约定不变,Playbook 是在其上加流程骨架:

- 提交规范:英文 conventional commits(与上游一致)
- 文档沉淀:读代码产出进 `docs-zh/03-源码精读`,任务经验进 `04-开发指南`
- 扩展原则:数据面→插件;管理面/UI→最小侵入(见 [03-接线指南](03-接线指南.md))
