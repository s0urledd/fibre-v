# R13 — Measuring the upload side: a synthetic publisher on Mocha

*Research note and design, 24 September 2026. Source facts are from
celestia-app at the pinned commit `3b77dc2f5b00e1a646a2e9dd98b5c024a0d9ad8a`
(`fibre/client_upload.go`, `fibre/server_upload.go`, `fibre/server_metrics.go`,
`fibre/client_metrics.go`, `x/fibre/types`, `pkg/appconsts/fibre_gas_consts.go`)
and this repository. Status: design plus non-invasive groundwork
(`internal/uploadprobe`, `sentinel-pub -upload-results`, off by default).
Nothing here uploads, settles or broadcasts anything; enabling it is the
owner's decision (§8).*

---

## 0. Summary

| Question | Answer |
|---|---|
| What does the observer not see today? | Only downloads. Whether a validator *accepts* an upload, how fast, and why it refuses one are invisible, except for one bit per real blob: did its signature land on the settled promise (item 18, `assignments.attested` over assigned promises). |
| What would a synthetic publisher add? | Per validator and per upload: accepted or not, the **reason** for a refusal (`budget_exceeded`, deadline, connection, rejected promise/shard, server error), and **upload latency**. And a signal when real traffic is too thin for the signing rate to mean anything. |
| Where do per-validator results come from? | Not from `Client.Upload`'s return value (the signed promise only says who signed before two-thirds). They come from the reference client's own OpenTelemetry spans: one `upload_to` span per validator with the final gRPC error recorded on it. A span processor reads them without forking the client (§2). Implemented: `internal/uploadprobe`. |
| Cost | Fibre's escrow charge, `650000 + 45000 × ceil(size / 262144)` utia at 1 utia/gas: **695,000 utia (0.695 TIA) per blob up to 256 KiB**, plus ≤ 8,000 utia of tx gas to settle. One blob an hour is **16.9 TIA/day, 118 TIA/week**. An abandoned promise costs the same once anyone submits its timeout (§3.2). |
| Recommended mode | **Adaptive**, not a fixed schedule: burn-in for 1–2 weeks after activation (hourly), then steady state that runs only when real traffic is thin, plus targeted diagnostic uploads when a validator's signing rate drops (§4). |
| Funds | `tensile-ops` (`celestia1jw8afsj3j0c23fxs09nu8pq5asxwes5e3kkxdx`) holds 10.02 TIA today: 14 blobs. The owner plans ~2,800 TIA (testnet), which covers burn-in (≤ 236 TIA) and months of steady state (§3.3). |
| Is any result a fault? | **No.** Upload results are published beside the download verdicts, never in the serve rate, and every reason is worded as what happened, not as blame (§5). |

---

## 1. What the protocol exposes about an upload

### 1.1 Server side (`fibre/server_upload.go`)

`UploadShard` runs, in order: promise verification (`InvalidArgument
"payment promise verification failed: …"`), a nil-shard check
(`InvalidArgument`), assignment verification (`InvalidArgument "shard
assignment verification failed: …"`), row-proof verification
(`InvalidArgument "shard verification failed: …"`), then, under a per-promise
lock, `store.Has`; if the shard is new, the storage limiter:

```go
reserved := s.occ.reserve(size)
if !reserved {
    s.metrics.uploadShardRejected.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "budget_exceeded")))
    st := status.New(grpccodes.ResourceExhausted, "fibre storage budget exceeded")
    st, _ = st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(retryAfterHint())})
    return nil, st.Err()
}
```

`retryAfterHint()` is one prune interval (1 minute) plus up to half an
interval of jitter. A failed store write is `Internal`; a client cancellation
mid-write is `Canceled`/`DeadlineExceeded`. On success the server signs the
promise and returns the signature.

`fibre/server_metrics.go` exports, **on the validator's own OTLP endpoint**,
`fibre.server.upload_shard.{in_flight,duration,bytes,rejected{reason}}` and
the limiter's `occupancy_bytes` / `budget_bytes` gauges. Those are the
operator's view; no client can read them. `budget_exceeded` is the only
rejection reason the server labels.

### 1.2 Client side (`fibre/client_upload.go`)

`Client.Upload` builds and signs the promise, then `uploadShards` fans out one
goroutine per validator (`uploadTo`) and returns as soon as the collected
signatures reach the safety threshold; **the remaining deliveries continue in
the background** until they finish or the client stops. `uploadTo`:

- opens a span `upload_to` with `validator_address` and `rows_count`;
- retries only on `ResourceExhausted`, up to `maxUploadRetries = 3`, waiting
  the server's `RetryInfo` delay capped at 2 minutes;
- on final failure, `span.RecordError(err)` and `SetStatus(Error, "failed to
  upload rows")`, then returns;
- on success, event `rows_uploaded`, then parses and adds the signature
  (a bad signature is also recorded on the span).

`fibre/client_metrics.go` records `fibre.client.upload_to.duration` and
`upload_to.rpc_latency` with `validator_address` and `success` attributes,
per attempt for the latter. The caller gets none of this in return values:
`SignedPaymentPromise.ValidatorSignatures` is positional over the validator
set, with empty entries for validators that had not signed when quorum was
reached — which conflates "refused", "slow" and "not needed".

### 1.3 What that means

A client can learn, per validator, per upload: accepted or not, the final
error text (hence the gRPC code and the budget message), the total delivery
time including retries, the row count, and whether the answer came before or
after quorum. It cannot learn the server's occupancy or budget, and it sees
only the final attempt's error (a `ResourceExhausted` that succeeded on the
second try shows as a success with a longer duration; the per-attempt RPC
metric could recover it, not implemented).

---

## 2. Groundwork implemented (off by default)

`internal/uploadprobe` — a `sdktrace.SpanProcessor` (`Recorder`) that turns
the client's `upload_to` spans into one `Result` per (upload, validator):

```json
{"schema_version":1,"vantage":"…","labels":{"promise_hash":"…","commitment":"…","blob_size":"131072"},
 "validator_address":"<hex>","rows":148,"started_at":"…","finished_at":"…","duration_ms":412,
 "delivered":true,"signed":true,"reason":"accepted"}
```

`reason` is one of `accepted`, `budget_exceeded`, `resource_exhausted`,
`rejected` (InvalidArgument), `unavailable`, `deadline`, `canceled` (the
client's own decision), `server_error` (Internal), `bad_signature`, `other`.
`Classify` is tested against error texts built with the same
`status.Error` calls the server makes; the recorder is tested against spans
shaped exactly as `uploadTo` shapes them.

`sentinel-pub -upload-results FILE` installs the recorder as the fibre
client's tracer, labels each upload's trace with its promise hash, waits
`-upload-results-grace` (30 s) for deliveries past quorum, stops the client
and writes the JSONL. It is a devnet tool; the flag is empty by default and
nothing else changes when it is.

Not implemented: a daemon, any Mocha run, collector ingestion, an API route
or a UI. Those are phases 2–4 (§7).

---

## 3. Cost

### 3.1 Per blob

| item | utia | source |
|---|---|---|
| escrow payment, blob ≤ 256 KiB | 650,000 + 45,000 × 1 = **695,000** | `x/fibre/types.EstimateGasForPayForFibre`, charged at 1 utia/gas (`PaymentAmount`) |
| escrow payment, 1 MiB | 650,000 + 45,000 × 4 = 830,000 | same |
| escrow payment, 8 MiB | 650,000 + 45,000 × 32 = 2,090,000 | same |
| settle tx (`MsgPayForFibre`) | ≤ 8,000 (sentinel-pub's 400,000 gas × 0.02); ~1,600 at the 0.004 min gas price | `pkg/appconsts` `PFFibreTxGasFixedCost 1378 + 1000/signature` ante gas plus execution |
| escrow deposit / top-up tx | ≤ 4,000 per deposit | 200,000 gas × 0.02 |

The fixed 650,000 dominates: a synthetic blob should be **one chunk
(≤ 256 KiB; default 128 KiB)**. Larger blobs cost more and load validators
more, and measure nothing an upload of one chunk does not — except
throughput, which the download probes already measure.

**Sampling fewer validators does not save fees.** The reference client
uploads to every validator in the set (the minimum-rows floor assigns every
bonded validator rows of every blob), and the payment depends only on blob
size. Load per validator is what sampling would reduce, and at one 128 KiB
blob per round it is already negligible (a few hundred KiB of rows and
proofs per validator per round). Fewer *rounds* is the only lever on cost.

### 3.2 An abandoned promise

Uploading and never settling (`sentinel-pub -abandon`) does not save
anything. Once `creation + payment_promise_timeout` (1 h by default) has
passed, **anyone** may submit `MsgPaymentPromiseTimeout`, and the chain then
charges the promise signer's escrow the same `PaymentAmount` — 695,000 utia
for a one-chunk blob — while the submitter pays its own tx gas and receives
nothing (`cmd/sentinel-pub` `-timeout`; the validator table's `timeouts_enforced`
tracks which validators do this). If nobody submits it the escrow is not
charged, but then validators stored the shards for free, which is precisely
the abuse the timeout exists to prevent: the synthetic publisher must budget
every upload as paid, and should **always settle**. Settling also turns the
synthetic blob into an ordinary publication that the download prober then
checks end to end, for free.

(The escrow reservation for a promise is held until it is settled or timed
out; `withdrawal_delay` (24 h) is longer than the maximum promise timeout, so
escrow cannot be withdrawn from under a pending promise.)

### 3.3 Per mode (at one 128 KiB blob per round, 703,000 utia with gas)

| mode | rounds | per day | per week |
|---|---|---|---|
| burn-in, hourly | 24/day | **16.9 TIA** | 118 TIA |
| burn-in, 2 weeks total | 336 | — | **236 TIA** for the whole burn-in |
| steady state, runs only in thin hours | 24 × (share of thin hours) | 4.2 TIA at 25% thin, 8.4 at 50%, ≤ 16.9 | 30 / 59 / ≤ 118 TIA |
| targeted, capped at 6/day | ≤ 6/day | ≤ 4.2 TIA | ≤ 30 TIA |
| worst case, steady + targeted at their caps | 30/day | 21.1 TIA | 148 TIA |

With the 10.02 TIA funded today: 14 blobs. With the planned ~2,800 TIA:
burn-in (236) leaves ~2,560 TIA, which is ≥ 121 days at the worst case
above, ~500 days at 25% thin hours and the targeted cap.

### 3.4 Escrow top-up

The fibre client reserves `PaymentAmount` in escrow per upload. Keep escrow
≥ two days of the mode's cap (≈ 42 TIA at the worst case) and top up to seven
days (≈ 150 TIA) when it falls below; the account keeps ≥ 1 TIA liquid for
tx gas. Each top-up is one `MsgDepositToEscrow`. The prober refuses to start a
round when escrow is below one round's reservation, and logs it; it never
deposits more than `-max-topup` in a day.

---

## 4. Modes (agreed direction: adaptive)

Item 18's signing participation already measures upload-side acceptance on
every real blob for free: a validator that refused or missed the upload has
no signature on the settled promise (though "not needed after quorum" looks
the same). Synthetic uploads earn their cost only where that signal is
missing or cannot say *why*. Hence three modes, each off by default and each
with a hard cap.

### 4.1 Burn-in

For the first 1–2 weeks after activation, one synthetic blob per hour.
Setup mistakes (wrong host registered, firewall, TLS certificate not endorsed,
budget misconfigured) cluster here, real traffic is thin, and the reason for a
refusal is what an operator needs to fix it.

Cost: 16.9 TIA/day; 236 TIA for two weeks.

Knobs: `-mode burnin`, `-burnin-until <date>` (required; the mode ends by
itself), `-interval 1h`.

### 4.2 Steady state

Each round (hourly), skip if the last hour already had at least N real
settled `MsgPayForFibre` from other publishers (read from the collector's
`publications` table: signer ≠ our account). Only when real traffic is thin
does the synthetic blob run. This keeps the signing-rate signal (item 18)
meaningful when publishers go quiet, and adds reasons and latency, without
paying for what real publishers already exercise.

Cost: 0.70 TIA per round actually run; ≤ 16.9 TIA/day; e.g. 4.2 TIA/day if a
quarter of hours are thin.

Knobs: `-mode steady`, `-interval 1h`, `-min-real-per-hour N` (default 3),
`-max-blobs-per-day 24`.

### 4.3 Targeted

When a validator's signing rate over the last 6 h drops by more than a
threshold against its own 7-day rate (with enough promises behind both), run
one diagnostic upload and record that validator's result and reason. The
upload still goes to the whole set (§3.1); the client is run with
`WithAwaitAllSignatures` or the recorder's grace period so the targeted
validator's result is captured even past quorum.

Cost: 0.70 TIA per diagnostic; capped at `-targeted-max-per-day` (default 6,
≤ 4.2 TIA/day) and `-targeted-cooldown` (default 6 h per validator), so a
validator that stays down costs at most 4 diagnostics a day and the set as a
whole at most 6.

Knobs: `-mode targeted` (combinable with steady), `-targeted-drop 0.2`,
`-targeted-min-promises 20`, `-targeted-cooldown 6h`,
`-targeted-max-per-day 6`.

### 4.4 Common knobs and guards (all off/defensive by default)

```yaml
upload_probe:
  enabled: false            # nothing runs unless true
  dry_run: true             # decide rounds, log the would-be spend, upload nothing
  modes: []                 # any of: burnin, steady, targeted
  chain_id: mocha-4         # refuses to start on any other chain id
  keyring_backend: test
  keyring_dir: /etc/fibre-observer/keyring-mocha
  key_name: tensile-ops
  namespace_id: tensileupl  # dedicated, so synthetic blobs are recognisable
  blob_bytes: 131072        # one chunk
  max_blobs_per_day: 24
  max_utia_per_day: 17000000
  escrow_min_utia: 42000000
  escrow_topup_utia: 150000000
  max_topup_utia_per_day: 150000000
  results: <data-dir>/upload_results.jsonl
```

Hard stops, in code rather than config: refuse any chain id but `mocha-4`
(or an explicit devnet id); stop for the day at `max_utia_per_day` counting
settled *and* pending promises; stop entirely after N consecutive settle
failures; a kill switch file in the data directory.

---

## 5. Storage, classification and etiquette

- **Separate record.** `upload_results.jsonl`, one line per (upload,
  validator) in the `uploadprobe.Result` schema, appended like every other
  record file, in the daily export, and a collector table
  `upload_probes`. Never mixed into `measurements.jsonl`.
- **Never a fault.** No upload outcome enters the serve rate, the broken
  count or any ranking. The API would publish per validator: uploads
  offered, accepted, the reason breakdown and latency percentiles, over the
  same windows, under the heading "upload acceptance (synthetic, one
  location)". `budget_exceeded` is the protocol's storage limiter doing its
  job and is shown as capacity information; `rejected` is usually a chain-view
  disagreement (the server a block behind) and is shown with its message;
  `canceled` is the client's own decision and is excluded from every figure.
- **Identifiable traffic (R4 §5).** Uploads leave from the published egress
  addresses; the signer (`tensile-ops`) and the namespace (`tensileupl`) are
  published on `/v1/meta` and in the publisher label registry, so operators
  can recognise synthetic blobs in their logs and the market pages can
  exclude them from publisher statistics. The configuration of every run
  goes into `runs.jsonl` as for every other component.
- **Load is bounded by construction**: one 128 KiB blob per round, rounds at
  most hourly plus ≤ 6 targeted a day. A validator receives a few hundred KiB
  per round — orders of magnitude under R4's per-validator download caps.
- **One vantage**: latency and connection failures are statements about a
  path, not a server, exactly as for downloads.

## 6. Risks

- **It adds load and spends funds.** Bounded as above; testnet only. Never
  mainnet without a separate decision.
- **Key custody.** The `test` keyring backend is an unencrypted file on the
  observer host (`/etc/fibre-observer/keyring-mocha`, owned by
  `fibre-observer`). Acceptable for testnet funds; the account should hold
  no more than a few weeks of spend beyond escrow.
- **A runaway loop.** The daily utia cap counts pending promises, not just
  settled ones; the kill switch and consecutive-failure stop are in code.
- **Self-measurement.** Synthetic blobs are probed by the download prober
  like any other. That is useful (an end-to-end check with a known
  publisher) but they must be labelled so they can be separated in every
  publisher figure.
- **Misreading the signal.** A missing signature after quorum is "not
  needed", not "refused"; only the recorder's per-validator result says
  which, and only for synthetic uploads. The site must keep the two apart.

## 7. Phased plan

| phase | what | spends |
|---|---|---|
| 0 (this change) | `internal/uploadprobe` schema + recorder, `sentinel-pub -upload-results` (off by default), this note | nothing |
| 1 | devnet (`fibre-devnet`): run `sentinel-pub -upload-results`, force `budget_exceeded` with a tiny `full_stake_storage_budget`, stop one validator for `unavailable`, check every reason is produced and labelled | devnet only |
| 2 | `sentinel-uploadprobe` (or `sentinel-pub -loop`) with the §4 mode logic and caps, **dry-run on Mocha**: reads the collector's store, decides rounds, logs the would-be spend; no upload | nothing |
| 3 | owner funds escrow and turns on burn-in on Mocha for 1–2 weeks; collector ingests `upload_results.jsonl` | ≤ 236 TIA |
| 4 | steady + targeted; API route and a validator-page panel "upload acceptance (synthetic)" | ≤ 21 TIA/day worst case |

## 8. What the owner must provide to enable it

- The decision to spend testnet funds, and the modes to enable.
- Escrow funding for `tensile-ops`: ≥ 42 TIA deposited to escrow plus ≥ 1 TIA
  liquid for gas before burn-in (the planned ~2,800 TIA covers every phase).
- Confirmation of the namespace (`tensileupl`) and that the signer and
  namespace may be published.
- A Mocha app gRPC endpoint for the fibre client's state queries (the local
  node the observer already follows).
