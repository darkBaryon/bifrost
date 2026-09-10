# 开发工作台（Development Workbench）

bifrost fork（darkBaryon/bifrost）的本地功能开发工作台，负责**具体开发人员级别的文档**：开发任务跟踪、具体实施方案、代码改动设计、实施步骤、测试、代码评审、验收与交付记录，并渲染成本地站点。不存放业务代码。

[`product/`](../product/README.md) 负责**产品经理和技术负责人级别的文档**：产品调研、需求、产品方案、路线图、整体架构、模块边界、技术选型与长期技术决策。

**整体架构放 product，具体功能如何实施放 workbench。** 例如，EE 采用 feature-first、模块内分层的约定放在 [EE 后端架构](../product/架构/EE后端架构.md)；某次账号认证开发需要改哪些文件、怎样迁移数据、怎样测试和回滚，写在本工作台的具体方案中。开发方案和开发蓝图不能代替上层架构决策。

- **流程规范**（共享流程 + 类型差异，2026-09 本地重排）：[规范.md](规范.md)（主页/理念/页面地图）+ [规范/流程.md](规范/流程.md)（执行序唯一权威）
- **frontmatter schema**：[SCHEMA.md](SCHEMA.md)
- **本仓怎么操作**（铁律 / 目录 / 读写协议 / 流程锚点）：[AGENTS.md](AGENTS.md)（唯一操作页）
- **日常入口**：[index.md](index.md)（活跃工作台，生成文件）

开发以 [产品与技术文档](../product/README.md) 中已确认的需求和架构为输入；已有源码导读见 [中文参考资料](../product/docs-zh/README.md)。实施中发现业务或架构方向需要变化，反馈到 product 更新决策；本工作台保留此次实施过程和验证证据。

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
