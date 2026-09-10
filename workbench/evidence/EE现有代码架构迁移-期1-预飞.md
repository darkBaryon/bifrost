# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-ee-architecture`
- 参数表: `workbench/cases/EE现有代码架构迁移/期1/checklist.yaml`
- 结论: **全绿**

| 检查项 | 命令 | 实际 | 备注 |
|---|---|---|---|
| 命令 ee-tests | `cd ee && GOWORK=off go test ./...` | 通过 |  |
| 命令 ee-vet | `cd ee && GOWORK=off go vet ./...` | 通过 |  |
| 命令 ee-build-with-ui | `make -C ee build` | 通过 |  |
| 命令 isolated-smoke | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-http` | 通过 |  |
| 命令 diff-check | `git diff --check` | 通过 |  |
| 产物 ee/cmd/bifrost-http/main.go | `test -e ee/cmd/bifrost-http/main.go` | 存在 |  |
| 产物 ee/internal/app/bootstrap.go | `test -e ee/internal/app/bootstrap.go` | 存在 |  |
| 产物 ee/internal/branding/service.go | `test -e ee/internal/branding/service.go` | 存在 |  |
| 产物 ee/internal/branding/http/handler.go | `test -e ee/internal/branding/http/handler.go` | 存在 |  |
| 产物 ee/internal/branding/persistence/migration.go | `test -e ee/internal/branding/persistence/migration.go` | 存在 |  |
| 必含 func DecodeAsset | `grep -rIn --exclude-dir=__pycache__ -- 'func DecodeAsset' ee/internal/branding` | 命中 1 |  |
| 禁留 github.com/darkBaryon/bifrost/ee/transports | `grep -rIn --exclude-dir=__pycache__ -- 'github.com/darkBaryon/bifrost/ee/transports' ee` | 0 处 |  |
| 禁留 github.com/darkBaryon/bifrost/ee/framework | `grep -rIn --exclude-dir=__pycache__ -- 'github.com/darkBaryon/bifrost/ee/framework' ee` | 0 处 |  |
| 禁留 ProbePlugin | `grep -rIn --exclude-dir=__pycache__ -- 'ProbePlugin' ee/internal` | 0 处 |  |
| 禁留 NewProbeHandler | `grep -rIn --exclude-dir=__pycache__ -- 'NewProbeHandler' ee/internal` | 0 处 |  |
| 禁留 EEHeaderMiddleware | `grep -rIn --exclude-dir=__pycache__ -- 'EEHeaderMiddleware' ee/internal` | 0 处 |  |
| 禁留 EEShellRewriter | `grep -rIn --exclude-dir=__pycache__ -- 'EEShellRewriter' ee/internal` | 0 处 |  |
| 禁留 ee_probe | `grep -rIn --exclude-dir=__pycache__ -- 'ee_probe' ee/internal` | 0 处 |  |

补充定向验证：探针旧路径实际走上游 SPA fallback（200、text/html）；加强脚本状态与类型断言后，独立重跑隔离冒烟通过。当前入口及包级 12 份文档的本地链接有效。PostgreSQL 专用迁移锁测试因未配置测试库而未执行。
