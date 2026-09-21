#!/usr/bin/env bash
#
# outage: cut the observer off on purpose, watch the site admit it, put it
# back, and check that nothing was lost or doubled.
#
#   A. chain source cut. The four units that read the RPC are pointed at a
#      dead port and restarted. Within OUTAGE_MIN minutes /v1/health must
#      stop saying ok, and say why: chain_liveness (no new block seen for
#      ten minutes) or a component that is alive but failing. The API keeps
#      serving its last figures throughout, which is the design, and the
#      dashboard says so in the header chip and the banner.
#   B. source restored. Within RECOVER_MIN minutes: health ok, the scanner
#      within a few blocks of the chain tip, no scan gap added (a transport
#      failure is retried, never recorded as a gap the node could not
#      serve), every row count at or above what it was, and no duplicate
#      line in the record.
#   C. every process stopped for STOP_MIN minutes, then started. The API is
#      down too (000), then ok again within RECOVER_MIN, with the scanner
#      checkpoint at or past where it was.
#
# The env file is edited for phase A and restored on every exit path,
# including a failed check, so a crashed run does not leave the box on a
# dead port. Total runtime about OUTAGE_MIN + 2*RECOVER_MIN + STOP_MIN.
#
# Usage: sudo deploy/test/outage.sh [instance]   (default mocha)
#   OUTAGE_MIN=12 RECOVER_MIN=6 STOP_MIN=3 to change the waits.
# Exit 0 when every check passes.
set -o errexit -o nounset -o pipefail

INSTANCE="${1:-mocha}"
ENVFILE=/etc/fibre-observer/$INSTANCE.env
OUTAGE_MIN="${OUTAGE_MIN:-12}"
RECOVER_MIN="${RECOVER_MIN:-6}"
STOP_MIN="${STOP_MIN:-3}"
FAILED=0
pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; FAILED=1; }
warn() { echo "  warn $*"; }
ts() { date -u +%H:%M:%S; }

[ -r "$ENVFILE" ] || { echo "no $ENVFILE" >&2; exit 2; }
envval() { sed -n "s/^$1=//p" "$ENVFILE" | head -1; }
DATA_DIR=$(envval DATA_DIR); RPC=$(envval RPC); API_LISTEN=$(envval API_LISTEN)
DB="$DATA_DIR/observer.db"
READERS="fibre-scan@$INSTANCE fibre-probe@$INSTANCE fibre-heartbeat@$INSTANCE fibre-collector@$INSTANCE"
ALL="$READERS fibre-api@$INSTANCE"
BAK="$ENVFILE.outage-bak"

restore_env() {
  if [ -f "$BAK" ]; then
    cp "$BAK" "$ENVFILE" && rm -f "$BAK"
    echo "$(ts) env restored (RPC=$(envval RPC))"
  fi
}
cleanup() {
  trap - EXIT
  restore_env
  # shellcheck disable=SC2086
  systemctl start $ALL 2>/dev/null || true
  # shellcheck disable=SC2086
  systemctl restart $READERS 2>/dev/null || true
}
trap cleanup EXIT

health() { curl -sS -m 10 -o "$1" -w '%{http_code}' "http://$API_LISTEN/v1/health" 2>/dev/null || echo 000; }
hfield() { python3 -c 'import json,sys; h=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$1" "$2" 2>/dev/null || true; }
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
  python3 - "$DATA_DIR" <<'PY'
import json, os, sys
d = sys.argv[1]; out = []
for name, keyf in (("publications.jsonl", lambda r: r.get("promise_hash")),
                   ("measurements.jsonl", lambda r: (r.get("vantage"), r.get("promise_hash"), r.get("validator_address"), r.get("scheduled_at")))):
    path = os.path.join(d, name); seen = set(); dup = 0
    if os.path.exists(path):
        for line in open(path, "rb"):
            line = line.strip()
            if not line: continue
            try: k = keyf(json.loads(line))
            except Exception: continue
            if k in seen: dup += 1
            seen.add(k)
    out.append(str(dup))
print(" ".join(out))
PY
}
monotonic() { python3 -c 'import sys; b=[int(x) for x in sys.argv[1].split()]; a=[int(x) for x in sys.argv[2].split()]; sys.exit(0 if all(x>=y for x,y in zip(a,b)) else 1)' "$1" "$2"; }
wait_for() { # wait_for <seconds> <description> <command...>; returns the command's last status
  local until=$(( $(date +%s) + $1 )); shift; local what=$1; shift
  while :; do
    if "$@"; then return 0; fi
    [ "$(date +%s)" -ge "$until" ] && return 1
    printf '  %s waiting: %s\n' "$(ts)" "$what"; sleep 30
  done
}

H=$(mktemp)
code=$(health "$H")
[ "$code" = 200 ] || { echo "health is $code before the test starts; fix that first"; exit 2; }
read -r h0 gaps0 <<<"$(scanned)"; c0=$(counts)
echo "$(ts) start: health ok, scanned=$h0 gaps=$gaps0 counts=$c0 RPC=$RPC"

echo "== A. cut the chain source for up to $OUTAGE_MIN min"
cp "$ENVFILE" "$BAK"
sed -i 's|^RPC=.*|RPC=http://127.0.0.1:9|' "$ENVFILE"
# shellcheck disable=SC2086
systemctl restart $READERS
sleep 20
# shellcheck disable=SC2086
for u in $READERS; do
  st=$(systemctl is-active "$u" || true)
  [ "$st" = "active" ] || [ "$st" = "activating" ] && pass "$u $st on a dead RPC (Restart=always keeps trying)" || fail "$u is $st"
done
degraded() { c=$(health "$H"); [ "$c" != 200 ] && [ "$c" != 000 ]; }
if wait_for $((OUTAGE_MIN * 60)) "health still ok" degraded; then
  st=$(hfield "$H" 'h["status"]'); bad=$(hfield "$H" '"; ".join(c["name"]+": "+c["detail"] for c in h["checks"] if not c["ok"])')
  pass "health $st after the cut: $bad"
  case "$bad" in *chain_liveness*|*collector*|*scanner*) pass "the reason names the chain source" ;; *) warn "degraded, but not for the chain source: $bad" ;; esac
else
  fail "health still $(health "$H") after $OUTAGE_MIN min without a chain source"
fi
code=$(curl -sS -m 30 -o "$H.net" -w '%{http_code}' "http://$API_LISTEN/v1/network?window=24h" 2>/dev/null || echo 000)
[ "$code" = 200 ] && pass "/v1/network still serves its last figures (computed_at $(hfield "$H.net" 'h.get("computed_at")'))" || fail "/v1/network -> $code during the outage"

echo "== B. restore the chain source, recover within $RECOVER_MIN min"
restore_env
# shellcheck disable=SC2086
systemctl restart $READERS
ok() { [ "$(health "$H")" = 200 ]; }
if wait_for $((RECOVER_MIN * 60)) "health not ok yet" ok; then pass "health ok again"; else fail "health still not ok $RECOVER_MIN min after the source came back: $(hfield "$H" '"; ".join(c["name"]+": "+c["detail"] for c in h["checks"] if not c["ok"])')"; fi
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
[ "$(health "$H")" = 000 ] && pass "API down: health 000 (nothing pretends to be alive)" || fail "health answered $(health "$H") with every unit stopped"
# shellcheck disable=SC2086
systemctl start $ALL
if wait_for $((RECOVER_MIN * 60)) "health not ok yet" ok; then pass "health ok after the restart"; else fail "health not ok $RECOVER_MIN min after the restart: $(hfield "$H" '"; ".join(c["name"]+": "+c["detail"] for c in h["checks"] if not c["ok"])')"; fi
read -r h3 _ <<<"$(scanned)"; c3=$(counts)
[ "$h3" -ge "$h2" ] && pass "checkpoint $h2 -> $h3" || fail "checkpoint went backwards $h2 -> $h3"
monotonic "$c2" "$c3" && pass "row counts $c2 -> $c3" || fail "row counts dropped $c2 -> $c3"
read -r dp dm <<<"$(dupes)"; [ "$dp" = 0 ] && [ "$dm" = 0 ] && pass "still no duplicate line in the record" || fail "duplicates: publications=$dp measurements=$dm"
rm -f "$H" "$H.net"

echo
if [ "$FAILED" = 0 ]; then echo "outage: every check passed"; else echo "outage: FAILED"; exit 1; fi
