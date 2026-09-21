#!/usr/bin/env bash
#
# rpc-check: is this RPC node enough for the observer?
#
# The scanner, collector, prober and heartbeat read one CometBFT RPC. Every
# figure on the site is downstream of it, so before the observer is trusted
# the node behind it has to be: on the right chain, in sync, and keeping
# enough history for what the observer asks of it. What it asks:
#
#   block, block_results  at every height it scans (block_results needs
#                         storage.discard_abci_responses = false on the node)
#   validators?height=    at the PROMISE height, up to PaymentPromiseHeightWindow
#                         (1000) blocks below the settlement it is scanning
#   abci_query?height=    x/fibre params at every height of a params-uncertainty
#                         range, up to 5000 heights back (scan.maxVerifyHeights)
#
# A node pruned tighter than that records scan gaps and holds verdicts, and
# the site says so, but the operator should know before, not after. A second
# RPC, when given, cross-checks the block hash at one height: two independent
# nodes agreeing on a hash is the only sync check that does not trust the
# node it is checking.
#
# Usage: deploy/test/rpc-check.sh <rpc-url> [second-rpc-url]
#   RPC_CHAIN_ID  the chain expected (default mocha-5)
#   RPC_LOOKBACK  how far back history must reach, in blocks (default 6000 =
#                 1000 promise window + 5000 uncertainty verify)
#
# Exit 0 when every check passes, 1 otherwise. Needs curl and python3.
set -o errexit -o nounset -o pipefail

RPC="${1:?rpc url}"
RPC2="${2:-}"
WANT_CHAIN="${RPC_CHAIN_ID:-mocha-5}"
LOOKBACK="${RPC_LOOKBACK:-6000}"
FAILED=0

pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; FAILED=1; }
warn() { echo "  warn $*"; }

# get <url> -> body on stdout; empty on transport failure.
get() { curl -sS -m 25 "$1" 2>/dev/null || true; }

# field <json> <python-expr over d> -> value, or "" when absent.
field() {
  printf '%s' "$1" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
try:
    print(eval(sys.argv[1]))
except Exception:
    pass
' "$2"
}

echo "== $RPC"
status=$(get "$RPC/status")
[ -n "$status" ] || { fail "no answer from $RPC/status"; exit 1; }
chain=$(field "$status" 'd["result"]["node_info"]["network"]')
tip=$(field "$status" 'int(d["result"]["sync_info"]["latest_block_height"])')
tip_time=$(field "$status" 'd["result"]["sync_info"]["latest_block_time"]')
earliest=$(field "$status" 'int(d["result"]["sync_info"]["earliest_block_height"])')
catching=$(field "$status" 'd["result"]["sync_info"]["catching_up"]')
moniker=$(field "$status" 'd["result"]["node_info"]["moniker"]')
version=$(field "$status" 'd["result"]["node_info"]["version"]')
echo "  node $moniker cometbft $version chain $chain tip $tip ($tip_time) earliest $earliest"

echo "== chain identity and sync"
if [ "$chain" = "$WANT_CHAIN" ]; then pass "chain id $chain"; else fail "chain id is $chain, want $WANT_CHAIN"; fi
if [ "$catching" = "False" ]; then pass "not catching up"; else fail "catching_up=$catching"; fi
age=$(python3 -c '
import sys, datetime
t = sys.argv[1].rstrip("Z")
t = t[:26] if "." in t else t
dt = datetime.datetime.fromisoformat(t).replace(tzinfo=datetime.timezone.utc)
print(int((datetime.datetime.now(datetime.timezone.utc) - dt).total_seconds()))
' "$tip_time")
if [ "$age" -le 120 ]; then pass "newest block ${age}s old"; else fail "newest block ${age}s old: the node is behind the chain, or its clock is"; fi
abci=$(get "$RPC/abci_info")
appv=$(field "$abci" 'd["result"]["response"]["app_version"]')
[ -n "$appv" ] && pass "app version $appv (x/fibre exists from 10)" || fail "abci_info gave no app_version"

echo "== history the observer needs (lookback $LOOKBACK blocks)"
need=$((tip - LOOKBACK))
[ "$need" -lt 1 ] && need=1
if [ "$earliest" -le "$need" ]; then
  pass "earliest_block_height $earliest <= $need"
else
  fail "earliest_block_height $earliest > $need: pruned tighter than the observer's lookback"
fi
# block and block_results at the far end of the lookback
h=$need
blk=$(get "$RPC/block?height=$h")
bh=$(field "$blk" 'd["result"]["block_id"]["hash"]')
if [ -n "$bh" ]; then pass "block $h readable ($bh)"; elif [ -z "$blk" ]; then fail "block $h: no answer (timeout, reset, or too large for 25 s)"; else fail "block $h: $(field "$blk" 'd.get("error",{}).get("data") or d.get("error")')"; fi
res=$(get "$RPC/block_results?height=$h")
rh=$(field "$res" 'd["result"]["height"]')
if [ -n "$rh" ]; then
  pass "block_results $h readable"
elif [ -z "$res" ]; then
  fail "block_results $h: no answer (timeout or reset)"
else
  fail "block_results $h: $(field "$res" 'd.get("error",{}).get("data") or d.get("error")') — the scanner reads results at every height; the node must keep them (storage.discard_abci_responses = false)"
fi
# validator set at a past height, paged the way the scanner pages it
vals=$(get "$RPC/validators?height=$h&page=1&per_page=100")
vt=$(field "$vals" 'int(d["result"]["total"])')
vn=$(field "$vals" 'len(d["result"]["validators"])')
if [ -n "$vt" ]; then
  pass "validators at $h: $vn of $vt on page 1"
else
  fail "validators at $h: $(field "$vals" 'd.get("error",{}).get("data") or d.get("error")') — the scanner fetches the set at the promise height"
fi
# historical state: a query the module answers on every app version
sp=$(get "$RPC/abci_query?path=%22/cosmos.staking.v1beta1.Query/Params%22&height=$h")
code=$(field "$sp" 'd["result"]["response"]["code"]')
if [ "$code" = "0" ]; then
  pass "abci_query at height $h answers (historical state kept)"
else
  fail "abci_query at height $h: code=$code log=$(field "$sp" 'd["result"]["response"]["log"]') — params reconcile reads state at past heights"
fi
# x/fibre itself: unknown path before activation is the expected answer
fp=$(get "$RPC/abci_query?path=%22/celestia.fibre.v1.Query/Params%22&height=$tip")
fcode=$(field "$fp" 'd["result"]["response"]["code"]')
flog=$(field "$fp" 'd["result"]["response"]["log"]')
case "$fcode" in
  0) pass "x/fibre params answer at $tip: Fibre is active" ;;
  6) pass "x/fibre not active yet at $tip (code 6: $flog); expected before app version 10" ;;
  *) fail "x/fibre params at $tip: code=$fcode log=$flog" ;;
esac

if [ -n "$RPC2" ]; then
  echo "== cross-check against $RPC2"
  s2=$(get "$RPC2/status")
  c2=$(field "$s2" 'd["result"]["node_info"]["network"]')
  t2=$(field "$s2" 'int(d["result"]["sync_info"]["latest_block_height"])')
  if [ -z "$t2" ]; then
    warn "no answer from $RPC2; hash cross-check skipped"
  else
    [ "$c2" = "$chain" ] && pass "same chain id" || fail "second node is on $c2"
    # a height both certainly have and that is final on both
    ch=$(( (tip < t2 ? tip : t2) - 20 ))
    h1=$(field "$(get "$RPC/block?height=$ch")" 'd["result"]["block_id"]["hash"]')
    h2=$(field "$(get "$RPC2/block?height=$ch")" 'd["result"]["block_id"]["hash"]')
    if [ -n "$h1" ] && [ "$h1" = "$h2" ]; then
      pass "block $ch hash agrees: $h1"
    else
      fail "block $ch hash differs: $h1 vs $h2"
    fi
    d=$(( tip > t2 ? tip - t2 : t2 - tip ))
    [ "$d" -le 10 ] && pass "tips within $d blocks" || warn "tips $d blocks apart"
  fi
else
  warn "no second RPC given: the hash cross-check is the one sync check that does not trust the node; pass one as the second argument"
fi

echo
if [ "$FAILED" = 0 ]; then echo "rpc-check: every check passed for $RPC"; else echo "rpc-check: FAILED for $RPC"; exit 1; fi
