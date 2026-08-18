# 开发工作台（Development Workbench）

bifrost fork（darkBaryon/bifrost）的本地功能开发工作台：在文件系统里管理需求、方案、评审、验收的流转，并渲染成一个干净的本地站点。**只管开发流程，不含业务代码。**

- **流程规范**（职责 / 三档分级 / 方案要件 / 评审 / Gate / 偏差协议）：[规范.md](规范.md) + [规范/](规范/) 七个子页
- **frontmatter schema**：[SCHEMA.md](SCHEMA.md)
- **本仓怎么操作**（铁律 / 目录 / 跑一个过程文档）：[规范/本仓操作规范.md](规范/本仓操作规范.md)
- **日常入口**：[index.md](index.md)（活跃工作台，生成文件）

## 命令

```bash
python3 tools/build_views.py            # 校验 frontmatter + 重新生成视图/导航
python3 tools/build_views.py --check    # 仅校验（钩子/CI 用，含视图漂移检测）
python3 tools/build_site.py             # 生成 site/ 静态站点
python3 tools/serve_site.py --port 8767 # 本地起站点（默认 8767）
python3 tools/preflight.py --config cases/<案子>/期<N>/checklist.yaml  # 预飞自检
```

依赖：Python 3.9+ 与 `pyyaml`（`pip install pyyaml`）。站点渲染零第三方依赖，纯标准库 + `assets/` 手写主题。

> 注：站点首页由 `index.md` 顶替本 README，正文一律写在 [规范.md](规范.md)。生成文件（首行带 `<!-- generated` 哨兵）禁止手改。
