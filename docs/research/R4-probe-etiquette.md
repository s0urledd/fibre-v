> Note: the 0.65 Gbps / 1.18 TB floor-validator sizing that this note treats as an assumption was relayed in the project brief as a Celestia core statement on Discord (4 September 2026). It has no public URL. Section 2.5 below is correct that it cannot be verified from public sources; the Discord message would resolve it.

# R4 — Probe etiquette and load policy for a Fibre observer

*Research note, 6 September 2026. Source facts are quoted from the local
`celestia-app` checkout at commit `5735e05013780b513b190f8253554f9e71781f4d`
(main, 4 Sep 2026) and from the observer repo (`plsgiveup/fibre`, local
checkout). Public statements were read on 6 Sep 2026 at the URLs given.
Anything I could not verify is marked **UNVERIFIED**.*

---

## 0. Summary

| Question | Answer | Where |
|---|---|---|
| Does the fibre server rate-limit downloads? | **No.** No per-peer limit, no bandwidth cap, no download concurrency limit. What exists: a 16-connection listener cap, 13 streams per connection, keepalive enforcement (min 10 s between client pings), a 15 s connection-setup timeout, max message size ≈ 132 MiB. | §1 |
| Can a probe ask for a subset of rows? | **No.** `DownloadShardRequest` carries only `blob_id`; the server returns the whole stored shard. Minimum bytes per successful probe = the validator's entire assigned shard (+ a fixed 64 KiB RLC vector). | §1.2 |
| Has core said anything about acceptable probe load? | Only that download rate limiting "is planned for the next iterations" and that community dashboards checking reachability and serving are expected. **No number for acceptable probe frequency exists anywhere public.** | §2 |
| Proposed default | 6 requests per (validator, blob) over the window (5 carry bytes); every blob probed while under budget, otherwise unbiased deterministic sampling; per-validator concurrency 1; hourly cap = 1 % of the floor validator's sized ingress (2.9 GB/h at 148 rows, scaled by rows); observer-side global cap 50 GB/h. | §3–§6 |
| Headline arithmetic | At 1 × 128 MiB blob/min, the observer costs a validator **0.5 %** of its sized capacity (any stake). At the ingest rate the hardware was actually sized for, full-shard probing would need **5× the validator's capacity** — so sampling is not optional, it is structural. | §3.4 |

---

## 1. What the server actually implements (source)

### 1.1 Transport limits — everything that exists

All of the caps live in `fibre/internal/grpc/server.go`; nothing in
`fibre/server.go`, `fibre/server_config.go` or `fibre/cmd/` adds any other
limit.

| Option | Value | Effect on a probe | Source (commit 5735e05) |
|---|---|---|---|
| `netutil.LimitListener(listener, maxConnections)` | `maxConnections = 16` | At most **16 TCP connections total**, from all peers combined. A probe that holds a connection holds 1/16 of the validator's entire client capacity. | `fibre/internal/grpc/server.go:28,60` |
| `grpc.MaxConcurrentStreams` | `maxConcurrentStreams = 13` | ≤ 13 in-flight RPCs per connection. | `server.go:29,73` |
| `grpc.ConnectionTimeout` | `15 * time.Second` | TCP+TLS+HTTP/2 setup must finish in 15 s or the slot is freed. | `server.go:33,74` |
| `grpc.KeepaliveEnforcementPolicy{MinTime}` | `10 * time.Second` | A client that pings more often than every 10 s gets `GOAWAY too_many_pings`. | `server.go:36,75-77`; grpc-go v1.83.2 `internal/transport/http2_server.go:930` |
| `grpc.KeepaliveParams{MaxConnectionIdle}` | `5 * time.Minute` | Idle connections are closed by the server. | `server.go:37,79` |
| `grpc.KeepaliveParams{Time, Timeout}` | `2 min` / `20 s` | Server pings idle peers; dead peers dropped. | `server.go:38-39,80-81` |
| `grpc.MaxRecvMsgSize` / `MaxSendMsgSize` | `ServerConfig.MaxMessageSize` = `MaxShardSize + MaxPaymentPromiseSize + 2 %` ≈ 132 MiB | Bounds one response; a max-stake shard is 129.83 MiB. | `fibre/server.go:134-135`; `fibre/protocol_params.go:197-223` |
| `ForceServerCodecV2(NewServerCodec(MaxRowsPerValidator, MerkleProofDepth))` | 4096 rows / depth 14 | Rejects oversized *uploads* before decoding. Not relevant to downloads. | `fibre/server.go:137-140` |
| `UploadVerifyWorkers` | `runtime.GOMAXPROCS(0)` | Caps concurrent **upload** verification only. | `fibre/server_config.go:37-38,99` |
| `UnlimitedBudget` / `FullStakeStorageBudget` | 2 TiB full-stake | Caps **upload** disk occupancy only (ADR-029). | `fibre/server_config.go:66-69`; `x/fibre/types/params.go:29-31` |

The server's own comment on why the connection/stream numbers are what they
are:

> "Connection and stream caps bound receive memory: gRPC buffers a full
> UploadShard message (~132 MiB) before the handler runs, so the worst case is
> maxConnections * maxConcurrentStreams * MaxRecvMsgSize (~27 GiB). The values
> are intentionally conservative for a 32 GiB-RAM validator. Tying them to
> staking power, or adding a per-peer connection policy, are possible
> follow-ups." — `fibre/internal/grpc/server.go:19-23`

The spec is explicit that there is no download throttling:

> "The server does not implement per-peer token buckets, throughput caps,
> request backoff hints, or explicit upload/download RPC concurrency limits."
> — `specs/src/fibre_server.md:226`

> "The download path stays unthrottled (out of scope)." — ADR-029, Negative
> consequences, `docs/architecture/adr-029-fibre-upload-rate-limit.md:217`

Only `UploadShard` can return `ResourceExhausted` with a `RetryInfo` hint
(`fibre/server_upload.go:105-106`). `DownloadShard` returns only
`InvalidArgument`, `NotFound`, `Internal` (`fibre/server_download.go:33,41,52,57`;
`specs/src/fibre_server.md:214-222`). **So today a probe can never receive a
"slow down" signal from a fibre server** — the observer has to self-limit.

Operator-tunable knobs on the `fibre start` command are exactly four:
`--app-grpc-address`, `--server-listen-address`, `--signer-grpc-address`,
`--unlimited-budget` (`fibre/cmd/start_cmd.go:69-72`). There is no flag or
TOML key for any connection or bandwidth limit
(`fibre/server_config.go:30-79`).

### 1.2 `DownloadShard` is whole-shard, no row subset

```proto
message DownloadShardRequest {
  bytes blob_id = 1; // const len == 33 (version + commitment)
}
message DownloadShardResponse {
  BlobShard shard = 1;
}
message BlobShard {
  repeated BlobRow rows = 1;
  bytes rlcs = 2; // flattened RLC vector, 16 bytes per original row
}
message BlobRow { uint32 index = 1; bytes data = 2; repeated bytes proof = 3; }
```
— `proto/celestia/fibre/v1/service.proto:9-13,18-21,34-42`

The handler decodes the blob ID, calls `s.store.Get(ctx, id.Commitment())`
and returns whatever it stored, unchanged
(`fibre/server_download.go:46,81-83`). There is no row-range, count, or
sampling parameter. The reference client also always takes the whole shard
(`fibre/client_download.go:162`).

**Consequence:** the smallest possible successful retrievability probe
transfers the validator's full shard for that blob. The only cheaper probes
are (a) a `NotFound`/error response (a few hundred bytes), and (b) the L1–L3
layers (DNS/TCP/TLS handshake, ~2–4 KB) that never call `DownloadShard`.

### 1.3 Bytes per shard (blob v0)

Constants: `Rows = 4096`, `EncodingRatio = 0.25` → `TotalRows = 16384`,
`MinRowSize = 64` (`fibre/protocol_params.go:58-70`; `pkg/rsema1d/field/leopard.go:4-5`);
`MerkleProofDepth = bits.Len(16383) = 14` → 14 × 32 = 448 proof bytes per row
(`protocol_params.go:214-216`); RLC vector = 4096 × 16 = 65,536 bytes per
response regardless of row count (`service.proto:20`); row size =
`ceil(len/4096)` rounded up to a multiple of 64 (`protocol_params.go:173-190`).
Rows per validator = `clamp(ceil(4096·3·f), 148, 4096)` for stake fraction
*f* (observer notes; `MinRowsPerValidator` = 148 from
`protocol_params.go:136-159`, confirmed by `fibre-assign`).

| Blob | Row size | Floor validator (148 rows, ≈1.2 % stake) | 30 % validator (3687 rows) | Max (4096 rows) |
|---|---:|---:|---:|---:|
| 128 MiB (max) | 32,768 B | **4.99 MB** (4.76 MiB) | **122.7 MB** (117 MiB) | 136.3 MB (129.9 MiB; `MaxShardSize` = 129.83 MiB) |
| 1 MiB | 256 B | 0.175 MB | 2.79 MB | 3.10 MB |
| 256 KiB (min) | 64 B | 0.147 MB | 2.09 MB | 2.31 MB |

(Computed as rows × (row + 448 proof + ~8 B framing) + 65,536 RLC + 4;
arithmetic script output in the appendix. For small blobs the fixed 64 KiB RLC
vector and the 448 B/row proofs dominate: a floor validator's 256 KiB shard is
56 % of the whole blob's size.)

### 1.4 What "one ordinary retrieval client" does

From `fibre/client_download.go` and `fibre/internal/grpc/`:

- One lazily-dialled gRPC connection per validator, cached for the client's
  lifetime ("Only one dial per validator", `client_cache.go:61`).
- For one `Download`, one `DownloadShard` per selected validator, all in
  parallel (`client_download.go:231-237`), stopping once enough rows arrive.
- `RPCTimeout` = 15 s per call (`client_config.go:109`).
- On an unreachable/timed-out peer: re-resolve host and retry **once**;
  application errors (including `NotFound`) are **not** retried
  (`client_cache.go:87-119`).
- Options set on dial: TLS, otel stats handler, max msg sizes, custom codec —
  **no `WithUserAgent`, no keepalive** (`fibre_client.go:91-100`).

So per validator, one ordinary read of a blob = exactly one full-shard
download. An observer probe round is indistinguishable from one reader, and the
policy below is expressed in "reader-equivalents".

---

## 2. Public statements

### 2.1 Forum thread 2288 (read in full, 6 Sep 2026)

<https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288>
— category Research, 5 posts, 2–4 Sep 2026.

Utku | Huginn (2 Sep 2026, post 1) asked the question this note answers:

> "`DownloadShard` is public to any client that completes the
> server-authenticated TLS handshake, and the spec notes no per-peer rate
> limiting is currently implemented. Is periodic external probing of registered
> Fibre endpoints (measuring per-validator shard availability, and whether a
> given commitment is still reconstructable) something the core team plans to
> ship, or is it expected to come from operators? If the latter, what probe
> rate would be acceptable given the current lack of rate limiting?"

Rachid CHAMI (rach-id, core, 3 Sep 2026, post 2) — the only core statement on
the subject:

> "We're planning to implement rate limiting for downloads in the next
> iterations. Also, the community is welcome to propose solutions on how to
> effectively rate limite download without impacting honest readers."
>
> "For making sure the data is available, that can be ensured by the security
> assumptions of the protocol. Additionaly, the community will generally build
> dashboards that will:
> - Periodically check whether the provided fibre server addresses, in the
>   x/valaddr module are reachable
> - Whether validators are serving the data
> - etc"

and, on the honesty model:

> "Yes, we rely on an honest majority to serve the data for the specified
> period of time. And by design, we only need 1/3rd of the validator set to be
> honest to be able to retrieve the rows."

Utku (post 3, 4 Sep) put the current probe cost on record ("p50 ~14ms, p95
~24ms … roughly 4-5 points across the window"), argued that "a naive per-peer
limit can't tell a legitimate verifier from abuse", and Rachid (post 4)
replied "cool stuff 👍 … definitely, feel free to write something and we can
review it".

**The acceptable-probe-rate question was asked directly and not answered with a
number.** Nobody in the thread objected to monitoring traffic.

### 2.2 Forum thread 2295 (follow-up write-up, 4 Sep 2026)

<https://forum.celestia.org/t/rate-limiting-fibres-read-path-without-ip-allowlists/2295>
— 1 post, Utku | Huginn, no replies as of 6 Sep 2026. Proposes classifying
download requests by whether the `BlobID` is a live, settled publication and
whether the target validator is assigned, rather than per-IP limiting; says
"NOT_FOUND responses after a publication's must_serve_until deadline shouldn't
penalize peer scores, reflecting expected observer behavior". No core reply
yet.

### 2.3 CIP-51 (read 6 Sep 2026)

<https://cips.celestia.org/cip-051.html> — "Fibre Protocol", status
Implemented, author @rach-id, created 2026-06-30. Parameter table (after fix
#405): `WithdrawalDelay 24h`, `PaymentPromiseTimeout 1h`,
`PaymentPromiseHeightWindow 1000`, `ShardRetention 4h` ("Bounded to
[10m, 168h]"), `FullStakeStorageBudget 2 TiB`.

The only serving-load text is in Security Considerations:

> "Servers MUST enforce message-size limits, proof verification, assignment
> checks, and validator-endorsed TLS identity verification."

Nothing about retrieval rate, serving load, download rate limiting or probe
frequency.

### 2.4 GitHub

- celestiaorg/celestia-app #7553 "Fibre Rate limiter ADR" (rach-id, opened
  16 Jul 2026, closed 30 Jul 2026 as completed): "Given that the rate limiter
  will be part of Fibre even after the ramp up period, we should decide on how
  this limiter would look like: global … shards sizes + staking power … PFF
  sizes + staking power … by client/escrow/user". The resulting ADR-029 covers
  uploads only (§1.1). Seen 6 Sep 2026 via GitHub search
  (<https://github.com/celestiaorg/celestia-app/issues/7553>).
- ADR-029 references "PROTOCO-1547 — rate-limiter tracking issue" (internal
  Linear; not public). `docs/architecture/adr-029-fibre-upload-rate-limit.md:222`.
- ADR-030 (elastic shard storage) lists "Client egress from Fibre servers" as
  explicitly **not** changed (`adr-030-fibre-elastic-shard-storage.md:46`).
- No issue or discussion in celestiaorg/celestia-app or celestiaorg/CIPs
  mentions probe frequency, synthetic download load, or operators objecting to
  monitoring traffic (GitHub issue search `repo:celestiaorg/celestia-app fibre
  download rate limit`, 5 hits, none relevant beyond #7553; web searches on
  forum.celestia.org and docs.celestia.org, 6 Sep 2026, no hits).

### 2.5 The "0.65 Gbps / 1.18 TB floor validator" sizing — UNVERIFIED

I could not find this figure in `celestia-app` (grep of `specs/`, `docs/`,
`fibre/`, release notes for `0.65`, `1.18`, `Gbps`), in the observer repo, on
the forum, in CIP-51, or on docs.celestia.org (whose generic validator page
says only "1 Gbps bandwidth", <https://docs.celestia.org/operate/getting-started/hardware-requirements/>,
seen 6 Sep 2026). It is internally consistent: 0.65 Gbps = 81.25 MB/s, and
81.25 MB/s × 4 h = **1.17 TB**, i.e. the 1.18 TB is the 4-hour `ShardRetention`
window at that ingress rate. I use it below as the *assumed ingress sizing* of
a 148-row validator, scaled linearly by rows for larger validators, and flag
it as an assumption in the config. Egress sizing is nowhere stated; the policy
therefore treats egress capacity as no larger than ingress.

---

## 3. Proposed default probe policy

### 3.1 Probes per (validator, blob)

Keep the existing schedule (`fibre-sentinel/internal/probe/schedule.go:48-60`):
4 in-window points at fractions `{0.12, 0.45, 0.72, 0.92}` of
`[settlement, must_serve_until]`, one grace point at `msu + 30 s`, one post
point at `msu + 150 s + 60 s`. That is **6 requests per (validator, blob)**.

Bytes: the 4 in-window probes and the grace probe (the shard is honestly still
present until `msu + ~1m45s`, per the measured prune lag in
`docs/fibre-operational-notes.md`) each transfer the full shard. The post
probe expects `NotFound` and transfers ~0. So **5 shard-downloads per
(validator, blob)** = 5 reader-equivalents spread over ≈ 4 h 15 min.

Do **not** thin this per blob when under pressure (§4) — a 2-point schedule
cannot distinguish "served all window" from "served when it noticed the
first probe".

Additionally, one **reachability heartbeat per validator** (L1–L3 only, no
`DownloadShard`, ~3 KB, one connection) every 10 min, independent of
publications. This is what makes Rachid's "periodically check whether the
provided fibre server addresses … are reachable" dashboard possible at
near-zero cost, and it keeps the reachable/unreachable signal live on days
when sampling is aggressive or nothing is published.

### 3.2 Every publication, or a sample?

**Every publication, while the budget allows.** When projected load for the
coming hour exceeds any cap in §3.5, blobs are sampled at probability *p* by
the deterministic, unpredictable rule in §4. Sampling is per **blob**, never
per validator: either all assigned validators of a blob are probed on the
full schedule, or none are. That keeps cross-validator comparisons fair (every
validator sees the same sampled set) and keeps the schedule intact.

### 3.3 Bytes per probe

Whole assigned shard (§1.2). Per-probe cost table = §1.3. Per (validator,
blob) over the window: 5 × shard.

| Blob | Floor validator (148 rows) | 30 % validator (3687 rows) |
|---|---:|---:|
| 128 MiB | 5 × 4.99 = **24.9 MB** | 5 × 122.7 = **613 MB** |
| 1 MiB | 0.88 MB | 14.0 MB |
| 256 KiB | 0.73 MB | 10.4 MB |

### 3.4 Load arithmetic

Assumed capacity: floor validator 0.65 Gbps = 81.25 MB/s = 292.5 GB/h
(§2.5, UNVERIFIED); a validator with *r* rows is assumed sized at
`0.65 Gbps × r/148` (30 % → 3687 rows → 16.2 Gbps; the fraction below is
therefore **stake-invariant**, because shard bytes and assumed capacity both
scale with rows).

**Scenario A — 1 blob/min of 128 MiB (60/h):**

| | Floor validator | 30 % validator | Observer total (100 validators, Σ rows ≈ 12,288) |
|---|---:|---:|---:|
| Requests | 60 × 6 = 360/h = **6/min** | 6/min | 36,000/h |
| Bytes | 60 × 5 × 4.99 MB = **1.50 GB/h** = 3.3 Mbps | 60 × 5 × 122.7 MB = 36.8 GB/h = 82 Mbps | 60 × 5 × 415 MB = **125 GB/h** = 277 Mbps; 3.0 TB/day |
| Share of assumed capacity | 3.3 / 650 = **0.51 %** | 82 / 16,190 = **0.51 %** | — |
| Reader-equivalents | 5 readers of every blob, per vantage | same | ≈ 15 full reads of every blob |

**Scenario B — 10 blobs/min of 1 MiB (600/h):**

| | Floor validator | 30 % validator | Observer total |
|---|---:|---:|---:|
| Requests | 600 × 6 = 3,600/h = **60/min** | 60/min | 360,000/h |
| Bytes | 600 × 5 × 0.175 MB = **0.53 GB/h** = 1.2 Mbps | 600 × 5 × 2.79 MB = 8.4 GB/h = 19 Mbps | 600 × 5 × 15.6 MB = **47 GB/h** = 104 Mbps |
| Share of assumed capacity | **0.18 %** | 0.12 % | — |

(Floor share is higher than the 30 % validator's for small blobs because the
fixed 64 KiB RLC vector is a large part of a 148-row shard.)

**Worst case — the rate the hardware was sized for.** If 0.65 Gbps is the
floor validator's *ingress* sizing, the network was sized for ≈ 81.25 MB/s ÷
0.0372 (floor shard as a fraction of blob bytes, §1.3) ≈ **2.2 GB/s of blob
publication** (≈ 16 × 128 MiB blobs per second). At that rate a full-schedule
observer would pull 5 × 81.25 MB/s = 406 MB/s from the floor validator —
**500 % of its capacity**. To stay at 1 % the sample rate must be
*p* = 0.01 / 5 = **0.2 %**. This is why §4 sampling is a first-class part of
the design, not a fallback: at any publication rate above ≈ 0.2 % of the
network's sized ingest (≈ 4.4 MB/s ≈ 2 × 128 MiB/min), a 5-download schedule
on every blob would exceed a 1 % budget.

**Expected case (near-term mainnet ramp-up, Scenario A-ish):** 0.5 % of a
validator's assumed capacity, 6 requests/min, one connection at a time. This
is well inside "a small single-digit percentage or less".

### 3.5 Hard caps (defaults)

| Cap | Default | Derivation |
|---|---|---|
| Per-validator concurrency | **1** in-flight request, **1** open connection | Never hold more than 1 of the server's 16 listener slots (§1.1). Close the connection after each probe; never keep idle connections (server closes them at 5 min anyway). Never send keepalive pings (server rejects pings < 10 s apart). |
| Min spacing between requests to one validator | **2 s** | Avoids back-to-back handshakes when several blobs' points coincide. |
| Max requests per validator per minute | **30** | Scenario A uses 6; Scenario B (60) triggers *p* = 0.5. Chosen because each probe currently costs **two** TCP+TLS handshakes (raw TLS at `probe.go:178-216`, then a fresh gRPC dial at `probe.go:268`); raise to 60 once the probe reuses one connection for L3+L4. |
| Max bytes per validator per hour | **1 % of assumed capacity × rows/148** → floor **2.93 GB/h**; 30 % validator 72.9 GB/h; 4096-row validator 81 GB/h | 0.01 × 292.5 GB/h. Scenario A floor uses 1.50 GB/h (51 % of cap). |
| Max bytes per validator per day | **0.75 % average** → floor **52.7 GB/day**, scaled by rows | 0.0075 × 81.25 MB/s × 86,400 s. Scenario A floor uses 35.9 GB/day (68 %). |
| Max global egress (observer ingress) per hour, per vantage | **50 GB/h** (≈ 111 Mbps) | Observer-side cost knob. Scenario A needs 125 GB/h → *p* = 0.40; Scenario B needs 47 GB/h → fits. |
| Max global per day, per vantage | **600 GB/day** | 12 × hourly, forces a lower sustained average. |
| Global concurrency | **8** in-flight probes across all validators | Keeps the observer's own NIC and CPU (proof verification) bounded. |
| Vantages | ≤ **3**, each with its own budget; the per-validator caps above are **per vantage**, so 3 vantages = 1.5 % hourly per validator worst case | More vantages multiply load; add them by lowering per-vantage caps, not by adding budget. |

The per-validator caps are the ones that protect operators; the global caps
protect the observer's bill. In Scenario A the **global** cap binds first
(*p* = 0.40) while every validator is at half its hourly cap; in Scenario B
the **request** cap binds (*p* = 0.5). Both are shown as NOT_PROBED gaps on
the dashboard (§4).

### 3.6 Backoff on failure

Principle: **never add requests because of a failure.** The schedule is fixed;
a failed point is recorded and the next point comes when it comes. Backoff
only ever *removes* the expensive L4 step or *stretches* the reachability
heartbeat. Failures are tracked per validator (across blobs), with *k* =
consecutive failures of that class.

| Outcome (existing taxonomy, `probe.go:357-387`) | Cost to validator | Immediate retry | Effect on scheduled points | Heartbeat interval | Cap | Reset |
|---|---|---|---|---|---|---|
| DNS fail, TCP refused, no route | ~0 (one SYN) | none | keep probing every point (cheap, and each is evidence) | 10 min × 2^k | 60 min | first success |
| TCP / TLS **timeout** (black-hole) | ~0 for validator, 5–10 s socket for observer | none | keep L1–L3 at every point; **skip L4** while k ≥ 3 (record `PROBE_ERROR:backoff`) | 10 min × 2^k | 60 min | first L3 success |
| gRPC `Unavailable` / `Internal` from a reachable server | one request | none | after k ≥ 2, skip L4 on points closer than 2^k × 5 min to the last failure | unchanged | 8× (40 min) | first `SERVED_OK` or `NotFound` |
| `DeadlineExceeded` on the download (slow server) | a partial shard | none | after k ≥ 2, skip L4 on points closer than 2^k × 5 min; log as `RPC_ERROR:slow` | unchanged | 8× | first `SERVED_OK` |
| `ResourceExhausted` (future download limiter) or any `RetryInfo` | one request | none; honour `RetryInfo`, capped at **2 min** exactly like the reference client (`client_upload.go:395-400`) | after 3 in a row, **halve** that validator's hourly byte and request caps for 1 h | unchanged | — | 1 h |
| `NotFound` in-window (a FAULT) | one request | none | none — this is the signal we exist to record | — | — | — |
| Identity failure (L3b) | handshake | none | keep L1–L3; skip L4 (there is nothing trustworthy to download) | 10 min × 2^k | 60 min | verified cert |

"Skip L4" points are written with outcome `PROBE_ERROR` and reason
`backoff:<class>:k=<k>` so the dashboard shows an observer-side gap, not a
validator fault.

---

## 4. Degradation: how sampling works

1. **Projection.** Every 5 min, compute for the next hour, from the trailing
   hour's publication rate and size mix: per-validator bytes, per-validator
   requests, global bytes. Compute `p = min(1, cap / projected)` over all caps
   in §3.5 (per-validator caps use the most constrained validator).
2. **Deterministic, unpredictable selection.** A publication is probed iff
   `uint64_be(SHA256(promise_hash || day_secret)[0:8]) < p × 2^64`,
   with `day_secret = HMAC-SHA256(master_secret, "YYYY-MM-DD")`. `promise_hash`
   is the on-chain identity already recorded by `sentinel-scan`
   (`fibre-sentinel/README.md`, record table). A validator cannot predict which
   blobs will be probed (it does not know `day_secret`), cannot influence the
   choice (it does not control `promise_hash`, which is set by the uploader
   before any validator signs), and cannot serve "only when watched" without
   serving everything.
3. **Auditability.** Publish `SHA256(day_secret)` before the day starts and
   `day_secret` after the day ends (commit–reveal). Anyone can then recompute
   the sample and verify it was unbiased. Record `p`, the day-secret
   commitment and the sampling decision in every publication's row.
4. **Whole schedule or nothing.** A sampled-in blob gets all 6 points on all
   assigned validators. A sampled-out blob gets **no** `DownloadShard` at all.
   Per-blob thinning is prohibited (§3.1).
5. **Record the gap.** Sampled-out blobs are written once per assigned
   validator as `NOT_PROBED` with reason `budget:p=<p>:<binding cap>` — the
   taxonomy already has `NOT_PROBED` (`fibre-sentinel/README.md`, error-class
   table), so the dashboard shows coverage honestly (e.g. "probed 40 % of
   publications this hour; cap: global_bytes_per_hour").
6. **Sticky p.** Once a blob is sampled in, it stays in even if `p` drops later
   in its window; `p` changes only apply to blobs settled after the change.
   Otherwise late points would be silently dropped.
7. **Priority when p < 1.** Nothing is prioritised by size or validator —
   doing so would bias the sample. The one allowed exception is a manual
   `always_probe` list of promise hashes (e.g. a blob under dispute), recorded
   as such.

---

## 5. Etiquette

- **Identify yourself on the wire.** grpc-go supports
  `grpc.WithUserAgent(s)`, which sends `user-agent: <s> grpc-go/<ver>` on every
  RPC (grpc-go v1.83.2 `dialoptions.go:563-568`, module cache). Set
  `fibre-sentinel/<version> (+https://<policy-url>; vantage=<name>)`, and add
  outgoing metadata `x-fibre-observer: <policy-url>`. Caveat: the fibre server
  does not log the user agent today (`server_download.go:74-78` logs only
  commitment, rows, row_size), so operators will only see it with a debug
  proxy. Worth a small upstream PR (log `user-agent` at debug level in
  `DownloadShard`) — it also gives core a cheap "known observer" signal for the
  download limiter they are planning.
- **Publish before activating.** Post this policy (caps, schedule, vantage IPs,
  UA string, opt-out) as a reply in forum thread 2288 or 2295 at least a week
  before probing mainnet; core explicitly invited such write-ups (post 4,
  thread 2288). Keep a versioned copy at the policy URL.
- **Contact / opt-out page.** One page listing: what the observer does, the
  exact per-validator caps, the vantage source IPs (so an operator can
  firewall them — that is a legitimate choice and should be recorded as
  `OPTED_OUT`, not `FAULT`), an email and a form. Opt-out → the validator is
  moved to **reachability-only** (L1–L3 heartbeat, no `DownloadShard`) and the
  dashboard says "operator opted out of retrievability probes on <date>".
- **What to say to an operator who complains** (keep it to this): "We probe
  your registered Fibre endpoint exactly as a reader would: one connection at
  a time, ≤ N requests/min, ≤ X GB/h, only for blobs you were assigned on
  chain, only during their retention window. In the last 24 h that was
  <actual bytes> and <actual requests> from <IPs>. Here is the policy and the
  per-validator log. If you want us to stop downloading, we will switch you to
  reachability-only today and say so publicly on the dashboard." Then do it.
  Never argue that the load is "small" without the numbers, and never keep
  probing a validator that has asked you to stop.
- **Never exceed a reader.** Never open more than one connection to a
  validator, never issue parallel `DownloadShard`s to it, never retry a
  `NotFound`, never ping faster than 10 s. If core ships a download limiter
  that returns `ResourceExhausted`/`RetryInfo`, honour it like the reference
  client does.

---

## 6. Config sketch (YAML)

```yaml
# fibre-sentinel probe policy — defaults from R4 (6 Sep 2026).
# All per-validator numbers are PER VANTAGE. Run <= 3 vantages.
schema_version: 1

identity:
  user_agent: "fibre-sentinel/0.3 (+https://example.org/fibre-observer; vantage=eu1)"  # sent via grpc.WithUserAgent
  metadata_header: "x-fibre-observer"        # value = policy_url, on every RPC
  policy_url: "https://example.org/fibre-observer"
  vantage: "eu1"

schedule:                                     # unchanged from schedule.go defaults
  in_window_fractions: [0.12, 0.45, 0.72, 0.92]  # x^0.7-style, packed toward the deadline
  grace_offset: 30s                           # must_serve_until + this (shard still present; full bytes)
  prune_tolerance: 150s                       # NOT_FOUND tolerated until msu + this (measured lag ~1m45s)
  post_margin: 60s                            # post probe at msu + prune_tolerance + this (expects NOT_FOUND)
  min_spacing: 20s
  thin_per_blob_under_pressure: false         # MUST stay false; degrade by sampling blobs, never by dropping points

heartbeat:
  enabled: true
  interval: 10m                               # L1-L3 only (DNS/TCP/TLS+identity), no DownloadShard, ~3 KB
  max_interval: 60m                           # backoff cap while a validator is unreachable

capacity_model:                               # ASSUMPTION — 0.65 Gbps / 1.18 TB per 4h for a 148-row validator is UNVERIFIED (R4 §2.5)
  floor_rows: 148
  floor_validator_bps: 650000000              # 0.65 Gbps ingress sizing, treated as an upper bound on egress too
  scale_with_rows: true                       # cap(rows) = cap(148) * rows / 148

caps:
  per_validator:
    concurrency: 1                            # one in-flight request and one open connection, ever
    min_request_spacing: 2s
    requests_per_minute: 30                   # raise to 60 once L3+L4 share one connection
    bytes_per_hour_fraction: 0.01             # of capacity_model -> 2.93 GB/h at 148 rows
    bytes_per_day_fraction: 0.0075            # -> 52.7 GB/day at 148 rows
    close_connection_after_probe: true
    keepalive_pings: false                    # server enforces >= 10 s between pings; we send none
  global:
    concurrency: 8
    bytes_per_hour: 50GB                      # observer-side cost knob; binds first in the 128 MiB/min scenario (p ~ 0.40)
    bytes_per_day: 600GB
    requests_per_second: 20

sampling:
  mode: deterministic                         # probe iff H(promise_hash || day_secret) < p * 2^64
  master_secret_file: /etc/fibre-sentinel/master.secret
  day_secret_derivation: "HMAC-SHA256(master, YYYY-MM-DD)"
  publish_commitment_ahead: 24h               # SHA256(day_secret) published before the day
  reveal_after: 24h                           # day_secret published after the day, so the sample is auditable
  projection_interval: 5m
  projection_lookback: 1h
  sticky_per_blob: true                       # once sampled in, all points run even if p drops later
  record_unsampled_as: NOT_PROBED             # reason "budget:p=<p>:<binding_cap>"
  always_probe: []                            # promise hashes exempt from sampling (recorded as such)

backoff:                                      # consecutive failures k are per validator, across blobs
  unreachable:        {retry: none, skip_l4_after: never, heartbeat_multiplier: 2, heartbeat_cap: 60m}
  timeout:            {retry: none, skip_l4_after: 3,     heartbeat_multiplier: 2, heartbeat_cap: 60m}
  rpc_unavailable:    {retry: none, skip_l4_after: 2, skip_window_base: 5m, multiplier: 2, cap: 40m}
  download_timeout:   {retry: none, skip_l4_after: 2, skip_window_base: 5m, multiplier: 2, cap: 40m}
  resource_exhausted: {retry: none, honour_retry_info: true, retry_info_cap: 2m, halve_caps_after: 3, halve_for: 1h}
  not_found:          {retry: none}           # in-window NOT_FOUND is the signal; never retried
  identity_fail:      {retry: none, skip_l4: always, heartbeat_multiplier: 2, heartbeat_cap: 60m}

timeouts:                                     # unchanged from probe.go DefaultStepTimeouts
  dns: 5s
  tcp: 5s
  tls: 10s
  identity: 2s
  download: 25s

opt_out:
  file: /etc/fibre-sentinel/opt-out.yaml      # list of validator addresses -> {since, contact, mode: reachability_only}
  mode_for_opted_out: reachability_only       # never DownloadShard; dashboard shows OPTED_OUT, not FAULT
```

---

## 7. Open items / unknowns

1. **0.65 Gbps / 1.18 TB provenance** — UNVERIFIED (§2.5). If core's number
   is egress rather than ingress, or differs, only `capacity_model` changes;
   the fractions in §3.4 scale linearly.
2. **Real validator count and stake distribution** at Fibre launch — the global
   totals assume 100 validators and Σ rows ≈ 12,288 (`3 × 4096`, matching the
   devnet's zero-overlap assignment in `docs/fibre-operational-notes.md`).
3. **Whether the planned download limiter will be per-IP** — if so, the
   observer's fixed vantage IPs are trivially throttled and the policy URL /
   user agent is the only way to ask for an allowance. Thread 2295 argues for
   a request-classification approach instead; no core reply as of 6 Sep 2026.
4. **Connection reuse in the probe** — the current probe opens two TCP+TLS
   connections per point (§3.5). Collapsing to one halves handshake load and
   is the precondition for raising `requests_per_minute`.
5. **WAN probe duration** — the 14–24 ms figures are localhost
   (`docs/fibre-operational-notes.md`). Over a WAN a 122 MB shard from a 30 %
   validator at, say, 200 Mbps takes ~5 s, during which one of the server's 16
   connection slots is occupied; the 25 s download timeout bounds it.

---

## Appendix — arithmetic script output

```
MerkleProofDepth 14 proof bytes/row 448
--- 128 MiB: row size 32768 B
  floor 1.2%       rows=  148 shard=4,986,836 B = 4.987 MB = 4.76 MiB ; blob fraction 0.0372
  30%              rows= 3687 shard=122,665,664 B = 122.666 MB = 116.98 MiB ; blob fraction 0.9139
  max 4096 rows    rows= 4096 shard=136,265,732 B = 136.266 MB = 129.95 MiB ; blob fraction 1.0153
--- 1 MiB: row size 256 B
  floor 1.2%       rows=  148 shard=175,060 B = 0.175 MB
  30%              rows= 3687 shard=2,793,920 B = 2.794 MB
  max 4096 rows    rows= 4096 shard=3,096,580 B = 3.097 MB
--- 256 KiB: row size 64 B
  floor 1.2%       rows=  148 shard=146,644 B = 0.147 MB
  30%              rows= 3687 shard=2,086,016 B = 2.086 MB
  max 4096 rows    rows= 4096 shard=2,310,148 B = 2.310 MB
MaxShardSize formula (protocol_params.go:199-210): 136,134,656 B = 129.83 MiB
floor capacity 0.65 Gbps = 81.25 MB/s = 292.5 GB/h = 1.17 TB per 4 h
```
Shard bytes = rows × (row + 448 + ~8 framing) + 65,536 RLC + 4; rows(f) =
clamp(ceil(4096·3·f), 148, 4096); row size = ceil(blob/4096) rounded up to 64.
