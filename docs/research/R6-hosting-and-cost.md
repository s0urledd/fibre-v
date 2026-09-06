# R6. Hosting and cost for the observer

Date: 6 September 2026. Inputs: the probe policy in `R4-probe-etiquette.md`
(shard sizes in its section 1.3, caps in section 3.5) and the component list
in `docs/adr/0001-observer-architecture.md`. All arithmetic is shown; change
an input and the rest follows.

## 1. What runs where

One VM for the MVP, hosting collector, prober, store, API and web. A second
region is a second prober only; it writes to the same store over the API's
private ingest endpoint or ships its measurements file. Why two regions would
be better, and why not for MVP:

- A single vantage cannot tell "validator unreachable" from "path between us
  and the validator is broken". Two vantages on different providers turn a
  simultaneous failure into strong evidence and a one-sided failure into a
  routing note. This matters most for `FAULT` verdicts based on reachability.
- Every rate the MVP shows is labelled "observed from one location"
  (`docs/verdicts.md`), which is honest but weaker. The second vantage is the
  cheapest credibility upgrade available and is phase 2 only because it
  doubles probe load on validators (R4 section 3.5 makes per-validator caps
  per vantage) and needs the policy published first.

## 2. Bandwidth per month under the R4 policy

Assumptions, each stated so it can be changed:

| input | value | source |
|---|---|---|
| validators with a registered Fibre endpoint | 100 | brief; mocha-5 active set size is not fixed, treat 100 as the upper bound used by R4 |
| sum of assigned rows across the set | 12,288 (3 × 4096, zero wrap overlap) | `docs/fibre-operational-notes.md` in the code repo, R4 section 7 |
| shard downloads per (validator, blob) | 5 | R4 section 3.1 |
| bytes of a whole network probe round for one blob | Σ over validators of shard bytes ≈ 415 MB for a 128 MiB blob, 15.6 MB for a 1 MiB blob, ≈ 13.9 MB for a 256 KiB blob | R4 section 3.4 (128 MiB, 1 MiB); 256 KiB derived below |
| heartbeat | 100 validators × 6 per hour × 3 KB | R4 section 3.1 |

256 KiB blob round: R4 section 1.3 gives 146,644 B for 148 rows and
2,310,148 B for 4096 rows. Per-row cost at 64-byte rows is 64 + 448 + 8 = 520 B,
so a round over 12,288 rows is 12,288 × 520 + 100 × 65,540 ≈ 6.39 MB + 6.55 MB
≈ 12.9 MB. Call it 13 MB.

### Scenario E: early Fibre on mocha-5 (the realistic case)

Early testnet traffic is a handful of test publications, not a stream. Assume
50 blobs/day, size mix 40 × 1 MiB and 10 × 128 MiB.

| item | per day | per month (30 d) |
|---|---|---|
| 128 MiB blobs: 10 × 5 × 415 MB | 20.8 GB | 623 GB |
| 1 MiB blobs: 40 × 5 × 15.6 MB | 3.1 GB | 94 GB |
| heartbeat: 100 × 144 × 3 KB | 0.04 GB | 1.3 GB |
| chain RPC (collector, 2 to 3 calls per block at ~6 s blocks, a few KB each; validator set at promise heights) | ~1 GB | ~30 GB |
| total ingress to the observer | ~25 GB | **~750 GB** |

Egress from the observer is small: gRPC requests are bytes, and the dashboard
serves JSON and static files. Budget 50 GB/month for the site.

### Scenario A: 1 × 128 MiB blob per minute (R4's stress case)

R4 computes 125 GB/h before caps. The global cap of 50 GB/h binds, so the
prober samples at p ≈ 0.40 and ingress is capped at:

| item | value |
|---|---|
| hourly | 50 GB (cap) |
| daily cap | 600 GB (R4 sets a lower daily cap than 24 × hourly) |
| monthly | **18 TB** |

This is the number to design the cap around, not the number to expect. It is
also why the daily cap exists: 18 TB/month of ingress is tolerable on
providers that do not meter ingress, and ruinous on ones that do.

### Scenario B: 10 × 1 MiB blobs per minute

R4: 47 GB/h before caps, request cap binds at p = 0.5, so ≈ 24 GB/h,
≈ 560 GB/day, ≈ **17 TB/month** at the cap. Same conclusion as A.

## 3. Storage growth of the store

Per probe row: the sentinel `Measurement` record is ~1.5 KB as JSON; in
SQLite with indexes assume 2 KB. Publications with full row lists are larger:
12,288 row indices at ~5 bytes each in JSON ≈ 60 KB per publication when
stored inline. Recommendation from ADR 0001: store the assignment as
(publication, validator, row_count) plus a compact row-index blob, ≈ 25 KB
per publication.

| scenario | probes/day | probe bytes/day | publications/day | pub bytes/day | total/month |
|---|---|---|---|---|---|
| E | 50 × 100 × 6 = 30,000 | 60 MB | 50 | 1.3 MB | ~1.8 GB |
| A at p = 0.4 | 1440 × 0.4 × 100 × 6 = 345,600 | 690 MB | 1440 | 36 MB | ~22 GB |

Either fits on a 100 GB disk for the first months. Daily `snapshots` rows
(100 validators × 1 row) are negligible.

## 4. Compute

The dominant CPU cost is proof verification: ~3000 rows in 14 to 24 ms on a
laptop-class core (`docs/fibre-operational-notes.md`). Scenario A at p = 0.4
is 345,600 probes/day = 4 per second, each ≤ 25 ms of CPU: well under one
core. The collector is I/O bound. The API and web are static-friendly. A
4 vCPU, 8 GB RAM VM is generous; 2 vCPU, 4 GB would run Scenario E.

Network interface: Scenario A's 50 GB/h cap is 111 Mbps sustained; a 1 Gbps
port is required so that a single 122 MB shard from a 30% validator does not
hold a validator connection slot for more than a few seconds (R4 section 7,
item 5).

## 5. Cost estimate

Prices are order-of-magnitude, from public list prices as generally known
in 2026; verify before buying. The point is the shape of the bill, not the
cents.

| item | Scenario E | Scenario A (at cap) |
|---|---|---|
| VM, 4 vCPU / 8 GB / 160 GB, 1 Gbps port, unmetered or ≥ 20 TB traffic included (Hetzner-class) | ~€15 to €30/month | same |
| same shape on a hyperscaler with metered ingress free and egress billed | ~$60 to $120/month; egress ~50 GB, negligible | same VM; ingress is free on the major clouds, so the 18 TB does not bill, but check the provider |
| second region prober (phase 2), 2 vCPU / 4 GB | ~€5 to €15/month | same |
| domain, TLS via Caddy (Let's Encrypt) | ~€10 to €15/year | same |
| chain RPC | free if using the team's own mocha-5 node or a public endpoint; note the celestia-core v0.41.0 heavy-RPC limit of 20 concurrent (R1 section 8) argues for the team's own node | same |

Realistic MVP bill: one Hetzner-class VM, well under €50/month including
domain. The observer's own bandwidth is not the constraint; the validators'
is, and R4's caps are set from theirs, not ours.

## 6. What would change these numbers

- A `DownloadShard` row subset API would cut probe bytes by an order of
  magnitude for large validators. There is none today (R1 section 4, R4
  section 1.2).
- A download rate limiter on the server (core says it is planned, R4 section
  2.1) would turn the observer's self-imposed caps into enforced ones; the
  policy already honours `RetryInfo`.
- Validator count above 100 raises the heartbeat and per-round costs
  linearly; row totals stay at ≈ 3 × 4096 plus overlap, so probe bytes do not
  grow with validator count beyond the 148-row floor effect.
