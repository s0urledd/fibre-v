# Fibre's operational realities

*Draft. Raw numbers from a local multi-validator devnet
(`fibre-devnet/multi-node-fibre.sh`, N=4) running celestia-app at commit
`0b69316466c3ba02f708c0e2a101f834d5d1827f` (the code has since been re-pinned
to `v10.1.0-corto`, whose fibre protocol, assignment and TLS code is
byte-identical), plus the constants compiled into
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
An entry is deleted on the first tick whose wall-clock minute is strictly
greater than the minute of `pruneAt` (`PruneBefore` compares the
`YYYYMMDDHHmm` keys), so the lag is anywhere in the open interval (0 s,
120 s): as little as a second when `pruneAt` falls at hh:mm:59 and the tick
lands at hh:mm+1:00, just under two minutes when it falls at hh:mm:00 and
the tick phase is :59. Never early on the server's own clock. In practice
`must_serve_until + ~1m45s` was measured below.

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

## Run of 16 September 2026: 8 MiB blobs at v10.1.0-corto

The earlier numbers in this document come from a devnet that only ever
published blobs of a few hundred KB. This run was made to exercise the size
the real network plans for. `celestia-appd` and `fibre` were built from
`v10.1.0-corto` (`fa5b523b`), the tag the observer pins; four validators,
retention floored at 10 minutes, two blobs of 8,650,752 bytes each.

Assignment came out at 3186 / 3035 / 3035 / 3035 rows, 12,291 distinct of
16,384, no wrap overlaps. At a row size of 2112 bytes that is a shard of
about **7.95 MiB per validator**, which matters because grpc-go's default
receive limit is 4 MiB: before the receive limit was raised to the reference
client's (`ProtocolParams.MaxMessageSize()`), every one of these downloads
would have failed with `ResourceExhausted` and been recorded as an in-window
FAULT against an honest validator. They returned `SERVED_OK`, with every row
verified against the commitment and against the recomputed assignment.

One validator's Fibre server was killed 2 minutes into the window. The
resulting classifications are the whole taxonomy in one run:

| phase | that validator | the other three |
|---|---|---|
| w1 (before the kill) | HEALTHY | HEALTHY |
| w2, w3, w4 | FAULT (`TCP_REFUSED`) | HEALTHY |
| grace | TOLERATED | HEALTHY, or TOLERATED once pruned |
| post | UNREACHABLE_POST_WINDOW | EXPECTED_GONE |

Totals over 48 probes: serve rate 29/35 (82.9 %), reachability 3/4, both
blobs `degraded` at their last complete in-window point (9,256 distinct rows
served of the 4,096 needed, so still reconstructable without the faulted
validator), zero observation gaps.

Incidental measurements from the same run:

| quantity | value |
|---|---|
| bytes downloaded by the observer | 221 MiB over 13 minutes, 29 downloads |
| one probe of a 7.95 MiB shard | 130 to 195 ms |
| four validators probed in parallel | within 35 ms of each other |
| observer clock offset from chain time | 1.7 to 3.2 s (devnet block lag) |
| `measurements.jsonl` for 48 probes | 80 KB |

The database was then deleted and rebuilt from the JSONL files alone: same 48
probes, same 29/35. The raw files are the record; the database is derived.

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
6. A shard is **large**: 7.95 MiB per validator for an 8 MiB blob on four
   validators, and the planned mainnet blob is 128 MiB. Any client of the
   Fibre read path, an observer included, has to raise gRPC's default 4 MiB
   receive limit and size its timeouts by the shard, not by a constant.

## Run of 16 September 2026: the taxonomy split, on live chain data

A four-validator devnet at `v10.1.0-corto`, two 2.25 MiB blobs, one Fibre
server killed after the first in-window probe. 48 probes over the full
schedule. What it establishes:

**Signature verification works against real `MsgPayForFibre`.** Both
publications carried four signature entries; all four verified positionally
against the validator set at the promise height, with nothing unmatched and
nothing out of position. Attested voting power equalled total voting power.
This is the first time the verification has run on chain-produced signatures
rather than on constructed test vectors.

**A killed server is reported as unreachable, not as breaking its promise.**
The six probes of the dead endpoint came back `TCP_REFUSED` and were
classified `UNREACHABLE`. Under the previous taxonomy they were six `FAULT`
rows, and the published serve rate was 26/32 = 81%: an accusation against a
validator when all the observer knew was that it could not reach the host.

| figure | value | what it says |
|---|---|---|
| serve rate | 26 / 26 | every probe that produced a verdict was a shard served |
| verdict coverage | 26 / 32 | six in-window probes produced no verdict, and the page says so |
| held out | `UNREACHABLE` 6 | named, not hidden |
| by obligation | 8 / 8 | one observation per (validator, blob), judged by its newest probe; the headline and the basis for any interval |
| by schedule point | w1 8/8, w2 6/6, w3 6/6, w4 6/6 | the drop from 8 to 6 is the moment the server was killed |
| attestation | 32 / 32 | every probe was of an obligation the chain proves |
| worst correlated point | 1 of 4 unreachable, below the 0.5 threshold | one validator down is not the observer's own network |
| reconstructable | 0 fully served, 2 degraded, 2 recoverable | the rows all came back; not everyone obliged answered |
| reachability | 3 / 4 | |

The reconstructability row is the clearest illustration of why "degraded" is
now reported separately. Three of four validators held 9,256 distinct rows
against the 4,096 needed, so both blobs were recoverable in full. Folding
that into a success rate would have published "100% reconstructable" while a
validator that was proven to owe the blob served nothing; reporting it as
zero would have implied the data was lost. Neither is true, so both numbers
are published.

The dashboard rendered every one of these with no console errors, and the
database was rebuilt from the JSONL to the same numbers.

What this run does **not** cover: `SHADOWED_SHARD` needs two promises over
one commitment under different assignments, which needs a validator-set
change mid-run; `NOT_REGISTERED` with the last-known-host fallback needs a
jailing, which takes thousands of blocks at the default downtime window.
Both are covered by unit tests only.
