#!/usr/bin/env bash
# ee 骨架冒烟: ee 版 (18080) 与 OSS 版 (18081) 各起一份, 对同一 config.db 的两份副本跑 9 条断言 (T1-T8).
# 前提: 已 make -C ee build (ee/tmp/bifrost-http 含 UI); 本机 ~/.config/bifrost/config.db 存在;
#       上游 transports/bifrost-http/ui/ 目录存在 (OSS 对照用 go run, 其 embed 需要该目录).
# 总则: OSS 对照一律不比状态码 (上游 /{filepath:*} 把未知 /api/* 兜底成 200 HTML), 只比 Content-Type 与结构.
set -uo pipefail
cd "$(dirname "$0")/../.."   # 仓库根

APP_SRC="${APP_SRC:-$HOME/.config/bifrost/config.db}"
EE_PORT=18080; OSS_PORT=18081
TMP="$(mktemp -d)"; mkdir -p "$TMP/ee" "$TMP/oss"
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  # go run 的子进程不随父 shell 退出, 按端口再杀一遍
  for port in $EE_PORT $OSS_PORT; do lsof -ti "tcp:$port" -sTCP:LISTEN 2>/dev/null | xargs kill 2>/dev/null || true; done
  rm -rf "$TMP"
}
trap cleanup EXIT
cp "$APP_SRC" "$TMP/ee/config.db" || { echo "missing $APP_SRC"; exit 1; }
cp "$APP_SRC" "$TMP/oss/config.db" || exit 1

wait_up() { for _ in $(seq 1 90); do curl -sf -m 2 "http://localhost:$1/api/version" >/dev/null && return 0; sleep 1; done; echo "port $1 did not come up"; return 1; }

echo "== starting ee (ee/tmp/bifrost-http) on $EE_PORT"
./ee/tmp/bifrost-http -port $EE_PORT -app-dir "$TMP/ee" > "$TMP/ee.log" 2>&1 & PIDS+=($!)
echo "== starting OSS (go run ./transports/bifrost-http) on $OSS_PORT"
( cd transports/bifrost-http && go run . -port $OSS_PORT -app-dir "$TMP/oss" > "$TMP/oss.log" 2>&1 ) & PIDS+=($!)
wait_up $EE_PORT || { tail -20 "$TMP/ee.log"; exit 1; }
wait_up $OSS_PORT || { tail -20 "$TMP/oss.log"; exit 1; }

pass=0; fail=0
check() { if eval "$2"; then echo "PASS $1"; pass=$((pass+1)); else echo "FAIL $1"; fail=$((fail+1)); fi; }
names() { curl -s "http://localhost:$1/api/providers" | python3 -c 'import sys,json; print(",".join(sorted(p["name"] for p in json.load(sys.stdin)["providers"])))'; }
ctype() { curl -s -X "${3:-GET}" -o /dev/null -w '%{content_type}' "http://localhost:$1$2"; }

EE_NAMES=$(names $EE_PORT); OSS_NAMES=$(names $OSS_PORT)
check "T1 providers equal ($EE_NAMES)" '[ -n "$EE_NAMES" ] && [ "$EE_NAMES" = "$OSS_NAMES" ]'
check "T2 /api/ee/ping ok=true json" 'curl -s http://localhost:$EE_PORT/api/ee/ping | python3 -c "import sys,json; d=json.load(sys.stdin); sys.exit(0 if d.get(\"ok\") is True else 1)"'
check "T3 ee_probe table exists" 'sqlite3 "$TMP/ee/config.db" ".tables" | tr -s " \n" " " | grep -qw ee_probe'
check "T4 X-Bifrost-EE header" 'curl -s -D - -o /dev/null http://localhost:$EE_PORT/api/version | grep -qi "^x-bifrost-ee: 1"'
check "T5a plugin ee-probe active" 'grep -q "plugin status: ee-probe - active" "$TMP/ee.log"'
check "T5b config_plugins has no ee-probe" '[ "$(sqlite3 "$TMP/ee/config.db" "select count(*) from config_plugins where name='"'"'ee-probe'"'"'")" = "0" ]'
check "T6 POST /api/branding/get json enabled=false; OSS GET fallback text/html" '[[ "$(ctype $EE_PORT /api/branding/get POST)" == application/json* ]] && curl -s -X POST http://localhost:$EE_PORT/api/branding/get | grep -q "\"enabled\":false" && [[ "$(ctype $OSS_PORT /api/branding)" == text/html* ]]'
check "T7 shell meta injected in ee only" 'curl -s http://localhost:$EE_PORT/ | grep -q "x-bifrost-ee" && ! curl -s http://localhost:$OSS_PORT/ | grep -q "x-bifrost-ee"'
check "T8 no error logs in ee" '! grep -q "\"level\":\"error\"" "$TMP/ee.log"'

echo "== $pass passed, $fail failed"
[ "$fail" -eq 0 ]
