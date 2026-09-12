#!/bin/sh
# 构建某个历史提交的 EE 二进制，供升级冒烟（identity-upgrade-smoke.py --legacy-binary）当作旧版本。
#   用法: build_legacy_ee.sh <commit> <输出路径>
# 幂等：输出已存在则直接返回；要重建先删掉输出文件。构建在临时 worktree 里进行，结束即移除。
set -eu
[ $# -eq 2 ] || { echo "用法: $0 <commit> <输出路径>"; exit 2; }
sha=$1; out=$2
if [ -x "$out" ]; then echo "已存在 ${out}（commit 见同名 .commit 文件）"; exit 0; fi
mkdir -p "$(dirname "$out")"
abs_out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
src=$(mktemp -d)
git worktree add --detach "$src" "$sha" >/dev/null 2>&1
trap 'git worktree remove --force "$src" >/dev/null 2>&1 || true' EXIT
( cd "$src/ee" && GOWORK=off go build -o "$abs_out" ./cmd/bifrost-http )
git rev-parse "$sha" > "$abs_out.commit"
echo "已构建 $out ← $(git rev-parse --short "$sha")"
