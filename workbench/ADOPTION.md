# Workbench 采纳记录

本工作台 = **playbook 规范内核 + xhs-recon 工具外壳**。血缘:xhs-recon 的 workbench 是流程实践的中间产物,后被提炼升级为 engineering-playbook(更完整:Change 模型/风险 Gate/收敛评审/Finding/重构与测试规范);工具外壳(frontmatter 状态机/视图生成/预飞/本地站点)仍以 xhs-recon 版本为源。

| 部件 | 来源 | 版本 |
|---|---|---|
| **规范内核**(`规范/` 四套 17 页 + 跨切模板 3 份) | darkBaryon/engineering-playbook | **v1.1.0 @ 78d5a7ef** |
| **工具外壳**(tools/ SCHEMA/ AGENTS/ templates×7/ assets/ tests) | darkBaryon/xhs-recon `workbench/` | @ 1c0ad324 |

取代:先前根目录的 `playbook/` 与 `cases/`(2026-08-18 拆除,当时尚无 case,零迁移损失)。

## vendor 时的本地适配

1. `README.md` 首段与规范引用:指向 bifrost fork、四套规范
2. `SCHEMA.md` / `规范/本仓操作规范.md`:示例 case 名换为 `UI中文化层v1`
3. `templates/checklist.yaml`:必过命令换为本仓真实命令(go build / tsc,高风险补 make lint)
4. `tools/build_views.py`:导航"规范"区改为 playbook 四套规范入口
5. `tools/build_site.py`:`SKIP_NAMES` 只对工作台根生效(否则 `规范/*/README.md` 不渲染)
6. `规范.md`:重写为四规范入口 + 工作台↔规范映射页
7. 新增 `.gitignore`(排除生成的 `site/`)

## 命令速查(在 workbench/ 目录下)

```bash
python3 tools/build_views.py            # 校验 frontmatter + 重新生成视图/导航(提交前必须全绿)
python3 tools/build_site.py             # 生成本地站点 site/
python3 tools/serve_site.py --port 8767 # 浏览站点
python3 tools/preflight.py --config cases/<案子>/期<N>/checklist.yaml   # 预飞自检
```

工具自测(在仓库根):`PYTHONPATH=. python3 -m pytest workbench/tests -q`

## 升级方式

- **规范升级**:读 playbook 的 CHANGELOG → 重新 vendor `规范/` 与跨切模板 → 更新本表版本号
- **外壳升级**:对照 xhs-recon workbench 的 tools/assets 变更 → 复核上表 7 处本地适配未被覆盖
- 升级后必跑:`build_views.py` + 工具自测 + `build_site.py`
