# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-branding`
- 参数表: `workbench/cases/Logo品牌设置/期1/checklist.yaml`
- 结论: **全绿**

| 检查项 | 命令 | 实际 | 备注 |
|---|---|---|---|
| 命令 branch | `test "$(git branch --show-current)" = feat/branding` | 通过 |  |
| 命令 baseline | `test "$(git rev-parse HEAD)" = 8fd392958288016f5c97756cac7b5d5461699bca` | 通过 |  |
| 命令 unstaged-only | `git diff --cached --exit-code` | 通过 |  |
| 命令 diff-check | `git diff --check` | 通过 |  |
| 命令 research-preserved | `python3 -c "import hashlib,pathlib; assert hashlib.sha256(pathlib.Path('docs-zh/08-开发计划/04-Logo与轻量品牌定制产品调研.md').read_bytes()).hexdigest() == 'b604fcf50b6bd719813722c1dacd914a53788279a4e1cf64b01309b173a34e68'"` | 通过 |  |
| 命令 allowed-paths | `python3 - <<'PYCODE'
import fnmatch, subprocess
base = '8fd392958288016f5c97756cac7b5d5461699bca'
tracked = subprocess.check_output(['git', 'diff', '--name-only', '-z', base, '--']).decode().split('\0')
new = subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '-z']).decode().split('\0')
allowed = [
  'ee/framework/configstore/branding.go', 'ee/framework/configstore/branding_test.go',
  'ee/framework/configstore/migrations.go', 'ee/framework/configstore/migrations_test.go',
  'ee/framework/configstore/tables/branding.go',
  'ee/transports/bifrost-http/handlers/branding.go', 'ee/transports/bifrost-http/handlers/branding_test.go',
  'ee/transports/bifrost-http/server/bootstrap.go', 'ee/transports/bifrost-http/server/bootstrap_test.go',
  'ee/ui/app/enterprise/components/branding/brandingView.tsx',
  'ee/ui/app/enterprise/components/branding/brandingPreview.tsx',
  'ee/ui/app/enterprise/components/branding/brandingUpload.tsx',
  'ee/ui/app/enterprise/lib/schemas/branding.ts', 'ee/scripts/branding-smoke.py',
  'ee/scripts/testdata/branding-icon.jpg',
  'ui/lib/hooks/useBranding.ts', 'ui/lib/hooks/useBranding.test.ts',
  'docs-zh/04-开发指南/08-Logo品牌设置.md',
  'docs-zh/08-开发计划/04-Logo与轻量品牌定制产品调研.md',
  'workbench/reports/Logo品牌设置.md', 'workbench/cases/Logo品牌设置/*',
  'workbench/evidence/Logo品牌设置-期1-*', 'workbench/findings.md',
  'workbench/index.md', 'workbench/views/all.md', 'workbench/需求报告.md',
  'workbench/SUMMARY.md', 'workbench/assets/nav.js',
  # 同一工作树内并行的轻量级案「收敛评审代码质量职责」(triggered_by 本案) 的产物, 逐文件列出
  'workbench/reports/收敛评审代码质量职责.md',
  'workbench/templates/代码评审.md', 'workbench/templates/收敛评审.md',
  'workbench/规范/机制/交付.md', 'workbench/规范/机制/收敛评审.md', 'workbench/规范/流程.md',
]
bad = sorted(p for p in set(tracked + new) if p and not any(fnmatch.fnmatchcase(p, rule) for rule in allowed))
assert not bad, 'out-of-scope changes: ' + repr(bad)
PYCODE
` | 通过 |  |
| 命令 go-format | `python3 - <<'PYCODE'
from pathlib import Path
import subprocess
roots = [Path('ee/framework/configstore')]
paths = [str(p) for root in roots for p in root.rglob('*.go')]
paths += [str(p) for p in Path('ee/transports/bifrost-http/handlers').glob('branding*.go')]
paths += [str(p) for p in Path('ee/transports/bifrost-http/server').glob('bootstrap*.go')]
assert paths, 'no implementation files'
result = subprocess.run(['gofmt', '-l', *paths], capture_output=True, text=True, check=True)
assert not result.stdout.strip(), result.stdout
PYCODE
` | 通过 |  |
| 命令 ui-format | `./ui/node_modules/.bin/oxfmt --config ui/.oxfmtrc.json --check ui/lib/hooks/useBranding.ts ui/lib/hooks/useBranding.test.ts ee/ui/app/enterprise/components/branding ee/ui/app/enterprise/lib/schemas/branding.ts` | 通过 |  |
| 命令 ee-build | `cd ee && GOWORK=off go build ./...` | 通过 |  |
| 命令 ee-vet | `cd ee && GOWORK=off go vet ./...` | 通过 |  |
| 命令 ee-test | `cd ee && GOWORK=off go test ./...` | 通过 |  |
| 命令 ui-build | `make -C ee build-ui` | 通过 |  |
| 命令 ui-typecheck | `cd ui && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/tsc --noEmit -p ../ee/ui/tsconfig.json --types vite/client,node` | 通过 |  |
| 命令 branding-ui-test | `cd ui && ./node_modules/.bin/vitest run lib/hooks/useBranding.test.ts` | 通过 |  |
| 命令 binary | `cd ee && GOWORK=off go build -o tmp/bifrost-branding ./transports/bifrost-http` | 通过 |  |
| 命令 restart-smoke | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding` | 通过 |  |
| 命令 workbench | `python3 workbench/tools/build_views.py --check` | 通过 |  |
| 产物 ee/framework/configstore/tables/branding.go | `test -e ee/framework/configstore/tables/branding.go` | 存在 |  |
| 产物 ee/framework/configstore/branding.go | `test -e ee/framework/configstore/branding.go` | 存在 |  |
| 产物 ee/framework/configstore/migrations.go | `test -e ee/framework/configstore/migrations.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding_test.go | `test -e ee/transports/bifrost-http/handlers/branding_test.go` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/brandingView.tsx | `test -e ee/ui/app/enterprise/components/branding/brandingView.tsx` | 存在 |  |
| 产物 ee/scripts/branding-smoke.py | `test -e ee/scripts/branding-smoke.py` | 存在 |  |
| 产物 docs-zh/04-开发指南/08-Logo品牌设置.md | `test -e docs-zh/04-开发指南/08-Logo品牌设置.md` | 存在 |  |
| 必含 ee_branding_v1 | `grep -rIn --exclude-dir=__pycache__ -- 'ee_branding_v1' ee/framework/configstore` | 命中 1 |  |
| 必含 APIMiddleware | `grep -rIn --exclude-dir=__pycache__ -- 'APIMiddleware' ee/transports/bifrost-http/server` | 命中 2 |  |
| 禁留 console.log("DEBUG | `grep -rIn --exclude-dir=__pycache__ -- 'console.log("DEBUG' ee/ui/app/enterprise/components/branding` | 0 处 |  |
