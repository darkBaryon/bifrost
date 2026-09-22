# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-console-ui`
- 参数表: `workbench/cases/工作台基础信息解耦/期1/checklist.yaml`
- 结论: **有红项**

| 检查项 | 命令 | 实际 | 备注 |
|---|---|---|---|
| 命令 workbench | `python3 -B workbench/tools/build_views.py --check` | 通过 |  |
| 命令 ee-vet | `cd ee && GOWORK=off go vet ./...` | 通过 |  |
| 命令 console-tests | `cd ee && GOWORK=off go test -race ./internal/host/... ./internal/identity/http/... ./internal/rbac/host/... ./internal/app/...` | 通过 |  |
| 命令 console-smoke | `python3 ee/scripts/console-bootstrap-smoke.py` | 失败 | FAIL format failed; inspect /private/var/folders/m3/56qrlvk11xs7nc4cxjnw1b700000gn/T/bifrost-console-check-1s1f7ven/build/format.log |
| 产物 ee/internal/host/console.go | `test -e ee/internal/host/console.go` | 存在 |  |
| 产物 ee/ui/app/enterprise/hooks/useConsoleConfig.ts | `test -e ee/ui/app/enterprise/hooks/useConsoleConfig.ts` | 存在 |  |
| 产物 ee/docs/工作台基础信息接口.md | `test -e ee/docs/工作台基础信息接口.md` | 存在 |  |
| 产物 workbench/evidence/工作台基础信息解耦-期1-验证.md | `test -e workbench/evidence/工作台基础信息解耦-期1-验证.md` | 存在 |  |
| 必含 /api/console/bootstrap | `grep -rIn --exclude-dir=__pycache__ -- '/api/console/bootstrap' ee/internal/host` | 命中 18 |  |
| 禁留 console.log("DEBUG | `grep -rIn --exclude-dir=__pycache__ -- 'console.log("DEBUG' ee/ui` | 0 处 |  |
