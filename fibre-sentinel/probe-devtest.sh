#!/usr/bin/env bash
#
# End-to-end check for the Sentinel probe scheduler + measurement store, with
# fault injection.
#
#   1. start multi-node-fibre.sh
#   2. run sentinel-scan (follow) so publications.jsonl fills
#   3. publish COUNT blobs
#   4. run sentinel-probe (--drain) against the same data dir
#   5. mid-window, kill ONE assigned validator's fibre server
#   6. let the prober drain its whole schedule (in-window + grace + post)
#   7. sentinel-measure-check: the killed validator shows an in_window FAULT,
#      live validators stay HEALTHY in window, post-window NOT_FOUND is
#      EXPECTED_GONE, grace NOT_FOUND is TOLERATED
#   8. tear down
#
# Runs ~16-18 min (retention floor is 10m + prune tolerance + post margin).
# Every wait has a timeout and dumps the relevant log tail on failure.
#
# Prereqs in PATH: celestia-appd, fibre, multi-node-fibre.sh, sentinel-scan,
#                  sentinel-pub, sentinel-probe, sentinel-measure-check
#
# Usage: ./probe-devtest.sh [N] [COUNT]

set -o errexit
set -o nounset
set -o pipefail

N="${1:-4}"
COUNT="${2:-3}"
RUN="${SENTINEL_PROBE_DEVTEST_HOME:-${TMPDIR:-/tmp}/sentinel-probe-devtest}"
DEVNET_HOME="${RUN}/devnet"
DATA_DIR="${RUN}/sentinel-data"
LOGDIR="${RUN}/logs"
RPC="http://127.0.0.1:26657"
GRPC="127.0.0.1:9090"
CHAIN_ID="fibre-devnet"

KILL_PORT=7981            # node 1's fibre server
KILL_HOST="127.0.0.1:${KILL_PORT}"
KILL_DELAY=180            # seconds after first publish before the kill

taskkill //F //IM celestia-appd.exe 2>/dev/null || true
taskkill //F //IM fibre.exe 2>/dev/null || true
taskkill //F //IM sentinel-scan.exe 2>/dev/null || true
taskkill //F //IM sentinel-probe.exe 2>/dev/null || true
sleep 2
rm -rf "$RUN"
mkdir -p "$LOGDIR"

DEVNET_PID="" ; SCAN_PID="" ; PROBE_PID=""
cleanup() {
  trap - INT TERM EXIT
  echo "--> cleanup"
  [ -n "$PROBE_PID" ]  && kill "$PROBE_PID"  2>/dev/null || true
  [ -n "$SCAN_PID" ]   && kill "$SCAN_PID"   2>/dev/null || true
  [ -n "$DEVNET_PID" ] && kill "$DEVNET_PID" 2>/dev/null || true
  sleep 3
  taskkill //F //IM celestia-appd.exe 2>/dev/null || true
  taskkill //F //IM fibre.exe 2>/dev/null || true
  taskkill //F //IM sentinel-scan.exe 2>/dev/null || true
  taskkill //F //IM sentinel-probe.exe 2>/dev/null || true
  echo "--> done. logs + data under ${RUN}"
}
trap cleanup INT TERM EXIT

fail() {
  echo "!! FAIL: $*" >&2
  for f in "$LOGDIR"/*.log; do
    [ -f "$f" ] || continue
    echo "---- tail $f ----" >&2
    tail -n 30 "$f" >&2
  done
  exit 1
}

pid_on_port() {
  netstat -ano -p tcp 2>/dev/null | grep -E ":$1 .*LISTENING" | awk '{print $NF}' | head -1
}

# ---- 1. devnet ----
echo "--> starting devnet (N=$N)"
FIBRE_DEVNET_HOME="$DEVNET_HOME" multi-node-fibre.sh "$N" > "$LOGDIR/devnet.log" 2>&1 &
DEVNET_PID=$!
echo -n "--> waiting for devnet READY "
for _ in $(seq 1 210); do
  [ -f "$DEVNET_HOME/READY" ] && { echo "ok"; break; }
  kill -0 "$DEVNET_PID" 2>/dev/null || fail "devnet exited before READY"
  echo -n "." ; sleep 2
done
[ -f "$DEVNET_HOME/READY" ] || fail "devnet not READY within 7m"
sleep 3

# ---- 2. scanner ----
echo "--> starting sentinel-scan (follow)"
sentinel-scan -rpc "$RPC" -data-dir "$DATA_DIR" -start-height 1 -follow \
  -follow-timeout 120s -checkpoint-every 3 > "$LOGDIR/scan.log" 2>&1 &
SCAN_PID=$!
sleep 3
kill -0 "$SCAN_PID" 2>/dev/null || fail "sentinel-scan exited immediately"

# ---- 3. publish ----
echo "--> publishing $COUNT blobs"
FIBRE_DEVNET_HOME="$DEVNET_HOME" sentinel-pub -rpc "$RPC" -grpc "$GRPC" \
  -chain-id "$CHAIN_ID" -count "$COUNT" -blob 98304 -gap 4s > "$LOGDIR/pub.log" 2>&1 \
  || fail "sentinel-pub failed"
FIRST_PUB_EPOCH=$(date +%s)
COMMITS="$(grep -oE 'commitment=[0-9a-f]{64}' "$LOGDIR/pub.log" | sed 's/commitment=//' | paste -sd, -)"
echo "--> published: $COMMITS"

echo -n "--> waiting for scanner to record $COUNT "
for _ in $(seq 1 40); do
  n="$(grep -c . "$DATA_DIR/publications.jsonl" 2>/dev/null || echo 0)"
  [ "${n:-0}" -ge "$COUNT" ] && { echo " ok ($n)"; break; }
  echo -n "." ; sleep 3
done
[ "${n:-0}" -ge "$COUNT" ] || fail "scanner recorded only ${n:-0}/$COUNT"

# ---- 4. prober (drain) ----
echo "--> starting sentinel-probe (--drain)"
# grace-offset 120s puts the grace probe AFTER the ~90s prune lag, so this run
# actually exercises the TOLERATED class (NOT_FOUND in the grace window), not
# just FAULT / HEALTHY / EXPECTED_GONE.
sentinel-probe -rpc "$RPC" -data-dir "$DATA_DIR" -vantage devtest --drain \
  -deadline 22m -in-window-probes 3 -grace-offset 120s -prune-tolerance 210s -post-margin 45s \
  -max-sleep 15s -max-lateness 60s > "$LOGDIR/probe.log" 2>&1 &
PROBE_PID=$!
sleep 3
kill -0 "$PROBE_PID" 2>/dev/null || fail "sentinel-probe exited immediately"

# ---- 5. fault injection ----
WAIT_KILL=$(( FIRST_PUB_EPOCH + KILL_DELAY - $(date +%s) ))
[ "$WAIT_KILL" -gt 0 ] && { echo "--> waiting ${WAIT_KILL}s before killing fibre on :${KILL_PORT}"; sleep "$WAIT_KILL"; }
KPID="$(pid_on_port "$KILL_PORT")"
[ -n "$KPID" ] || fail "no listener on :${KILL_PORT}"
KILL_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
taskkill //F //PID "$KPID" >/dev/null 2>&1 || kill -9 "$KPID" 2>/dev/null || true
echo "--> >>> FAULT: killed fibre :${KILL_PORT} (pid ${KPID}) at ${KILL_AT}"

# ---- 6. let the prober drain ----
echo -n "--> waiting for sentinel-probe to drain (up to ~21m) "
DRAINED=0
for _ in $(seq 1 260); do   # 260 * 5s = ~21m
  if ! kill -0 "$PROBE_PID" 2>/dev/null; then echo " exited"; PROBE_PID=""; break; fi
  echo -n "." ; sleep 5
done
[ -z "$PROBE_PID" ] || fail "sentinel-probe did not exit within ~21m"
grep -q 'done (--drain)' "$LOGDIR/probe.log" || fail "sentinel-probe exited without draining cleanly"

# ---- 7. verify ----
kill "$SCAN_PID" 2>/dev/null || true ; SCAN_PID=""
echo "--> sentinel-measure-check"
sentinel-measure-check -data-dir "$DATA_DIR" -killed-host "$KILL_HOST" -kill-at "$KILL_AT" -min-probes 8 \
  || fail "sentinel-measure-check reported problems"

echo ""
echo "=================================================="
echo "  PASS: probe schedule + measurements + taxonomy"
echo "        fault injection detected, live validators healthy"
echo "=================================================="
