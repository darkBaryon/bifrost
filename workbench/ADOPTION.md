# Workbench 采纳记录

- **工作台骨架来源**:darkBaryon/xhs-recon `workbench/` @ `1c0ad324`(2026-08-18 vendor)
- **流程规范上游**:darkBaryon/engineering-playbook v1.1.0 @ `78d5a7ef`
  (本工作台 `规范/` 是其在项目内的运行形态;两者演进以 Playbook 仓库为准,定期对齐)
- 取代:先前根目录的 `playbook/`(9 份模板)与 `cases/`(2026-08-18 拆除,记录零迁移损失——当时尚无 case)

## vendor 时的本地适配(共 5 处)

1. `README.md` 首段:项目指向 bifrost fork
2. `SCHEMA.md` / `规范/本仓操作规范.md`:示例 case 名换为 `UI中文化层v1`
3. `规范/评审规则.md`:评审提问示例换为 zhLocale 场景
4. `templates/checklist.yaml`:必过命令换为本仓真实命令(go build / tsc,高风险补 make lint)
5. 新增 `.gitignore`(排除生成的 `site/`)

## 命令速查(在 workbench/ 目录下)

```bash
python3 tools/build_views.py            # 校验 frontmatter + 重新生成视图/导航(提交前必须全绿)
python3 tools/build_site.py             # 生成本地站点 site/
python3 tools/serve_site.py --port 8767 # 浏览站点
python3 tools/preflight.py --config cases/<案子>/期<N>/checklist.yaml   # 预飞自检
```

工具自测(在仓库根):`PYTHONPATH=. python3 -m pytest workbench/tests -q`

## 升级方式

1. 到两个上游仓库读变更(xhs-recon 的 workbench 工具/规范、engineering-playbook 的 CHANGELOG)
2. 重新 vendor 变化的文件,复核上表 5 处本地适配是否被覆盖
3. 跑 `build_views.py` + 工具自测,更新本文件的 commit 记录
