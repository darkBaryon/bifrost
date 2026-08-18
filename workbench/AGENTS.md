# Development Workbench — Agent 操作约定

本工作台是本项目功能开发的过程文档库（需求 + 过程文档两类，存在文件系统、渲染成本地站点）。**规范全文**在 `规范.md`（主页）+ `规范/` 七个子页；本文件只规定「在本仓怎么操作」。

## 铁律

1. **文件永不移动**：生命周期只在 frontmatter 的 `status` 字段，归档 = 改字段，不挪文件。
2. **首行带 `<!-- generated` 哨兵的文件禁止手改**（`index.md`、`SUMMARY.md`、`需求报告.md`、`views/*`、`cases/*/index.md`、`assets/nav.js`）。改完源文档跑 `python3 tools/build_views.py` 重新生成。
3. **提交前 `build_views.py` 必须全绿**（拦：frontmatter 缺字段 / 枚举非法 / relates 悬空 / 视图漂移）。

## 读

- 全局现状：`index.md`（活跃工作台）；单个过程文档按 `cases/<case>/index.md` 的编号顺序读（当前方案 → 评审按轮 → 验收；旧版方案仅考古）。
- **判断任何文档的现状只看 frontmatter 的 `status` / `verdict`，不从正文猜。**
- 机器检索：frontmatter 即数据库（字段与状态机定义在 `SCHEMA.md`），`views/all.md` 即全量索引，按 `type:` / `status:` / `case:` grep 直达。

## 写

- 一律从 `templates/` 起稿（需求 / 开发蓝图 / 方案 / checklist.yaml / 方案评审 / 代码评审 / 验收记录），frontmatter 字段齐全才算建档。
- 路径与命名：`reports/<需求标题>.md`；`cases/<过程文档名>/期<N>/{方案v<N>.md, checklist.yaml, 方案评审<R>.md, 代码评审<R>.md, 验收记录.md}`；非分期功能 `phase` 固定 1。
- 状态机：
  - 需求：`观察中 → 待计划 → 开发中 → 已交付`（另有 `已作废`）；
  - 方案/蓝图：`草拟中 → 待评审 →（需修改 → 出 v+1 回待评审）→ 已通过 →（实施完成）→ 已实施 →（代码评审通过 + Gate 2）→ 已归档`。
- 评审结论「需修改」一律出 v+1 新文档，旧版 `status: 需修改` 不再动；仅 Gate 决策的落地允许在已通过文档原地修订（必须新增「Gate 决策结果」章节并逐处标注来源）。
- 轻量级也立需求文件（`tier: 轻量级`，极简正文），不建方案页；`需求报告.md`（生成）按档位汇总全部需求。

## 流程锚点（逐步映射见 规范.md，细则读规范子页）

- 实施完成 → 方案尾部补**实施记录三章节**（实施记录 / 实施偏差记录 / 蓝图验证备忘；无偏差也写「无」）→ `status: 已实施`，这是触发代码评审的信号。
- 预飞自检：`python3 tools/preflight.py --config cases/<案子>/期<N>/checklist.yaml`，**全绿才请评审，不绿先修**。
- Gate 2 通过：落 `验收记录.md`，方案 → `已归档`，需求 → `已交付`。

## 命令

```bash
python3 tools/build_views.py            # 校验 + 重新生成视图/导航
python3 tools/build_views.py --check    # 仅校验（钩子/CI 用）
python3 tools/build_site.py             # 生成站点
python3 tools/preflight.py --config <checklist.yaml> [--repo <项目根>] [--out facts.md]
```
