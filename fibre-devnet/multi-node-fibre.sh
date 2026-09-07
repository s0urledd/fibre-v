#!/usr/bin/env bash
#
# Multi-validator local Fibre devnet.
#
# Brings up N celestia-appd validators on ONE host and a `fibre` server per
# validator, so shard assignment is non-degenerate (the single-node script
# gives one validator all 4096 original rows).
#
# Stake is near-equal: each validator gets a full proportional share of rows
# (well above the minRows floor, below the originalRows cap), and any single
# validator holds < K rows, so a blob stays reconstructable after one fibre
# server is killed. All N nodes stay up for consensus (never killed); only
# fibre servers are stopped during fault-injection testing. Validator 0 has a
# hair more stake so it is the stable proposer.
#
# Fibre params are lowered to the protocol floor (payment_promise_timeout and
# shard_retention = 10m each; withdrawal_delay cannot go below ~12h10m) so a
# shard's pruneAt is creation + 10m and retention behaviour is observable in
# ~10-12 minutes. This mirrors the setFibreShortLifetimes genesis modifier in
# fibre/internal/e2e/fibre_stack_test.go.
#
# Prerequisites in PATH:
#   celestia-appd   -- build with `make build-standalone` (v10-only: genesis
#                      starts at app version 10, so x/fibre and x/valaddr are
#                      live from block 1 with no upgrade)
#   fibre           -- build with `make build-fibre-server` (-> build/fibre)
#   curl            -- (jq is used for prettier output if present, not required)
#
# Usage:   ./multi-node-fibre.sh [N]        (default N=4, min 2)
# Stop:    Ctrl-C  (cleans up all processes)

set -o errexit
set -o nounset
set -o pipefail

# ----------------------------------------------------------------------------
# Config
# ----------------------------------------------------------------------------
N="${1:-4}"
if [ "$N" -lt 2 ]; then echo "N must be >= 2" >&2; exit 1; fi

CHAIN_ID="fibre-devnet"
KEYRING="test"
FEES="6000utia"
WORKDIR="${FIBRE_DEVNET_HOME:-${HOME}/.fibre-devnet}"
LOGDIR="${WORKDIR}/logs"

# Near-equal stake. Voting power = tokens / 1e6. With 4 validators each ~25%
# stake, the row-count formula ceil(4096 * stake% * 3) gives each ~3000 rows
# (> minRows 148, < originalRows 4096), Σ ~12k < 16384 (no wrap), all disjoint.
STAKE_NODE0="1050000000000utia"   # 1.05e12 -> 1.05e6 power (stable proposer)
STAKE_OTHER="1000000000000utia"   # 1.00e12 -> 1.00e6 power
GENESIS_BALANCE="10000000000000utia"   # per account, > its stake + fee
ESCROW_DEPOSIT="2000000000utia"

# Fibre params (nanoseconds not accepted; Duration JSON is "<seconds>s").
FIBRE_PROMISE_TIMEOUT="600s"   # MinPaymentPromiseTimeout
FIBRE_SHARD_RETENTION="600s"   # MinShardRetention
# withdrawal_delay: MinWithdrawalDelay = MaxPaymentPromiseTimeout(12h)+10m.
FIBRE_WITHDRAWAL_DELAY="43800s"

# Per-node port base. node i:
#   RPC        26657 + i*100
#   P2P        26656 + i*100
#   gRPC        9090 + i*10
#   API         1317 + i*10
#   privval    26669 + i*100     (celestia-appd PrivValidatorAPI; fibre signs here)
#   fibre       7980 + i
rpc_port()     { echo $((26657 + $1 * 100)); }
p2p_port()     { echo $((26656 + $1 * 100)); }
grpc_port()    { echo $((9090  + $1 * 10)); }
api_port()     { echo $((1317  + $1 * 10)); }
privval_port() { echo $((26669 + $1 * 100)); }
fibre_port()   { echo $((7980  + $1)); }

APP_HOME()   { echo "${WORKDIR}/app${1}"; }
FIBRE_HOME() { echo "${WORKDIR}/fibre${1}"; }

APP_PIDS=()
FIBRE_PIDS=()

# ----------------------------------------------------------------------------
# Lifecycle
# ----------------------------------------------------------------------------
CLEANED=0
cleanup() {
  [ "$CLEANED" = 1 ] && return; CLEANED=1
  trap - INT TERM EXIT
  echo ""
  echo "--> stopping fibre servers"
  for pid in "${FIBRE_PIDS[@]:-}"; do [ -n "${pid:-}" ] && kill "$pid" 2>/dev/null || true; done
  echo "--> stopping app nodes"
  for pid in "${APP_PIDS[@]:-}"; do [ -n "${pid:-}" ] && kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  echo "--> stopped. homes + logs left under ${WORKDIR}"
}
trap cleanup INT TERM EXIT

HAVE_JQ=0
preflight() {
  for bin in celestia-appd fibre curl; do
    command -v "$bin" >/dev/null 2>&1 || { echo "missing: $bin" >&2; exit 1; }
  done
  command -v jq >/dev/null 2>&1 && HAVE_JQ=1
  echo "--> celestia-appd: $(celestia-appd version 2>&1 | head -1 || true)"
  echo "--> fibre:         $(fibre version 2>&1 | head -1 || true)"
  echo "--> jq:            $([ "$HAVE_JQ" = 1 ] && echo yes || echo 'no (plain output)')"
}

# height_of <rpc_port> -- latest block height from /status, jq-free.
# Never fails (pipefail-safe): prints the height or nothing.
height_of() {
  { curl -s "http://127.0.0.1:${1}/status" 2>/dev/null || true; } \
    | grep -oE '"latest_block_height": *"?[0-9]+' | grep -oE '[0-9]+$' | head -1 || true
}

# toml_set <file> <key> <value> [after_key]
#   value is written verbatim -- caller quotes strings.
#   Replaces `key = ...` if present (in ANY section -- fine for the unique keys
#   used here), else uncomments `# key = ...`, else inserts `key = value` after
#   the `after_key` line (default `moniker`, a top-level BaseConfig field that
#   `init` always writes) so top-level keys land before the first [section].
toml_set() {
  local file="$1" key="$2" val="$3" after="${4:-moniker}"
  if grep -qE "^[[:space:]]*${key}[[:space:]]*=" "$file"; then
    sed -i.bak -E "s#^([[:space:]]*${key}[[:space:]]*=).*#\1 ${val}#" "$file"
  elif grep -qE "^[[:space:]]*#[[:space:]]*${key}[[:space:]]*=" "$file"; then
    sed -i.bak -E "s#^[[:space:]]*#[[:space:]]*(${key}[[:space:]]*=).*#\1 ${val}#" "$file"
  else
    sed -i.bak -E "0,/^[[:space:]]*${after}[[:space:]]*=/ s##&\n${key} = ${val}#" "$file"
  fi
  rm -f "${file}.bak"
}

# toml_set_section <file> <section> <key> <value>  -- scoped to one [section].
toml_set_section() {
  local file="$1" section="$2" key="$3" val="$4"
  sed -i.bak -E "/^\[${section}\]/,/^\[/ { s#^([[:space:]]*${key}[[:space:]]*=).*#\1 ${val}# }" "$file"
  rm -f "${file}.bak"
}

wait_for_height() {
  local target="$1" rpc="$2" h
  echo -n "--> waiting for height ${target} on :${rpc} "
  for _ in $(seq 1 150); do
    h="$(height_of "$rpc")"
    if [ -n "${h:-}" ] && [ "$h" -ge "$target" ] 2>/dev/null; then echo "(h=${h})"; return 0; fi
    echo -n "."
    sleep 2
  done
  echo " TIMEOUT" >&2
  exit 1
}

# ----------------------------------------------------------------------------
# Genesis
# ----------------------------------------------------------------------------
build_genesis() {
  echo "--> wiping ${WORKDIR}"
  rm -rf "${WORKDIR}"
  mkdir -p "${LOGDIR}"

  # 1. init every node's home (own node key + priv_validator_key).
  for i in $(seq 0 $((N - 1))); do
    celestia-appd init "node${i}" --chain-id "${CHAIN_ID}" --home "$(APP_HOME "$i")" >/dev/null 2>&1
  done

  # 2. one key + genesis account per validator, all recorded in node 0's genesis.
  for i in $(seq 0 $((N - 1))); do
    celestia-appd keys add "val${i}" --keyring-backend "${KEYRING}" --home "$(APP_HOME "$i")" >/dev/null 2>&1
    local addr
    addr="$(celestia-appd keys show "val${i}" -a --keyring-backend "${KEYRING}" --home "$(APP_HOME "$i")")"
    celestia-appd genesis add-genesis-account "$addr" "${GENESIS_BALANCE}" --home "$(APP_HOME 0)" >/dev/null
  done
  # a non-validator account used to fund escrow / drive the fibre client later
  celestia-appd keys add uploader --keyring-backend "${KEYRING}" --home "$(APP_HOME 0)" >/dev/null 2>&1
  UPLOADER_ADDR="$(celestia-appd keys show uploader -a --keyring-backend "${KEYRING}" --home "$(APP_HOME 0)")"
  celestia-appd genesis add-genesis-account "${UPLOADER_ADDR}" "${GENESIS_BALANCE}" --home "$(APP_HOME 0)" >/dev/null

  # 3. lower the fibre params in node 0's genesis to the protocol floor.
  #    The three keys are unique strings in genesis.json; patch them in place.
  local g="$(APP_HOME 0)/config/genesis.json"
  sed -i.bak -E \
    -e "s#(\"payment_promise_timeout\"[[:space:]]*:[[:space:]]*)\"[0-9]+s\"#\1\"${FIBRE_PROMISE_TIMEOUT}\"#" \
    -e "s#(\"shard_retention\"[[:space:]]*:[[:space:]]*)\"[0-9]+s\"#\1\"${FIBRE_SHARD_RETENTION}\"#" \
    -e "s#(\"withdrawal_delay\"[[:space:]]*:[[:space:]]*)\"[0-9]+s\"#\1\"${FIBRE_WITHDRAWAL_DELAY}\"#" \
    "$g"
  rm -f "${g}.bak"
  echo "--> fibre genesis params:"
  grep -oE '"(payment_promise_timeout|shard_retention|withdrawal_delay)":[[:space:]]*"[0-9]+s"' "$g" | sed 's/^/      /'

  # 4. distribute that genesis, then each node signs its own gentx from its own
  #    home (own priv_validator_key => distinct consensus key in the set).
  mkdir -p "$(APP_HOME 0)/config/gentx"
  for i in $(seq 0 $((N - 1))); do
    [ "$i" -eq 0 ] || cp "$g" "$(APP_HOME "$i")/config/genesis.json"
    local stake="${STAKE_OTHER}"
    [ "$i" -eq 0 ] && stake="${STAKE_NODE0}"
    celestia-appd genesis gentx "val${i}" "${stake}" \
      --chain-id "${CHAIN_ID}" --keyring-backend "${KEYRING}" --home "$(APP_HOME "$i")" \
      --fees "${FEES}" \
      --commission-rate 0.05 --commission-max-rate 1.0 --commission-max-change-rate 0.01 \
      --output-document "$(APP_HOME 0)/config/gentx/gentx-${i}.json" >/dev/null 2>&1
  done

  celestia-appd genesis collect-gentxs --home "$(APP_HOME 0)" >/dev/null 2>&1
  celestia-appd genesis validate-genesis --home "$(APP_HOME 0)" >/dev/null

  # 5. final genesis + persistent_peers to every node.
  local peers="" id
  for i in $(seq 0 $((N - 1))); do
    id="$(celestia-appd comet show-node-id --home "$(APP_HOME "$i")")"
    peers="${peers}${peers:+,}${id}@127.0.0.1:$(p2p_port "$i")"
  done

  for i in $(seq 0 $((N - 1))); do
    [ "$i" -eq 0 ] || cp "$g" "$(APP_HOME "$i")/config/genesis.json"
    local cfg="$(APP_HOME "$i")/config/config.toml"
    local app="$(APP_HOME "$i")/config/app.toml"

    toml_set_section "$cfg" "rpc" "laddr" "\"tcp://0.0.0.0:$(rpc_port "$i")\""
    # celestia-core BlockAPI gRPC (default :9098) and pprof (:6060) also bind per
    # process -- remap so nodes on one host do not collide.
    toml_set_section "$cfg" "rpc" "grpc_laddr" "\"tcp://127.0.0.1:$((19098 + i * 100))\""
    toml_set_section "$cfg" "rpc" "pprof_laddr" "\"localhost:$((6060 + i))\""
    toml_set_section "$cfg" "p2p" "laddr" "\"tcp://0.0.0.0:$(p2p_port "$i")\""
    toml_set_section "$cfg" "p2p" "persistent_peers" "\"${peers}\""
    toml_set_section "$cfg" "p2p" "addr_book_strict" "false"
    toml_set_section "$cfg" "p2p" "allow_duplicate_ip" "true"
    toml_set_section "$cfg" "consensus" "timeout_commit" "\"1s\""
    toml_set         "$cfg" "priv_validator_grpc_laddr" "\"127.0.0.1:$(privval_port "$i")\""
    toml_set_section "$cfg" "tx_index" "indexer" "\"kv\""
    toml_set         "$cfg" "discard_abci_responses" "false"

    # app.toml: per-node gRPC + API, zero min gas price.
    toml_set_section "$app" "grpc" "enable" "true"
    toml_set_section "$app" "grpc" "address" "\"0.0.0.0:$(grpc_port "$i")\""
    toml_set_section "$app" "api" "enable" "true"
    toml_set_section "$app" "api" "address" "\"tcp://0.0.0.0:$(api_port "$i")\""
    toml_set         "$app" "minimum-gas-prices" "\"0utia\"" "minimum-gas-prices"
  done
}

# ----------------------------------------------------------------------------
# Run
# ----------------------------------------------------------------------------
start_nodes() {
  for i in $(seq 0 $((N - 1))); do
    celestia-appd start --home "$(APP_HOME "$i")" \
      --grpc.enable --api.enable \
      --delayed-precommit-timeout 1s \
      > "${LOGDIR}/app${i}.log" 2>&1 &
    APP_PIDS+=($!)
    echo "--> node ${i} pid ${APP_PIDS[$i]}  rpc :$(rpc_port "$i")  grpc :$(grpc_port "$i")  privval :$(privval_port "$i")"
  done
  wait_for_height 2 "$(rpc_port 0)"
}

start_fibre() {
  : > "${LOGDIR}/fibre-pids"   # <i> <pid> <listen_port> per line (for fault injection)
  for i in $(seq 0 $((N - 1))); do
    fibre start --home "$(FIBRE_HOME "$i")" \
      --app-grpc-address "127.0.0.1:$(grpc_port "$i")" \
      --signer-grpc-address "127.0.0.1:$(privval_port "$i")" \
      --server-listen-address "127.0.0.1:$(fibre_port "$i")" \
      --unlimited-budget \
      > "${LOGDIR}/fibre${i}.log" 2>&1 &
    FIBRE_PIDS+=($!)
    echo "${i} ${FIBRE_PIDS[$i]} $(fibre_port "$i")" >> "${LOGDIR}/fibre-pids"
    echo "--> fibre ${i} pid ${FIBRE_PIDS[$i]}  listen :$(fibre_port "$i")  -> app :$(grpc_port "$i") signer :$(privval_port "$i")"
  done
  sleep 8
}

# restart_fibre <i>  -- bring a killed fibre server back (used by the e2e).
restart_fibre() {
  local i="$1"
  fibre start --home "$(FIBRE_HOME "$i")" \
    --app-grpc-address "127.0.0.1:$(grpc_port "$i")" \
    --signer-grpc-address "127.0.0.1:$(privval_port "$i")" \
    --server-listen-address "127.0.0.1:$(fibre_port "$i")" \
    --unlimited-budget \
    >> "${LOGDIR}/fibre${i}.log" 2>&1 &
  local pid=$!
  FIBRE_PIDS[$i]=$pid
  sed -i "s/^${i} .*/${i} ${pid} $(fibre_port "$i")/" "${LOGDIR}/fibre-pids"
  echo "--> restarted fibre ${i} pid ${pid}"
}

register_hosts() {
  for i in $(seq 0 $((N - 1))); do
    if celestia-appd tx valaddr set-host "127.0.0.1:$(fibre_port "$i")" \
      --from "val${i}" --keyring-backend "${KEYRING}" --home "$(APP_HOME "$i")" \
      --chain-id "${CHAIN_ID}" --fees "${FEES}" --node "tcp://127.0.0.1:$(rpc_port 0)" --yes \
      > "${LOGDIR}/register${i}.log" 2>&1
    then echo "--> registered val${i} -> 127.0.0.1:$(fibre_port "$i")"
    else echo "!!  register val${i} FAILED - see ${LOGDIR}/register${i}.log"; fi
    sleep 2
  done
  sleep 4
  echo "--> on-chain fibre providers:"
  local out
  out="$(celestia-appd query valaddr providers --node "tcp://127.0.0.1:$(rpc_port 0)" --output json)"
  if [ "$HAVE_JQ" = 1 ]; then echo "$out" | jq -c '.providers[]?'; else echo "$out"; fi
}

fund_escrow() {
  # escrow for the uploader account (the fibre client signs promises with it).
  celestia-appd tx fibre deposit-to-escrow "${ESCROW_DEPOSIT}" \
    --from uploader --keyring-backend "${KEYRING}" --home "$(APP_HOME 0)" \
    --chain-id "${CHAIN_ID}" --fees "${FEES}" --node "tcp://127.0.0.1:$(rpc_port 0)" --yes \
    > "${LOGDIR}/escrow.log" 2>&1 || echo "!!  escrow deposit FAILED - see ${LOGDIR}/escrow.log"
  sleep 5
  echo "--> uploader escrow:"
  celestia-appd query fibre escrow-account "${UPLOADER_ADDR}" --node "tcp://127.0.0.1:$(rpc_port 0)" --output json
}

summary() {
  cat <<EOF

============================================================
  Fibre devnet up: ${N} validators, chain-id ${CHAIN_ID}
============================================================
  node 0 stake ${STAKE_NODE0}  (stable proposer)
  nodes  1..$((N-1)) stake ${STAKE_OTHER} each

  app RPC     : $(for i in $(seq 0 $((N-1))); do printf "127.0.0.1:%s " "$(rpc_port "$i")"; done)
  app gRPC    : $(for i in $(seq 0 $((N-1))); do printf "127.0.0.1:%s " "$(grpc_port "$i")"; done)
  fibre listen: $(for i in $(seq 0 $((N-1))); do printf "127.0.0.1:%s " "$(fibre_port "$i")"; done)

  fibre params : payment_promise_timeout=${FIBRE_PROMISE_TIMEOUT}  shard_retention=${FIBRE_SHARD_RETENTION}
                 => pruneAt = creation + 10m ; prune loop ticks every 60s

  uploader acct: ${UPLOADER_ADDR}  (escrow funded; keyring ${KEYRING} @ $(APP_HOME 0))
  consensus addrs:
$(for i in $(seq 0 $((N-1))); do printf "    val%s  %s\n" "$i" "$(celestia-appd comet show-address --home "$(APP_HOME "$i")")"; done)

  logs: ${LOGDIR}/
  fibre pids: ${LOGDIR}/fibre-pids
  Ctrl-C to tear down.
============================================================
EOF
}

# ----------------------------------------------------------------------------
main() {
  preflight
  build_genesis
  start_nodes
  start_fibre
  register_hosts
  fund_escrow
  summary
  # readiness marker for wrappers (e.g. run-e2e.sh) to poll on.
  date +%s > "${WORKDIR}/READY"
  wait "${APP_PIDS[0]}"
}
main "$@"
