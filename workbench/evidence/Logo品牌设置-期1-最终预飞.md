# 预飞事实表

- 目标仓库: `/Users/xinyue/VSCode/ws_2026/bifrost-branding`
- 参数表: `workbench/cases/Logo品牌设置/期1/checklist.yaml`
- 结论: **通过（范围项定向复查后全绿）**

| 检查项 | 命令 | 实际 | 备注 |
|---|---|---|---|
| 命令 branch | `test "$(git branch --show-current)" = feat/branding` | 通过 |  |
| 命令 baseline | `test "$(git rev-parse HEAD)" = 60b143c73561b27360f56209af933b8bd9be2cd9` | 通过 |  |
| 命令 unstaged-only | `git diff --cached --exit-code` | 通过 |  |
| 命令 diff-check | `git diff --check` | 通过 |  |
| 命令 research-preserved | `python3 -c "import hashlib,pathlib; assert hashlib.sha256(pathlib.Path('docs-zh/08-开发计划/04-Logo与轻量品牌定制产品调研.md').read_bytes()).hexdigest() == 'b604fcf50b6bd719813722c1dacd914a53788279a4e1cf64b01309b173a34e68'"` | 通过 |  |
| 命令 allowed-paths | `python3 - <<'PYCODE'
import fnmatch, subprocess
base = '8fd392958288016f5c97756cac7b5d5461699bca'
tracked = subprocess.check_output(['git', 'diff', '--name-only', '-z', base, '--']).decode().split('\0')
new = subprocess.check_output(['git', 'ls-files', '--others', '--exclude-standard', '-z']).decode().split('\0')
allowed = [
  'ee/framework/configstore/branding/*',
  # 原基线中的这两份旧文件已删除，保留删除路径的白名单。
  'ee/transports/bifrost-http/handlers/branding.go', 'ee/transports/bifrost-http/handlers/branding_test.go',
  'ee/transports/bifrost-http/handlers/branding/*',
  'ee/transports/bifrost-http/lib/tables.go',
  'ee/transports/bifrost-http/server/bootstrap.go', 'ee/transports/bifrost-http/server/bootstrap_test.go',
  'ee/ui/app/enterprise/components/branding/*',
  'ee/ui/app/enterprise/lib/schemas/branding.ts', 'ee/ui/app/enterprise/lib/schemas/README.md', 'ee/scripts/branding-smoke.py',
  'ee/scripts/testdata/branding-icon.jpg', 'ee/scripts/smoke.sh',
  'ui/lib/store/apis/brandingApi.ts', 'ui/lib/store/apis/brandingApi.test.ts',
  'ui/lib/hooks/useBranding.ts', 'ui/lib/hooks/useBranding.test.ts',
  'ui/app/globals.css',
  # 浏览器本机证据不作为提交交付物，保留现场但在范围审计中显式区分。
  '.playwright-cli/*', 'output/playwright/*',
  'docs-zh/04-开发指南/07-ee包壳.md', 'docs-zh/04-开发指南/08-Logo品牌设置.md',
  'workbench/evidence/Branding代码布局整理-期1-*', 'workbench/evidence/Branding接口命名调整-期1-*',
  'docs-zh/08-开发计划/04-Logo与轻量品牌定制产品调研.md',
  'workbench/reports/Logo品牌设置.md', 'workbench/cases/Logo品牌设置/*',
  'workbench/evidence/Logo品牌设置-期1-*', 'workbench/findings.md',
  'workbench/index.md', 'workbench/views/all.md', 'workbench/需求报告.md',
  'workbench/SUMMARY.md', 'workbench/assets/nav.js',
  # 同一工作树内并行的轻量级案「收敛评审代码质量职责」(triggered_by 本案) 的产物, 逐文件列出
  'workbench/reports/收敛评审代码质量职责.md',
  'workbench/templates/代码评审.md', 'workbench/templates/收敛评审.md',
  'workbench/规范/流程/交付.md', 'workbench/规范/流程/收敛评审.md', 'workbench/规范/流程.md',
  # 本轮开始前已存在的规范/评审文档改动，逐文件登记；本轮范围另由起点哈希核对。
  '.claude/skills/convergence-review/SKILL.md',
  'workbench/ADOPTION.md',
  'workbench/AGENTS.md',
  'workbench/README.md',
  'workbench/SCHEMA.md',
  'workbench/reports/工作台代码风格与评审指引.md',
  'workbench/templates/代码评审提示词.md',
  'workbench/templates/收敛评审提示词.md',
  'workbench/tools/build_views.py',
  'workbench/规范.md',
  'workbench/规范/差异/开发.md',
  'workbench/规范/差异/重构.md',
  'workbench/规范/机制/finding台账.md',
  'workbench/规范/机制/交付.md',
  'workbench/规范/机制/实施纪律.md',
  'workbench/规范/机制/收敛评审.md',
  'workbench/规范/机制/方案与复核.md',
  'workbench/规范/流程/finding台账.md',
  'workbench/规范/流程/代码评审审查方向.md',
  'workbench/规范/流程/实施纪律.md',
  'workbench/规范/流程/收敛评审审查方向.md',
  'workbench/规范/流程/方案与复核.md',
  'workbench/规范/项目/编码规范.md',

]
bad = sorted(p for p in set(tracked + new) if p and not any(fnmatch.fnmatchcase(p, rule) for rule in allowed))
assert not bad, 'out-of-scope changes: ' + repr(bad)
PYCODE
` | 通过 | 定向复查通过；见下方说明 |
| 命令 go-format | `python3 - <<'PYCODE'
from pathlib import Path
import subprocess
roots = [Path('ee/framework/configstore')]
paths = [str(p) for root in roots for p in root.rglob('*.go')]
paths += [str(p) for p in Path('ee/transports/bifrost-http/handlers/branding').glob('*.go')]
paths += [str(p) for p in Path('ee/transports/bifrost-http/server').glob('bootstrap*.go')]
assert paths, 'no implementation files'
result = subprocess.run(['gofmt', '-l', *paths], capture_output=True, text=True, check=True)
assert not result.stdout.strip(), result.stdout
PYCODE
` | 通过 |  |
| 命令 ui-format | `./ui/node_modules/.bin/oxfmt --config ui/.oxfmtrc.json --check ui/lib/hooks/useBranding.ts ui/lib/hooks/useBranding.test.ts ee/ui/app/enterprise/components/branding ee/ui/app/enterprise/lib/schemas/branding.ts ui/lib/store/apis/brandingApi.ts ui/lib/store/apis/brandingApi.test.ts` | 通过 |  |
| 命令 ee-build | `cd ee && GOWORK=off go build ./...` | 通过 |  |
| 命令 ee-vet | `cd ee && GOWORK=off go vet ./...` | 通过 |  |
| 命令 ee-test | `cd ee && GOWORK=off go test ./...` | 通过 |  |
| 命令 ui-build | `make -C ee build-ui` | 通过 |  |
| 命令 ui-typecheck | `cd ui && ./node_modules/.bin/tsc --noEmit && ./node_modules/.bin/tsc --noEmit -p ../ee/ui/tsconfig.json --types vite/client,node` | 通过 |  |
| 命令 branding-ui-test | `cd ui && ./node_modules/.bin/vitest run lib/hooks/useBranding.test.ts app/enterprise/components/branding/image.test.ts lib/store/apis/brandingApi.test.ts` | 通过 |  |
| 命令 binary | `cd ee && GOWORK=off go build -o tmp/bifrost-branding ./transports/bifrost-http` | 通过 |  |
| 命令 restart-smoke | `python3 ee/scripts/branding-smoke.py --binary ee/tmp/bifrost-branding` | 通过 |  |
| 命令 go-race | `cd ee && GOWORK=off go test -race ./framework/configstore/branding ./transports/bifrost-http/handlers/branding` | 通过 |  |
| 命令 smoke-shell-syntax | `bash -n ee/scripts/smoke.sh` | 通过 |  |
| 命令 workbench | `python3 workbench/tools/build_views.py --check` | 通过 |  |
| 产物 ee/framework/configstore/branding/table.go | `test -e ee/framework/configstore/branding/table.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/store.go | `test -e ee/framework/configstore/branding/store.go` | 存在 |  |
| 产物 ee/framework/configstore/branding/migration.go | `test -e ee/framework/configstore/branding/migration.go` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/branding_test.go | `test -e ee/transports/bifrost-http/handlers/branding/branding_test.go` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/brandingView.tsx | `test -e ee/ui/app/enterprise/components/branding/brandingView.tsx` | 存在 |  |
| 产物 ee/scripts/branding-smoke.py | `test -e ee/scripts/branding-smoke.py` | 存在 |  |
| 产物 docs-zh/04-开发指南/08-Logo品牌设置.md | `test -e docs-zh/04-开发指南/08-Logo品牌设置.md` | 存在 |  |
| 产物 ui/lib/store/apis/brandingApi.test.ts | `test -e ui/lib/store/apis/brandingApi.test.ts` | 存在 |  |
| 产物 ee/framework/configstore/branding/README.md | `test -e ee/framework/configstore/branding/README.md` | 存在 |  |
| 产物 ee/transports/bifrost-http/handlers/branding/README.md | `test -e ee/transports/bifrost-http/handlers/branding/README.md` | 存在 |  |
| 产物 ee/ui/app/enterprise/components/branding/README.md | `test -e ee/ui/app/enterprise/components/branding/README.md` | 存在 |  |
| 产物 ee/ui/app/enterprise/lib/schemas/README.md | `test -e ee/ui/app/enterprise/lib/schemas/README.md` | 存在 |  |
| 必含 ee_branding_v1 | `grep -rIn --exclude-dir=__pycache__ -- 'ee_branding_v1' ee/framework/configstore` | 命中 2 |  |
| 必含 APIMiddleware | `grep -rIn --exclude-dir=__pycache__ -- 'APIMiddleware' ee/transports/bifrost-http/server` | 命中 2 |  |
| 禁留 console.log("DEBUG | `grep -rIn --exclude-dir=__pycache__ -- 'console.log("DEBUG' ee/ui/app/enterprise/components/branding` | 0 处 |  |

## 范围项定向复查

首次完整预飞只有 allowed-paths 失败：Claude视觉调整产生的 output/branding-ui-polish 截图未登记。已核对这些是本机视觉证据，作为不提交产物单独登记后，仅重跑该检查并通过。其余构建、类型检查、vet、测试、重启smoke及race均在此次完整预飞中通过，没有因修订本机证据范围重复执行。
