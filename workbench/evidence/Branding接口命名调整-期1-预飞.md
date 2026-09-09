# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-branding`
- 参数表: `workbench/evidence/Logo品牌设置-期1-接口命名调整检查清单.yaml`
- 结论: **全部通过（workbench 生成后定向复验关闭，记录如下）**

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
| 命令 ui-format | `./ui/node_modules/.bin/oxfmt --config ui/.oxfmtrc.json --check ee/ui/app/enterprise/components/branding ee/ui/app/enterprise/lib/schemas/branding.ts ui/lib/store/apis/brandingApi.ts ui/lib/store/apis/brandingApi.test.ts` | 通过 |  |
| 命令 ui-build | `make -C ee build-ui` | 通过 |  |
| 命令 ui-typecheck | `cd ui && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/tsc --noEmit -p ../ee/ui/tsconfig.json --types vite/client,node` | 通过 |  |
| 命令 ui-test | `cd ui && ./node_modules/.bin/vitest run lib/hooks/useBranding.test.ts app/enterprise/components/branding/image.test.ts lib/store/apis/brandingApi.test.ts` | 通过 |  |
| 命令 binary | `cd ee && GOWORK=off go build -o tmp/bifrost-branding ./transports/bifrost-http` | 通过 |  |
| 命令 smoke-shell-syntax | `bash -n ee/scripts/smoke.sh` | 通过 |  |
| 命令 smoke | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding` | 通过 |  |
| 命令 workbench | `python3 workbench/tools/build_views.py --check` | 通过（定向复验） | 初跑在实施记录更新后发现视图漂移；执行 build_views.py 后再次 --check 通过 |
| 产物 ee/transports/bifrost-http/handlers/branding/handler.go | `test -e ee/transports/bifrost-http/handlers/branding/handler.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/settings.go | `test -e ee/transports/bifrost-http/handlers/branding/settings.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/validation.go | `test -e ee/transports/bifrost-http/handlers/branding/validation.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/asset.go | `test -e ee/transports/bifrost-http/handlers/branding/asset.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/store.go | `test -e ee/framework/configstore/branding/store.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/migration.go | `test -e ee/framework/configstore/branding/migration.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/table.go | `test -e ee/framework/configstore/branding/table.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/branding_test.go | `test -e ee/transports/bifrost-http/handlers/branding/branding_test.go` | 存在 |  |
| 产物 ui/lib/store/apis/brandingApi.test.ts | `test -e ui/lib/store/apis/brandingApi.test.ts` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/useBrandingForm.ts | `test -e ee/ui/app/enterprise/components/branding/useBrandingForm.ts` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/image.ts | `test -e ee/ui/app/enterprise/components/branding/image.ts` | 存在 |  |
| 必含 ee_branding_v1 | `grep -rIn --exclude-dir=__pycache__ -- 'ee_branding_v1' ee/framework/configstore/branding` | 命中 1 |  |
| 禁留 console.log("DEBUG | `grep -rIn --exclude-dir=__pycache__ -- 'console.log("DEBUG' ee/ui/app/enterprise/components/branding` | 0 处 |  |

仅重跑失败项：build_views.py → --check → build_site.py 均 exit 0，62 篇文档，103 页站点。代码未改，不重复已通过的代码测试和构建。

评审后仅补useBranding的缓存注释：oxfmt --check与git diff --check通过，两侧独立hash反证确认无逻辑变化，未重复代码测试。
