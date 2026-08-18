# 开发工作台(Workbench)

> 本仓的工程流程以 **[workbench/](../../workbench/)** 为运行载体——需求、方案、评审、验收全部以带 frontmatter 的文档在文件系统流转,并渲染成本地站点。规范全文读 [workbench/规范.md](../../workbench/规范.md);本页只做入口指引。

## 心智模型

- **frontmatter 即数据库**:任何文档的现状只看 `status`/`verdict` 字段,不从正文猜
- **文件永不移动**:生命周期靠改字段,归档不挪文件
- **生成物禁止手改**:首行带 `<!-- generated` 哨兵的文件(index/SUMMARY/views/nav.js)只能由 `build_views.py` 重新生成

## 日常动线

```
起需求  → 从 workbench/templates/需求.md 起稿,落 workbench/reports/
定档    → 蓝图级 / 标准级 / 轻量级(规范/三档分级);轻量级不建方案页
做方案  → cases/<案子>/期<N>/方案v1.md + checklist.yaml → 方案评审 → Gate 1
实施    → feature 分支;完成后方案尾部补实施记录三章节,status: 已实施
预飞    → python3 tools/preflight.py --config cases/<案子>/期<N>/checklist.yaml,全绿才请评审
代码评审 → 评审拿预飞事实表只做语义判断 → Gate 2 → 验收记录,方案归档
提交前  → python3 tools/build_views.py 必须全绿
```

## 本仓专属约定(规范之外的项目事实)

- 基线表述:`develop @ <sha>`,注明最近上游同步点;上游同步本身走 [02-开发工作流](02-开发工作流.md),不占 case
- checklist 必过命令的本仓默认值已写在 [模板](../../workbench/templates/checklist.yaml):`go build 三模块 + ui tsc`,高风险档补 `make lint` 与相关 `go test`
- 扩展位置的判断(插件 vs 改源码)见 [03-接线指南](03-接线指南.md)
- 收敛评审:`.claude/skills/convergence-review`(大改动在 Gate 2 前跑)
- 采纳来源与升级方式:[workbench/ADOPTION.md](../../workbench/ADOPTION.md)
