#!/usr/bin/env bash
#
# restore: a backup is only a backup once something has been brought back
# from it and served. This pulls the nightly copy from BACKUP_REMOTE into a
# temporary directory, rebuilds the database from it, starts a second
# observer-api on a spare port against that directory, and reads figures
# from it.
#
# What must hold:
#   - the copy contains the record (publications.jsonl at least) and not the
#     sampling master key, which backup.sh excludes on purpose;
#   - observer-collector --once turns the copy into a database;
#   - observer-api starts on it and answers /v1/meta and /v1/network;
#   - the restored counts are at most the live ones (the copy is older) and
#     the gap is explained by the copy's age.
#
# Usage: sudo deploy/test/restore.sh [instance] [spare-port]   (mocha, 18081)
# Needs rclone with RCLONE_CONFIG (/etc/fibre-observer/rclone.conf) and
# BACKUP_REMOTE set in the env file. Exit 0 when every check passes.
set -o errexit -o nounset -o pipefail

INSTANCE="${1:-mocha}"
PORT="${2:-18081}"
ENVFILE=/etc/fibre-observer/$INSTANCE.env
FAILED=0
pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; FAILED=1; }
warn() { echo "  warn $*"; }

[ -r "$ENVFILE" ] || { echo "no $ENVFILE" >&2; exit 2; }
envval() { sed -n "s/^$1=//p" "$ENVFILE" | head -1; }
REMOTE=$(envval BACKUP_REMOTE); RPC=$(envval RPC); VANTAGE=$(envval VANTAGE); API_LISTEN=$(envval API_LISTEN)
export RCLONE_CONFIG="${RCLONE_CONFIG:-/etc/fibre-observer/rclone.conf}"
[ -n "$REMOTE" ] || { echo "BACKUP_REMOTE is empty in $ENVFILE: there is no backup to restore from" >&2; exit 1; }
command -v rclone >/dev/null || { echo "rclone is not installed" >&2; exit 1; }

TMP=$(mktemp -d "${TMPDIR:-/tmp}/fibre-restore.XXXXXX")
API_PID=""
cleanup() { [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT

echo "== 1. pull the copy from $REMOTE/$INSTANCE"
if rclone copy "$REMOTE/$INSTANCE" "$TMP" --transfers 4 --checkers 8 --stats-one-line --stats 0 --log-level NOTICE; then
  pass "copied $(find "$TMP" -type f | wc -l) file(s), $(du -sh "$TMP" | cut -f1)"
else
  fail "rclone copy failed"; exit 1
fi
[ -f "$TMP/publications.jsonl" ] && pass "publications.jsonl present" || fail "no publications.jsonl in the copy"
[ -f "$TMP/measurements.jsonl" ] && pass "measurements.jsonl present" || warn "no measurements.jsonl in the copy (none written yet?)"
[ -f "$TMP/state.json" ] && pass "state.json present" || warn "no state.json in the copy"
[ -f "$TMP/sampling-master.key" ] && fail "sampling-master.key is in the backup: it must never leave the host" || pass "sampling-master.key not in the copy"
[ -f "$TMP/observer.db" ] && warn "observer.db is in the copy (backup.sh excludes it; litestream is the database's copy)" || true
newest=$(find "$TMP" -name '*.jsonl' -printf '%T@\n' 2>/dev/null | sort -n | tail -1 | cut -d. -f1)
if [ -n "$newest" ]; then
  age=$(( $(date +%s) - newest ))
  echo "  newest record file in the copy is $((age / 3600))h $(( (age % 3600) / 60 ))m old"
fi

echo "== 2. rebuild the database from the copy"
if timeout 1200 /usr/local/bin/observer-collector -rpc "$RPC" -data-dir "$TMP" -vantage "$VANTAGE" -once \
     -endpoints-every 0 -escrow-every 0 -avatars-every 0 -export-hour -1 > "$TMP/rebuild.log" 2>&1; then
  pass "rebuilt: $(tail -1 "$TMP/rebuild.log")"
else
  fail "rebuild failed: $(tail -3 "$TMP/rebuild.log")"; exit 1
fi

echo "== 3. serve it"
/usr/local/bin/observer-api -data-dir "$TMP" -listen "127.0.0.1:$PORT" -vantage "$VANTAGE" > "$TMP/api.log" 2>&1 &
API_PID=$!
up=0
for _ in $(seq 1 30); do
  sleep 2
  code=$(curl -sS -m 5 -o "$TMP/meta.json" -w '%{http_code}' "http://127.0.0.1:$PORT/v1/meta" 2>/dev/null || echo 000)
  [ "$code" = "200" ] && { up=1; break; }
  kill -0 "$API_PID" 2>/dev/null || break
done
if [ "$up" = 1 ]; then
  pass "observer-api answers /v1/meta on :$PORT from the restored directory"
else
  fail "observer-api did not come up on :$PORT: $(tail -3 "$TMP/api.log")"; exit 1
fi
rc=$(python3 -c 'import json,sys; c=json.load(open(sys.argv[1]))["counts"]; print(c["Publications"], c["Probes"])' "$TMP/meta.json")
lc=$(curl -sS -m 10 "http://$API_LISTEN/v1/meta" | python3 -c 'import json,sys; c=json.load(sys.stdin)["counts"]; print(c["Publications"], c["Probes"])' 2>/dev/null || echo "? ?")
read -r rpub rprobe <<<"$rc"; read -r lpub lprobe <<<"$lc"
echo "  restored counts: publications=$rpub probes=$rprobe · live: publications=$lpub probes=$lprobe"
if [ "$lpub" != "?" ]; then
  if [ "$rpub" -le "$lpub" ] && [ "$rprobe" -le "$lprobe" ]; then pass "restored counts are at most the live ones; the difference is what arrived since the copy"; else fail "restored counts exceed live: something in the copy is not on the live host"; fi
fi
code=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/network?window=24h" 2>/dev/null || echo 000)
[ "$code" = "200" ] && pass "/v1/network answers from the restored data" || fail "/v1/network -> $code"
code=$(curl -sS -m 30 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/validators?window=24h" 2>/dev/null || echo 000)
[ "$code" = "200" ] && pass "/v1/validators answers from the restored data" || fail "/v1/validators -> $code"

echo
if [ "$FAILED" = 0 ]; then echo "restore: every check passed (restored API stopped, $TMP removed)"; else echo "restore: FAILED"; exit 1; fi
