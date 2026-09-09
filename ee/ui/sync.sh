#!/usr/bin/env bash
# 生成上游 ui/app/enterprise 覆盖层:
#   1. 复制上游占位实现 ui/app/_fallbacks/enterprise → ui/app/enterprise (上游 vite 以该目录存在与否切换 @enterprise 别名)
#   2. 把 ee/ui/app/enterprise 下的每个文件逐个符号链接盖上去 (只放我们真改的文件; vite dev 热更新可用)
# 上游 make install-ui (被 make dev 依赖) 会 rm -rf ui/app/enterprise, 属预期, 再跑一次本脚本即可.
# 注意: 覆盖层存在 ⇒ 上游 UI 编成企业模式 (IS_ENTERPRISE=true), 差异清单见 product/docs-zh/04-开发指南/07-ee包壳.md
set -euo pipefail
cd "$(dirname "$0")/../.."   # 仓库根

SRC=ui/app/_fallbacks/enterprise
DST=ui/app/enterprise
OVERLAY=ee/ui/app/enterprise

[ -d "$SRC" ] || { echo "missing $SRC (upstream fallbacks)"; exit 1; }
rm -rf "$DST"
cp -R "$SRC" "$DST"

( cd "$OVERLAY" && find . -type f -print0 ) | while IFS= read -r -d '' f; do
  f="${f#./}"
  mkdir -p "$DST/$(dirname "$f")"
  ln -sf "$(pwd)/$OVERLAY/$f" "$DST/$f"
  echo "  overlay: $f"
done
echo "ui overlay ready at $DST (base: $SRC, overlay: $OVERLAY)"
