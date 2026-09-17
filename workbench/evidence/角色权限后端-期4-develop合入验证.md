# 角色权限后端：develop 合入验证

日期：2026-09-18。用户确认后端验收，并明确要求合入 `develop` 后推送公司 GitLab。

## 合入内容

- 目标基线：`822f24a66`，当时的 `origin/develop`，已有国内定价和内容安全开发框架。
- 来源：`feat/user-permissions-phase1` 的 `e4bb18912`，包含四期后端、敏感权限、删除账号和最近登录时间。
- 合并保留身份/角色迁移和认证工厂、开发护栏、品牌、定价；全部路由注册后核对角色规则，最后安装 RootGuard。
- Makefile 同时保留定价、品牌、身份与角色权限冒烟入口。
- `go mod tidy` 仅整理直接依赖标记，不改变版本及 go.sum。
- Finding 重号处理：develop 原 FIND-022（定价评审流程问题）保留；角色权限的原 FIND-022 改为 FIND-030，台账说明历史映射。历史评审与证据不改写。

## 合并验证

在主工作树 `ee/` 执行：

- `GOWORK=off go test -race ./...`：全量通过。
- `GOWORK=off go vet ./...`：通过。
- `GOWORK=off go build -o tmp/bifrost-rbac-merge ./cmd/bifrost-http`：通过。
- `GOWORK=off go mod tidy -diff`：无差异。

使用上述同一构建产物运行：

| 脚本 | 结果 |
|---|---|
| rbac-smoke.py --database sqlite | 通过：权限、敏感字段、账号删除、最近登录时间、通知筛选和 WS 撤权、启动条件路由 |
| identity-smoke.py --database sqlite | 通过：初始化竞争、账号/密码、配置、会话撤销、WS、离线恢复及重启 |
| branding-smoke.py | 通过：共享宿主、页面、品牌读写、鉴权、图片缓存与重启保留 |
| guardrails-smoke.py | 通过：默认关闭、输入拦截、观察、输出拦截、流式边界和重启关闭 |
| pricing-smoke.py | 通过：厂商识别、换算、幂等重启、手工数据保留、异常价格文件及汇率更新 |

冒烟仅使用隔离数据库、随机端口和本地/合成测试数据，没有修改服务器部署。
本轮没有重跑 PostgreSQL；其账号生命周期和迁移验证沿用[后端验收记录](../cases/角色权限后端/期4/验收记录.md)中的证据。

构建产物 SHA256：`251b4428d98a19e82471d4fef2f87d405220f7fecbe240ba4019f726633b3309`。此为合并工作树构建，尚未生成合并提交时测试，不冒作干净提交构建。

本机临时证据：

- RBAC：`/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/bifrost-rbac-smoke-7lzbei0v`
- 身份：`/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/bifrost-identity-smoke-lwmx0w2c`
- 护栏：`/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/ee-guardrails-o7418gtj`

## 旧冒烟断言适配

首次品牌冒烟失败于已移除 `/api/ee/ping` 原先预期 200 HTML；已验收 RootGuard 要求未知 API 返回 404 JSON。更新该断言，根页面仍验证 200 HTML，复跑通过。

首次身份冒烟失败于向不存在角色 987654 发布通知；角色模块要求受众角色存在，正确返回 404。身份脚本改用全员通知，继续验证原有会话撤销行为；角色受众合法性与过滤由 RBAC 冒烟覆盖，复跑通过。

独立只读合并复核：装配顺序、双方功能保留、Makefile、文档融合、Finding 重号和历史报告完整性通过。测试适配与依赖分类亦经独立复核。没有修改已验收的生产权限规则。

## 交付边界

本记录随合并提交进入 develop；推送使用 `origin develop`，不操作上游 dev。主工作树原有未跟踪工具与构建文件不纳入提交。不触发 Jenkins 或生产部署。
