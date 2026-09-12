#!/bin/sh
# 重构时的表征测试守卫：证明既有测试文件"断言与测试逻辑零改动"。
#   用法: ALLOWED_FILE=<允许集合文件> test_guard.sh <基线SHA> <基线路径>[=<当前路径>] ...
#   允许集合文件每行一条整行锚定的 ERE（# 开头与空行忽略）；匹配到的 diff 行视为方案列名的机械替换。
# 两项检查，任一失败即退出非零：
#   1. 断言行内容逐行相等：基线与当前文件中 t.Fatal*/t.Error* 行完全一致（不只是行数）。
#   2. 剔除允许集合后 diff 为空：改动行除允许集合外不得有其他内容（也就禁止向既有文件新增测试函数）。
set -eu
[ $# -ge 2 ] || { echo "用法: ALLOWED_FILE=<文件> $0 <基线SHA> <基线路径>[=<当前路径>] ..."; exit 2; }
[ -n "${ALLOWED_FILE:-}" ] && [ -r "$ALLOWED_FILE" ] || { echo "ALLOWED_FILE 未设置或不可读"; exit 2; }
base=$1; shift
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
grep -vE '^[[:space:]]*(#|$)' "$ALLOWED_FILE" > "$tmp/allowed" || true   # 只有注释时 grep 返回 1，不能让 set -e 提前退出
[ -s "$tmp/allowed" ] || { echo "允许集合为空"; exit 2; }
rc=0
for pair in "$@"; do
  old=${pair%%=*}; new=${pair#*=}
  git show "$base:$old" > "$tmp/base" || { echo "无法读取基线 $old"; rc=1; continue; }
  [ -r "$new" ] || { echo "当前文件不存在 $new"; rc=1; continue; }
  grep -E 't\.(Fatal|Error)' "$tmp/base" > "$tmp/a" || true
  grep -E 't\.(Fatal|Error)' "$new" > "$tmp/b" || true
  if ! cmp -s "$tmp/a" "$tmp/b"; then
    echo "断言行有变化: $new"; diff "$tmp/a" "$tmp/b" | head -20; rc=1
  fi
  diff -U0 "$tmp/base" "$new" | grep -E '^[-+]' | grep -vE '^(\+\+\+|---)' > "$tmp/d" || true
  # -f 逐条 ERE 匹配；grep -v 留下未被任何允许模式命中的改动行
  grep -vE -f "$tmp/allowed" "$tmp/d" > "$tmp/left" || true
  if [ -s "$tmp/left" ]; then
    echo "允许集合之外的改动: $new"; head -20 "$tmp/left"; rc=1
  fi
done
exit $rc
