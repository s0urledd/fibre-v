# fibre-devnet

A multi-validator local Celestia Fibre devnet, for exercising shard assignment
and retention end to end.

`multi-node-fibre.sh [N]` (default 4, minimum 2) brings up, on one host:

- **N `celestia-appd` validators**, full mesh, near-equal stake. Node 0 has a
  hair more so it is the stable block proposer; all N stay up for consensus.
- **One `fibre` server per validator.** Each fibre server signs payment
  promises by delegating `SignRawBytes` to its node's `priv_validator_grpc_laddr`
  (there is no standalone signer — so every validator in the set needs a running
  node process). During fault testing you kill a *fibre server*, never a node.
- Each fibre host registered on chain (`celestia-appd tx valaddr set-host`).
- A funded `uploader` account with escrow deposited, for driving a fibre client
  or a Sentinel probe.

## Why a multi-node devnet

`celestia-app/scripts/single-node-fibre.sh` brings up one validator — and with
one validator the shard assignment is degenerate: that validator gets all 4096
original rows, so retention and reconstruction behaviour can't be observed.
Assignment only becomes interesting (rows split, no single validator holding
enough to matter, wrap-around) with several validators of comparable stake.

## Short lifetimes

Genesis `x/fibre` params are lowered to the **protocol floor** — the same idea
as `setFibreShortLifetimes` in `celestia-app/fibre/internal/e2e/fibre_stack_test.go`:

| param | default | devnet | floor |
|---|---|---|---|
| `payment_promise_timeout` | 1h | **10m** | `MinPaymentPromiseTimeout` = 10m |
| `shard_retention` | **4h** | **10m** | `MinShardRetention` = 10m |
| `withdrawal_delay` | 24h | 12h10m | `MinWithdrawalDelay` = MaxPromiseTimeout + 10m |

So `pruneAt = creation_timestamp + max(10m, 10m) = creation + 10m`, and the
fibre server's prune loop ticks every 60s. **10 minutes is the hard protocol
minimum** — `Params.Validate()` rejects anything lower, on chain and via
`MsgUpdateFibreParams`. That is why the end-to-end tests take ~15 minutes.

Ports (node `i`): RPC `26657+i·100`, P2P `26656+i·100`, SDK gRPC `9090+i·10`,
API `1317+i·10`, privval gRPC `26669+i·100`, core BlockAPI gRPC `19098+i·100`
(remapped from `:9098`), pprof `6060+i`, fibre listen `7980+i`.

## Prerequisites

`celestia-appd` and `fibre` on `PATH`, plus `curl` (`jq` optional, only for
prettier output). Build them from a celestia-app checkout at the pinned commit
`0b69316466c3ba02f708c0e2a101f834d5d1827f` (v10; `x/fibre` and `x/valaddr` are
live from block 1, no upgrade):

```
go build -tags ledger -o build/celestia-appd ./cmd/celestia-appd
go build             -o build/fibre           ./fibre/cmd
```

## Run

```
./multi-node-fibre.sh 4
#  ~1-3 min to genesis + READY (4-node gentx is slow on some platforms).
#  Prints every endpoint, the uploader address, per-validator consensus addrs.
#  Writes ${FIBRE_DEVNET_HOME:-~/.fibre-devnet}/READY when it is up.
#  Ctrl-C tears everything down; homes + logs stay under the workdir.
```

## Status

Smoke-tested at **N=2 and N=4** with `celestia-appd` + `fibre` built from the
pinned commit: nodes produce blocks, every fibre server comes up (signer +
app-gRPC connected), every host registers on chain, the uploader escrow funds.
The Fibre Sentinel repo's `fibre-sentinel/devtest.sh` and
`fibre-sentinel/probe-devtest.sh` drive full N=4 runs against this script,
including a mid-window fault injection, and both pass.

Portability notes (watch for these if you change the script):

- Genesis `gentx` needs `--fees` — celestia enforces a network minimum gas price
  even at InitChain, so a zero-fee gentx panics the node on startup.
- Per-process ports that collide on one host and are remapped:
  `priv_validator_grpc_laddr` (`:26669`), the celestia-core BlockAPI gRPC
  `[rpc] grpc_laddr` (`:9098`), `[rpc] pprof_laddr` (`:6060`), plus the obvious
  RPC / P2P / SDK-gRPC / API.
- `celestia-appd version` prints nothing without release ldflags — harmless.
- Cleanup helpers (`taskkill` fallback, `netstat -ano` PID parse) are
  Windows/git-bash specific; on Linux the `lsof`/`kill` path is used instead.

## What the end-to-end flow looks like

1. Upload a blob with the fibre client (funded `uploader` account); get its
   `BlobID` and promise height.
2. `fibre-assign`: compute the assignment for that commitment over the validator
   set at the promise height, with `assign.ParamsV10BlobV0`.
3. For each **assigned** validator, `DownloadShard` from its registered host
   (over a `fibre-tlsverify` TLS config), verify the row proofs / RLC against
   the commitment (`pkg/rsema1d`), and `ShardMap.Verify` the returned indices.
   An un-assigned validator returning `NotFound` is normal.
4. Repeat on a schedule until `creation + 10m`; expect a clean flip to
   `NotFound` shortly *after* that, not before.

`fibre-sentinel` implements exactly this, persistently.
