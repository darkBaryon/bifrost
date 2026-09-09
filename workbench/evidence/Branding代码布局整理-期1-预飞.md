# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-branding`
- 参数表: `workbench/evidence/Logo品牌设置-期1-代码布局整理检查清单.yaml`
- 结论: **全绿**

| 检查项 | 命令 | 实际 | 备注 |
|---|---|---|---|
| 命令 diff-check | `git diff --check` | 通过 |  |
| 命令 go-format | `python3 - <<'PY'
import subprocess
from pathlib import Path
paths = [str(p) for base in ['ee/framework/configstore/branding', 'ee/transports/bifrost-http/handlers/branding'] for p in Path(base).glob('*.go')]
paths += ['ee/transports/bifrost-http/server/bootstrap.go', 'ee/transports/bifrost-http/lib/tables.go']
result = subprocess.check_output(['gofmt', '-l', *paths], text=True)
assert not result.strip(), result
PY
` | 通过 |  |
| 命令 go-vet | `cd ee && GOWORK=off go vet ./...` | 通过 |  |
| 命令 go-test | `cd ee && GOWORK=off go test -count=1 ./...` | 通过 |  |
| 命令 go-race | `cd ee && GOWORK=off go test -race ./framework/configstore/branding ./transports/bifrost-http/handlers/branding` | 通过 |  |
| 命令 ui-format | `./ui/node_modules/.bin/oxfmt --config ui/.oxfmtrc.json --check ee/ui/app/enterprise/components/branding ee/ui/app/enterprise/lib/schemas/branding.ts` | 通过 |  |
| 命令 ui-build | `make -C ee build-ui` | 通过 |  |
| 命令 ui-typecheck | `cd ui && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/tsc --noEmit -p ../ee/ui/tsconfig.json --types vite/client,node` | 通过 |  |
| 命令 ui-test | `cd ui && ./node_modules/.bin/vitest run lib/hooks/useBranding.test.ts app/enterprise/components/branding/image.test.ts` | 通过 |  |
| 命令 binary | `cd ee && GOWORK=off go build -o tmp/bifrost-branding ./transports/bifrost-http` | 通过 |  |
| 命令 smoke | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding` | 通过 |  |
| 命令 workbench | `python3 workbench/tools/build_views.py --check` | 通过 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/handler.go | `test -e ee/transports/bifrost-http/handlers/branding/handler.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/settings.go | `test -e ee/transports/bifrost-http/handlers/branding/settings.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/validation.go | `test -e ee/transports/bifrost-http/handlers/branding/validation.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/asset.go | `test -e ee/transports/bifrost-http/handlers/branding/asset.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/store.go | `test -e ee/framework/configstore/branding/store.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/migration.go | `test -e ee/framework/configstore/branding/migration.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/table.go | `test -e ee/framework/configstore/branding/table.go` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/useBrandingForm.ts | `test -e ee/ui/app/enterprise/components/branding/useBrandingForm.ts` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/image.ts | `test -e ee/ui/app/enterprise/components/branding/image.ts` | 存在 |  |
| 必含 ee_branding_v1 | `grep -rIn --exclude-dir=__pycache__ -- 'ee_branding_v1' ee/framework/configstore/branding` | 命中 1 |  |
| 禁留 console.log("DEBUG | `grep -rIn --exclude-dir=__pycache__ -- 'console.log("DEBUG' ee/ui/app/enterprise/components/branding` | 0 处 |  |
