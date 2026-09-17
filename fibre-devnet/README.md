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

## Stake shape, and why it matters more than validator count

By default every validator gets near-equal stake. That is the wrong shape for
most of what is worth testing, because almost everything a real network does
differently follows from the stake being skewed rather than from there being
more validators.

```bash
FIBRE_DEVNET_POWERS=fibre-devnet/powers/mocha-5.txt ./multi-node-fibre.sh 30
```

`fibre-devnet/powers/` holds voting-power snapshots of real networks, one power
per line, largest first, with the chain, height and time they were taken in the
header. `snapshot-powers.py <rpc>` refreshes one.

A smaller set is picked by spreading evenly down the curve, tail included, so it
keeps the shape of the real one. Taking the largest N instead — `FIBRE_DEVNET_POWER_PICK=head` —
drops the entire class of small validators, and that class is where the
interesting behaviour lives. Measured against the mocha-5 snapshot (79
validators, 322,778,683 total power):

| pick | share range | lifted to the 148-row floor |
|---|---|---|
| largest 20 | 2.33% – 13.1% | **0 of 20** |
| spread 20 | 0.0000003% – 26.1% | 2 of 20 |
| largest 30 | 1.89% – 10.6% | **0 of 30** |
| spread 30 | 0.0000003% – 18.9% | 9 of 30 |
| all 79 | 0.0000003% – 7.2% | **33 of 79** |

Two thirds of the real network's validators are small enough that the clamp
decides their row count, and on mocha the smallest has a voting power of **1**.
A run that never lifts anyone to the floor has not tested the branch 42% of the
network lives on.

Twelve validators spread down that curve give a 27-fold range in assigned rows
and trip both clamps at once — the largest is cut to the 4,096 ceiling, the
smallest lifted to the 148 floor:

```
  power        share      rows
  23110000     39.4225%   4096   <- ceiling (raw 4841)
  ...
  1             0.0000%    148   <- floor (raw 1)
```

Spreading preserves the shape but not the shares: sampling 12 of 79 pulls the
largest from 7.2% up to 39%. For mocha's actual shares, run all 79 — at the
measured 445 MB per validator (315 MB for the node, 130 MB for its fibre
server) that needs about 35 GB of RAM.

### Producing each verdict class on purpose

The taxonomy has nine classes and a devnet that just runs produces two of them.
The rest come from doing something to it, and the two that matter most come from
the same action at different times:

```bash
# 1. start the devnet on a real curve, publish a few blobs
FIBRE_DEVNET_POWERS=powers/mocha-5.txt ./multi-node-fibre.sh 12
sentinel-pub -count 6 -gap 20s

# 2. stop two fibre servers and leave them down
awk '$1==10 || $1==11 {print $2}' ~/.fibre-devnet/logs/fibre-pids | xargs kill

# 3. keep publishing
sentinel-pub -count 6 -gap 20s
```

Blobs from step 1 were uploaded to those two validators and signed by them, so
the chain proves they held the shard: a probe that cannot reach them afterwards
is **UNREACHABLE**. Blobs from step 3 never reached them, so they never signed
and nothing proves they were ever sent anything: a probe is **UNATTESTED**,
whatever happened on the wire. Same two validators, same downtime, two different
classes, and only one of them is ever held against anyone — which is the
distinction the whole taxonomy exists to make.

`IDENTITY_EXPIRED` needs a certificate whose signed validity window has lapsed;
`NOT_REGISTERED` needs a validator with no `x/valaddr` entry; `TOLERATED` and
`EXPECTED_GONE` arrive on their own once a blob's retention window closes.

**`sentinel-pub` publishes at the protocol's safety threshold by default.** It
used to pass `WithAwaitAllSignatures()` unconditionally, which waits for every
validator rather than stopping at two thirds of voting power. That had two
consequences worth knowing about if you read older runs: every devnet blob came
back with a full signature set, so `UNATTESTED` — about a third of every real
assignment — was never produced at all; and a publish FAILED outright the moment
any validator was down, with `not enough voting power: collected X, required Y`
where Y is the whole set rather than two thirds. Pass `-await-all` if you
deliberately want every signature.

### Two thirds, and why most validators have no signature on chain

The publisher stops collecting signatures once **two thirds of voting power**
has answered (`fibre/validator/signature_set.go`), and the chain's own check
uses the same threshold. Validators past that point still receive the shard —
delivery continues in the background — but their signature never reaches the
chain, so nothing on chain proves they hold it.

It is a race, not a fixed list: membership follows upload-completion order, so
the same validator is in some quorums and not others. How large the quorum is
can be bounded: simulated against the mocha snapshot over 20,000 uniformly
random arrival orders it holds a median of 53 of 79 validators, and the floor
is 30, the case where the largest validators all answer first — a bound, not an
expectation. Who is in it cannot be bounded that way: completion order depends
on shard size (which scales with stake), on the network path between publisher
and validator, and on the validator's own write-and-sign latency, none of which
a uniform draw models. An earlier version of this section said inclusion was
flat across stake; that was the simulation's assumption read back as a result,
and it is withdrawn.

The observer classifies those probes `UNATTESTED` and holds them out of the
serve rate in both directions. A devnet with equal stake barely produces the
class; one on a real curve produces it the way the real network will.

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

Ports (node `i`, plus `FIBRE_DEVNET_PORT_OFFSET` if set — use it on a host that
already runs a chain on 26657/9090): RPC `26657+i·100`, P2P `26656+i·100`, SDK gRPC `9090+i·10`,
API `1317+i·10`, privval gRPC `26669+i·100`, core BlockAPI gRPC `19098+i·100`
(remapped from `:9098`), pprof `6060+i`, fibre listen `7980+i`.

## Prerequisites

`celestia-appd` and `fibre` on `PATH`, plus `curl` (`jq` optional, only for
prettier output). Build them from a celestia-app checkout at the pinned commit
`fa5b523b7e3b2b83bd16bc072a45cbd3819fa369` (v10; `x/fibre` and `x/valaddr` are
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
