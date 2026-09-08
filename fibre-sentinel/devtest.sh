#!/usr/bin/env bash
#
# End-to-end check for the Sentinel chain scanner against a fresh Fibre devnet.
#
#   1. start multi-node-fibre.sh (N validators + a fibre server each)
#   2. start sentinel-scan in follow mode from height 1
#   3. publish COUNT blobs with sentinel-pub
#   4. wait (bounded) for the scanner to record all COUNT
#   5. sentinel-verify: every published commitment is present, with the right
#      must_serve_until (creation + max(promise_timeout, shard_retention)) and a
#      non-degenerate assignment table
#   6. tear everything down
#
# Every wait has a timeout and prints the tail of the relevant log on failure —
# nothing here can hang.
#
# Prereqs in PATH: celestia-appd, fibre, multi-node-fibre.sh, sentinel-scan,
#                  sentinel-pub, sentinel-verify
#
# Usage: ./devtest.sh [N] [COUNT]

set -o errexit
set -o nounset
set -o pipefail

N="${1:-4}"
COUNT="${2:-4}"
RUN="${SENTINEL_DEVTEST_HOME:-${TMPDIR:-/tmp}/sentinel-devtest}"
DEVNET_HOME="${RUN}/devnet"
DATA_DIR="${RUN}/sentinel-data"
LOGDIR="${RUN}/logs"
RPC="http://127.0.0.1:26657"
GRPC="127.0.0.1:9090"
CHAIN_ID="fibre-devnet"

# devnet genesis values from multi-node-fibre.sh
PROMISE_TIMEOUT="600s"
SHARD_RETENTION="600s"

taskkill //F //IM celestia-appd.exe 2>/dev/null || true
taskkill //F //IM fibre.exe 2>/dev/null || true
taskkill //F //IM sentinel-scan.exe 2>/dev/null || true
sleep 2
rm -rf "$RUN"
mkdir -p "$LOGDIR"

DEVNET_PID=""
SCAN_PID=""
cleanup() {
  trap - INT TERM EXIT
  echo "--> cleanup"
  [ -n "$SCAN_PID" ] && kill "$SCAN_PID" 2>/dev/null || true
  [ -n "$DEVNET_PID" ] && kill "$DEVNET_PID" 2>/dev/null || true
  # multi-node-fibre.sh traps TERM and stops its children; give it a moment.
  sleep 3
  taskkill //F //IM celestia-appd.exe 2>/dev/null || true
  taskkill //F //IM fibre.exe 2>/dev/null || true
  taskkill //F //IM sentinel-scan.exe 2>/dev/null || true
  echo "--> done. logs + data under ${RUN}"
}
trap cleanup INT TERM EXIT

fail() {
  echo "!! FAIL: $*" >&2
  for f in "$LOGDIR"/*.log; do
    [ -f "$f" ] || continue
    echo "---- tail $f ----" >&2
    tail -n 25 "$f" >&2
  done
  exit 1
}

# ---- 1. devnet ----
echo "--> starting devnet (N=$N)"
FIBRE_DEVNET_HOME="$DEVNET_HOME" multi-node-fibre.sh "$N" > "$LOGDIR/devnet.log" 2>&1 &
DEVNET_PID=$!

echo -n "--> waiting for devnet READY "
for _ in $(seq 1 210); do   # 210 * 2s = 7 min cap (genesis of N nodes is slow on Windows)
  [ -f "$DEVNET_HOME/READY" ] && { echo "ok"; break; }
  kill -0 "$DEVNET_PID" 2>/dev/null || fail "devnet process exited before READY"
  echo -n "."
  sleep 2
done
[ -f "$DEVNET_HOME/READY" ] || fail "devnet did not become READY within 7m"
sleep 3

# ---- 2. scanner (follow) ----
echo "--> starting sentinel-scan (follow, from height 1)"
sentinel-scan -rpc "$RPC" -data-dir "$DATA_DIR" -start-height 1 -follow \
  -follow-timeout 90s -poll 2s -rpc-timeout 15s -checkpoint-every 5 \
  > "$LOGDIR/scan.log" 2>&1 &
SCAN_PID=$!
sleep 3
kill -0 "$SCAN_PID" 2>/dev/null || fail "sentinel-scan exited immediately (see scan.log)"

# ---- 3. publish ----
echo "--> publishing $COUNT blobs"
FIBRE_DEVNET_HOME="$DEVNET_HOME" sentinel-pub -rpc "$RPC" -grpc "$GRPC" \
  -chain-id "$CHAIN_ID" -count "$COUNT" -blob 98304 -gap 5s \
  > "$LOGDIR/pub.log" 2>&1 || fail "sentinel-pub failed"

COMMITS="$(grep -oE 'commitment=[0-9a-f]{64}' "$LOGDIR/pub.log" | sed 's/commitment=//' | paste -sd, -)"
[ -n "$COMMITS" ] || fail "no commitments parsed from pub.log"
echo "--> published commitments: $COMMITS"

# ---- 4. wait for the scanner to record them ----
echo -n "--> waiting for scanner to record $COUNT publications "
for _ in $(seq 1 60); do   # 60 * 3s = 3 min cap
  n=0
  [ -f "$DATA_DIR/publications.jsonl" ] && n="$(grep -c . "$DATA_DIR/publications.jsonl" || true)"
  if [ "${n:-0}" -ge "$COUNT" ]; then echo " ok ($n)"; break; fi
  kill -0 "$SCAN_PID" 2>/dev/null || fail "sentinel-scan died mid-run (see scan.log)"
  echo -n "."
  sleep 3
done
n="$(grep -c . "$DATA_DIR/publications.jsonl" 2>/dev/null || echo 0)"
[ "${n:-0}" -ge "$COUNT" ] || fail "scanner recorded only ${n:-0}/$COUNT within 3m"

# ---- 5. verify ----
echo "--> stopping scanner, running sentinel-verify"
kill "$SCAN_PID" 2>/dev/null || true
SCAN_PID=""
sleep 2

sentinel-verify -data-dir "$DATA_DIR" -expect-commitments "$COMMITS" \
  -promise-timeout "$PROMISE_TIMEOUT" -shard-retention "$SHARD_RETENTION" \
  -min-validators-with-rows 2 || fail "sentinel-verify reported problems"

echo ""
echo "=========================================="
echo "  PASS: scanner captured all $COUNT publications"
echo "        with correct assignment + must_serve_until"
echo "=========================================="
