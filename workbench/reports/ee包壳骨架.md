---
title: ee 包壳骨架
type: 需求
id: ee包壳骨架
status: 开发中
tier: 标准级
created: 2026-09-04
---

## 背景

企业级功能(审计、多用户、内容安全)不改上游文件,全部放在独立的 `ee/` 里,靠上游预留的插座接进去(插座清单见 docs-zh/06-产品调研/03 §1.4)。这条路线还没有走通过:没有 ee/ 模块、没有自己的 main、没有把 UI 的 `@enterprise` 别名接到自己的目录、没有验证过"Bootstrap 之后能否再挂路由"。B1 审计日志排在第一位,但它的第一周就是这副骨架,与其混在审计里做,不如单独立案先把骨架跑通,后面每个功能都在上面长。

## 目标

1. `ee/` 独立 Go 模块(纳入 go.work,内部路径镜像上游),`ee/transports/bifrost-http` 是自己的 main,照上游 176 行 main 起一个服务;OSS 的页面与 API 行为不变(同一份 config.db);控制台因 `@enterprise` 覆盖层进入企业模式(2026-09-04 裁决接受),差异按方案清单逐项核过并落文档
2. 三个插座各打通一次并留下最小示例:`ConfigStore.DB()` 自建一张表并跑迁移;`SyncLoadedPlugin` 注册一个进程内插件(空实现即可);在上游 Router 上多挂一条自己的路由(如 `GET /api/ee/ping`)
3. UI 侧:`ee/ui/` 通过符号链接接到 `ui/app/enterprise`,vite 的 `@enterprise` 别名解析到它;至少替换 `_fallbacks` 里一个组件证明链路通(哪一个在方案里定)
4. 构建:一条命令产出带 UI 的 ee 二进制;`make dev` 式的开发启动方式有文档
5. 结论落盘:哪些插座可用、Bootstrap 之后挂路由是否可行(不可行则记录改走 ServerCallbacks 的结论)、每个插座的最小用法,写进 docs-zh/04-开发指南

## 非目标

- 不做任何业务功能(审计、RBAC、内容安全都在各自需求里)
- 不设 IsEnterprise 标记、不替换鉴权(B2 才做)
- 不改上游任何文件;插座不够时记录下来,不在本案里补
- 不做集群、license、打包分发

## 定档提议

标准级(L2)——新增独立模块与构建链路、动 UI 别名解析,属多模块结构改动;但不动业务数据、不动权限、可整体删除回滚,不到 L3。

**用户裁决**:标准级可(2026-09-04 会话)

## 验收口径

- `cd ee && go build ./...` 通过;ee 的 `bifrost-http` 用 `-app-dir ~/.config/bifrost` 起在 8080,`/api/providers` 返回与 OSS 相同的 provider 列表,控制台可打开
- `GET /api/ee/ping` 返回 200 JSON;config.db 中出现 `ee_probe` 表;启动日志里 ee 注册的插件处于 active;`GET /api/branding` 返回默认值 JSON;首页 HTML 含 ShellRewriter 注入的标记
- 覆盖层生成后(`ui/app/enterprise` = 上游占位复制 + ee/ui 逐文件符号链接),`vite build` 与 `tsc --noEmit` 通过,审计页显示 ee 占位组件;企业模式差异清单逐项人工核过并落文档
- 相对 upstream/dev 的 diff 只含本仓既有差异(docs-zh、workbench、.claude、中文化四文件、main.tsx 两行)与新增的 ee/(checklist `upstream-untouched` 为空)
- docs-zh/04-开发指南 新增"ee 包壳"一页

## 处置历史

- 2026-09-04 立案(已检索 views/all.md 查重,无重复;来源:03 文档 §四 原"两个最小实验"改为以真实骨架落地)
- 2026-09-04 用户裁决标准级,转 待计划,起草方案 v1
- 2026-09-04 方案评审1 需修改(B1 企业模式副作用);用户裁决接受企业模式,目标 1 措辞同步修订;出方案 v2
- 2026-09-04 方案评审2 需修改(两处事实性错误);验收口径同步;出方案 v3
- 2026-09-04 方案评审3 通过;Gate 1 用户放行,转 开发中
- 2026-09-04 实施完成(5 步 5 个提交,冒烟 9/9),方案 v3 → 已实施;待预飞、代码评审 ∥ 收敛评审
