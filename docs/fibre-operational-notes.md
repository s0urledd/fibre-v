# Fibre's operational realities

*Draft. Raw numbers from a local multi-validator devnet
(`fibre-devnet/multi-node-fibre.sh`, N=4) running celestia-app at commit
`0b69316466c3ba02f708c0e2a101f834d5d1827f`, plus the constants compiled into
that build. None of the numbers below appear in Fibre's public docs or specs;
they are what you find by reading the source and watching a network.*

---

## The promise, and what isn't written down

Fibre is Celestia's low-latency data-availability path. A client pays for a
blob; the validators sign that they received it; each validator then owes a
window of **serving** its assigned slice of the erasure-coded rows to anyone who
asks. The on-chain `MsgPayForFibre` records the payment and the signatures. It
does not record — and consensus does not enforce — whether the serving actually
continues.

Everything that governs that serving obligation is either an on-chain parameter
that nobody has written a guide to, or a compiled-in constant that no RPC
exposes. This note collects them.

## Shard lifetime: 4 hours by default, 10 minutes minimum

The window a shard must stay available is:

```
must_serve_until = creation_timestamp + max(payment_promise_timeout, shard_retention)
```

(`fibre/server_upload.go` `shardPruneAt` = `max(creation+retention, expiresAt)`,
where `expiresAt = creation + payment_promise_timeout`.)

| param | default | min | max |
|---|---|---|---|
| `shard_retention` | **4h** | 10m | 7d |
| `payment_promise_timeout` | 1h | 10m | 12h |
| `payment_promise_height_window` | 1000 blocks | — | — |
| `withdrawal_delay` | 24h | ~12h10m | 7d |

`shard_retention` was added specifically to **decouple** the serving obligation
from `payment_promise_timeout` — before it, a shard only had to outlive the
payment's settlement window. Now the default is 4 hours of guaranteed
retrievability per blob, independent of when (or whether) the payment settles.
`Params.Validate()` rejects a `shard_retention` below 10 minutes both on chain
and through `MsgUpdateFibreParams`, so 10m is a hard floor — which is why a
devnet that wants to observe pruning in minutes sets it exactly there.

## Pruning is late, and predictably so

A shard is not deleted at `must_serve_until`. The fibre server's prune loop runs
**once a minute** (`fibre/server_prune.go`: `pruneInterval = time.Minute`) and
the prune key is stored at **minute precision** (`store.go` `formatTimestamp`).
So the effective deletion time is the first minute-boundary tick at or after
`must_serve_until`, rounded up — in practice `must_serve_until + ~1 to 2
minutes`.

Measured on the devnet (25-second probe resolution, `shard_retention` = 10m so
`pruneAt` = `creation + 10m`):

```
pruneAt − 4m20s   3 validators: SERVED_OK
pruneAt + 1m18s   3 validators: SERVED_OK      <- last SERVED_OK
pruneAt + 1m43s   3 validators: NOT_FOUND      <- first NOT_FOUND, all three at once
```

Real deletion landed in `(pruneAt + 80s, pruneAt + 105s]`. All surviving
validators flipped together, because they share the prune cadence.

**For monitoring this is the important number.** A monitor that treats the first
`NOT_FOUND` after `pruneAt` as a retention failure raises a false alarm on every
single blob — the lag *is* the honest behaviour. The tolerance window has to be
set from the measured lag (here: `pruneAt + ~1m45s`, with margin), not from the
protocol deadline. `fibre-sentinel` defaults its grace window to
`must_serve_until + 2m30s` and exposes it as `-prune-tolerance`.

## Assignment: each validator serves ~3× its stake share of the rows

Blob v0 encodes into `OriginalRows = 4096` data rows and `TotalRows = 16384`
(encoding ratio 0.25). A validator's row count is:

```
rows = clamp( ceil( OriginalRows · power · LT.den / (totalPower · LT.num) ),
              MinRowsPerValidator, OriginalRows )
```

with `LivenessThreshold = 1/3`, so the multiplier is `1 / (1/3) = 3`. A
validator holding fraction *f* of the voting power serves about **3·f·4096**
rows (capped at 4096, floored at `MinRowsPerValidator = 148`).

Observed, N=4, near-equal stake (`totalPower` = 4,050,000):

| validator | power | fraction | rows | `ceil(4096·3·f)` |
|---|---:|---:|---:|---:|
| proposer | 1,050,000 | 0.2593 | **3186** | 3186 |
| other ×3 | 1,000,000 | 0.2469 | **3035** | 3035 |

- **Σ = 12,291 rows, all distinct, zero wrap-overlap.** The network as a whole
  stores ≈ `3 · OriginalRows` — three times the 4096 needed to reconstruct.
- **No single validator can reconstruct** (3186 < 4096). Any two can.
- The blob survives the loss of any set of validators holding **less than 1/3**
  of the stake — that is the `LivenessThreshold` made concrete. Lose 1/3+ and
  the distinct rows can drop below 4096.

`MinRowsPerValidator` (148) and `LivenessThreshold` (1/3) are **not on chain**
and not returned by any RPC — they are `toml:"-"` constants in
`fibre/protocol_params.go`. `MinRowsPerValidator` is derived once at startup
from a float formula (the unique-decodability floor). Anything recomputing an
assignment has to pin them to a specific celestia-app build; `fibre-assign`
refuses to guess.

## Storage budget: 2 TiB per retention window at full stake — ~146 MiB/s

`FullStakeStorageBudget` defaults to `2 << 40` = **2 TiB**
(`x/fibre/types/params.go`). That is the Fibre disk a 100%-stake validator is
expected to devote over **one `shard_retention` window**. Over the default 4h
window:

```
2 TiB / 4h  ≈  145.6 MiB/s   sustained write, at full stake
```

A validator with fraction *f* of the stake budgets `2·f` TiB per window. This is
a soft per-node cap the server enforces locally; the devnet runs the fibre
server with `--unlimited-budget` to take it out of the picture during
retention/assignment testing. On mainnet-scale parameters it is the number that
bounds how much Fibre throughput the validator set can actually absorb.

## Probe cost

One retrievability probe = dial (TCP + TLS 1.3) + `DownloadShard` + verify every
returned row proof and the RLC vector against the commitment (`pkg/rsema1d`) +
check the returned indices against the recomputed assignment.

Localhost, one validator (~3000 rows), from two devnet runs:

| | p50 | p95 | max | n |
|---|---:|---:|---:|---:|
| run A (25s cadence, 34 rounds) | ~14 ms | ~24 ms | 42 ms | ~98 |
| run B (probe-devtest) | ~24 ms | ~40 ms | 98 ms | 27 |

Fast-fail cases are near-free: a dead server refuses TCP in **0–1 ms**; a
pruned-but-reachable server answers `NOT_FOUND` in **3–13 ms**. Publishing a
blob (upload to all validators + collect signatures) took **~70 ms**.

Over a real WAN, add one to two round-trips for the TLS 1.3 handshake plus the
download RTT; the CPU cost (verifying ~3000 proofs) is unchanged and small. The
discovery side (`sentinel-scan`) is 2–3 RPC calls per block and negligible.

The dominant cost of running a Sentinel is not CPU or bandwidth — it is the
**schedule**: to prove continuous serving you need several probes spread across
each blob's 4-hour window, per assigned validator, per vantage point.

## Fault behaviour

Killing one assigned validator's fibre server mid-window:

- The next probe to that validator returns `RPC_UNAVAILABLE` / `TCP_REFUSED`
  within **~1 ms** — a killed server is unambiguous and cheap to detect.
- The blob still fully reconstructs from the survivors: in run A, `30 ms`,
  `98304` bytes byte-identical, from the 9216 distinct rows the three remaining
  validators still held (4096 needed).
- After `must_serve_until`, that same unreachability is **not** a fault — the
  obligation is over. `fibre-sentinel` classes it `UNREACHABLE_POST_WINDOW`.

A full `probe-devtest` run — 3 blobs, 5 schedule points each, 4 validators, one
killed mid-window — produced **60 measurements** and the taxonomy held with zero
misclassifications: 27 `HEALTHY`, 9 `FAULT` (the killed validator, in-window), 12
`TOLERATED` (grace, post-prune), 9 `EXPECTED_GONE`, 3 `UNREACHABLE_POST_WINDOW`.

## Takeaways

1. The serving guarantee is **4 hours by default** and entirely off-chain. If
   you depend on Fibre retrievability, something has to watch it.
2. Honest pruning is **late by 1–2 minutes** and network-wide simultaneous. Any
   retention monitor must tolerate that or it cries wolf constantly.
3. Assignment gives **~3× redundancy** and tolerates **<1/3 stake loss** — but
   the constants that set this are compiled in, not on chain, so a checker has
   to pin a build.
4. Full-stake Fibre storage is budgeted at **~146 MiB/s** (2 TiB / 4h); that is
   the real ceiling on Fibre throughput.
5. Detecting a *down* validator is trivial and instant. Detecting one that
   serves *only when watched*, or prunes early, is why the probe schedule has to
   be dense and spread across the whole window.
