#!/usr/bin/env bash
#
# outage: cut the observer off on purpose, watch the site admit it, put it
# back, and check that nothing was lost or doubled.
#
#   A. chain source cut. The four units that read the RPC are pointed at a
#      dead port and restarted. Within OUTAGE_MIN minutes /v1/health must
#      answer 503 — the API up and degraded, not gone — with a body whose
#      failing checks name the chain source: chain_liveness (no new block
#      seen for ten minutes) or a reader that is alive but failing. A
#      failing check with another name does not count. /v1/network keeps
#      serving its last figures throughout, which is the design.
#   B. source restored. Within RECOVER_MIN minutes: health 200, the scanner
#      within twenty blocks of the chain tip, no scan gap added (a transport
#      failure is retried, never recorded as a gap the node could not
#      serve), every row count at or above what it was, and no duplicate
#      line in the record.
#   C. every process stopped for STOP_MIN minutes, then started. The API is
#      down too (000, no HTTP answer at all), then 200 again within
#      RECOVER_MIN, with the scanner checkpoint at or past where it was.
#
# This script edits the instance's env file (RPC=) and restores it on every
# exit path, including a failed check. Run it against a test instance, not
# a production one; it is the RPC setting it changes.
#
# Usage: sudo deploy/test/outage.sh [instance]   (default mocha)
#   OUTAGE_MIN=12 RECOVER_MIN=6 STOP_MIN=3 to change the waits.
# Exit 0 when every check passes.
set -o errexit -o nounset -o pipefail
# shellcheck source=lib.sh
. "$(dirname "$0")/lib.sh"

INSTANCE="${1:-mocha}"
ENVFILE=/etc/fibre-observer/$INSTANCE.env
OUTAGE_MIN="${OUTAGE_MIN:-12}"
RECOVER_MIN="${RECOVER_MIN:-6}"
STOP_MIN="${STOP_MIN:-3}"
CHAIN_REASONS="chain_liveness,scanner,collector,heartbeat,prober"

[ -r "$ENVFILE" ] || { echo "no $ENVFILE" >&2; exit 2; }
DATA_DIR=$(envval "$ENVFILE" DATA_DIR); RPC=$(envval "$ENVFILE" RPC); API_LISTEN=$(envval "$ENVFILE" API_LISTEN)
DB="$DATA_DIR/observer.db"
HEALTH="http://$API_LISTEN/v1/health"
READERS="fibre-scan@$INSTANCE fibre-probe@$INSTANCE fibre-heartbeat@$INSTANCE fibre-collector@$INSTANCE"
ALL="$READERS fibre-api@$INSTANCE"
BAK="$ENVFILE.outage-bak"
MANIFEST_TOOL="$(dirname "$0")/../backup-manifest.py"
[ -x "$MANIFEST_TOOL" ] || MANIFEST_TOOL=/usr/local/bin/fibre-backup-manifest

restore_env() {
  if [ -f "$BAK" ]; then
    cp "$BAK" "$ENVFILE" && rm -f "$BAK"
    echo "$(ts) env restored (RPC=$(envval "$ENVFILE" RPC))"
  fi
}
cleanup() {
  trap - EXIT
  restore_env
  # shellcheck disable=SC2086
  systemctl start $ALL 2>/dev/null || true
  # shellcheck disable=SC2086
  systemctl restart $READERS 2>/dev/null || true
  rm -f "$H" "$H.net"
}
H=$(mktemp)
trap cleanup EXIT

scanned() { python3 -c 'import json,sys; s=json.load(open(sys.argv[1])); print(s.get("last_scanned_height",0), len(s.get("gaps") or []))' "$DATA_DIR/state.json" 2>/dev/null || echo "0 0"; }
counts() {
  python3 - "$DB" <<'PY'
import sqlite3, sys
con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
print(*con.execute("SELECT (SELECT COUNT(*) FROM publications), (SELECT COUNT(*) FROM probes), (SELECT COUNT(*) FROM reachability)").fetchone())
PY
}
tip() { curl -sS -m 15 "$RPC/status" 2>/dev/null | python3 -c 'import json,sys; print(int(json.load(sys.stdin)["result"]["sync_info"]["latest_block_height"]))' 2>/dev/null || echo 0; }
dupes() {
  # the whole record of each file, archived segments first (observer-archive)
  python3 - "$DATA_DIR" "$MANIFEST_TOOL" <<'PY'
import json, os, subprocess, sys
d = sys.argv[1]; out = []
for name, keyf in (("publications.jsonl", lambda r: r.get("promise_hash")),
                   ("measurements.jsonl", lambda r: (r.get("vantage"), r.get("promise_hash"), r.get("validator_address"), r.get("scheduled_at")))):
    path = os.path.join(d, name); seen = set(); dup = 0
    if os.path.exists(path):
        cat = subprocess.Popen([sys.argv[2], "cat", d, name], stdout=subprocess.PIPE)
        for line in cat.stdout:
            line = line.strip()
            if not line: continue
            try: k = keyf(json.loads(line))
            except Exception: continue
            if k in seen: dup += 1
            seen.add(k)
        if cat.wait() != 0:
            raise SystemExit(f"{name}: the manifest tool could not read the record")
    out.append(str(dup))
print(" ".join(out))
PY
}
monotonic() { python3 -c 'import sys; b=[int(x) for x in sys.argv[1].split()]; a=[int(x) for x in sys.argv[2].split()]; sys.exit(0 if all(x>=y for x,y in zip(a,b)) else 1)' "$1" "$2"; }
wait_for() { # wait_for <seconds> <description> <command...>
  local until=$(( $(date +%s) + $1 )); shift; local what=$1; shift
  while :; do
    if "$@"; then return 0; fi
    [ "$(date +%s)" -ge "$until" ] && return 1
    printf '  %s waiting: %s\n' "$(ts)" "$what"; sleep 30
  done
}
degraded_for_chain() { [ "$(http_code "$HEALTH" "$H")" = 503 ] && health_has_reason "$H" "$CHAIN_REASONS"; }
healthy() { [ "$(http_code "$HEALTH" "$H")" = 200 ]; }

code=$(http_code "$HEALTH" "$H")
[ "$code" = 200 ] || { echo "health is $code before the test starts ($(health_bad_checks "$H")); fix that first"; exit 2; }
read -r h0 gaps0 <<<"$(scanned)"; c0=$(counts)
echo "$(ts) start: health 200, scanned=$h0 gaps=$gaps0 counts=$c0 RPC=$RPC"

echo "== A. cut the chain source for up to $OUTAGE_MIN min"
cp "$ENVFILE" "$BAK"
sed -i 's|^RPC=.*|RPC=http://127.0.0.1:9|' "$ENVFILE"
# shellcheck disable=SC2086
systemctl restart $READERS
sleep 20
for u in $READERS; do
  st=$(systemctl is-active "$u" || true)
  if [ "$st" = "active" ] || [ "$st" = "activating" ]; then pass "$u $st on a dead RPC (Restart=always keeps trying)"; else fail "$u is $st"; fi
done
if wait_for $((OUTAGE_MIN * 60)) "health not yet 503 naming the chain source" degraded_for_chain; then
  pass "health 503 naming the chain source: $(health_bad_checks "$H")"
else
  code=$(http_code "$HEALTH" "$H")
  case "$code" in
    000) fail "health 000 after $OUTAGE_MIN min: the API went away during a chain-source outage; it must stay up and say degraded" ;;
    200) fail "health still 200 after $OUTAGE_MIN min without a chain source" ;;
    503) fail "health 503 but no failing check names the chain source ($CHAIN_REASONS): $(health_bad_checks "$H")" ;;
    *)   fail "health $code after $OUTAGE_MIN min" ;;
  esac
fi
code=$(HTTP_TIMEOUT=30 http_code "http://$API_LISTEN/v1/network?window=24h" "$H.net")
if [ "$code" = 200 ]; then
  pass "/v1/network still serves its last figures (computed_at $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("computed_at"))' "$H.net" 2>/dev/null))"
else
  fail "/v1/network -> $code during the outage"
fi

echo "== B. restore the chain source, recover within $RECOVER_MIN min"
restore_env
# shellcheck disable=SC2086
systemctl restart $READERS
if wait_for $((RECOVER_MIN * 60)) "health not 200 yet" healthy; then pass "health 200 again"; else fail "health $(http_code "$HEALTH" "$H") $RECOVER_MIN min after the source came back: $(health_bad_checks "$H")"; fi
caught_up() { read -r h _ <<<"$(scanned)"; t=$(tip); [ "$t" -gt 0 ] && [ $((t - h)) -le 20 ]; }
if wait_for $((RECOVER_MIN * 60)) "scanner catching up" caught_up; then
  read -r h1 gaps1 <<<"$(scanned)"; pass "scanner at $h1, tip $(tip): caught up"
else
  read -r h1 gaps1 <<<"$(scanned)"; fail "scanner at $h1, tip $(tip): not caught up"
fi
[ "$h1" -ge "$h0" ] && pass "checkpoint $h0 -> $h1" || fail "checkpoint went backwards $h0 -> $h1"
[ "$gaps1" = "$gaps0" ] && pass "no scan gap added ($gaps1)" || fail "scan gaps $gaps0 -> $gaps1: a transport outage must be retried, not recorded as a gap"
c1=$(counts); monotonic "$c0" "$c1" && pass "row counts $c0 -> $c1" || fail "row counts dropped $c0 -> $c1"
read -r dp dm <<<"$(dupes)"; [ "$dp" = 0 ] && [ "$dm" = 0 ] && pass "no duplicate line in the record" || fail "duplicates: publications=$dp measurements=$dm"

echo "== C. stop every process for $STOP_MIN min"
read -r h2 _ <<<"$(scanned)"; c2=$(counts)
# shellcheck disable=SC2086
systemctl stop $ALL
sleep $((STOP_MIN * 60))
code=$(http_code "$HEALTH" "$H")
[ "$code" = 000 ] && pass "API down: no HTTP answer (000); nothing pretends to be alive" || fail "health answered $code with every unit stopped"
# shellcheck disable=SC2086
systemctl start $ALL
if wait_for $((RECOVER_MIN * 60)) "health not 200 yet" healthy; then pass "health 200 after the restart"; else fail "health $(http_code "$HEALTH" "$H") $RECOVER_MIN min after the restart: $(health_bad_checks "$H")"; fi
read -r h3 _ <<<"$(scanned)"; c3=$(counts)
[ "$h3" -ge "$h2" ] && pass "checkpoint $h2 -> $h3" || fail "checkpoint went backwards $h2 -> $h3"
monotonic "$c2" "$c3" && pass "row counts $c2 -> $c3" || fail "row counts dropped $c2 -> $c3"
read -r dp dm <<<"$(dupes)"; [ "$dp" = 0 ] && [ "$dm" = 0 ] && pass "still no duplicate line in the record" || fail "duplicates: publications=$dp measurements=$dm"

echo
if [ "$FAILED" = 0 ]; then echo "outage: every check passed"; else echo "outage: FAILED"; exit 1; fi
