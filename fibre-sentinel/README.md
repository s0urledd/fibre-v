# fibre-sentinel

An **independent observer** for Celestia Fibre. It watches the chain for Fibre
publications and then, from the outside, checks whether the assigned validators
actually keep serving each blob for its retention window.

```
git clone https://github.com/plsgiveup/fibre && cd fibre/fibre-sentinel
go build -o bin/ ./cmd/...
```

(`fibre-sentinel` depends on the sibling `fibre-assign` and `fibre-tlsverify`
modules via in-repo `replace` directives, so it builds from a repo checkout, not
via `go install <path>@latest`.)

Two programs:

- **`sentinel-scan`** — discovery + recording. Walks the chain over one CometBFT
  RPC endpoint, finds every publication (a transaction whose sole message is
  `MsgPayForFibre`), decodes the full `PaymentPromise`, and writes one record
  per publication to `publications.jsonl`. Never contacts a validator.
- **`sentinel-probe`** — measurement. Turns each record into a probe schedule
  over that blob's retention window and appends one raw `Measurement` per probe
  to `measurements.jsonl`, classified against a fault taxonomy.

---

## Why this tool exists

**Fibre in one paragraph.** Fibre is Celestia's optional low-latency data
availability path. When a client pays for a Fibre blob, the validators sign that
they received the data, and each validator takes on an obligation to serve its
assigned slice of the erasure-coded rows for a retention window —
`shard_retention`, **4 hours by default**. The chain records the payment. It
does **not** record, or enforce, whether validators actually keep serving after
that.

### Why external observation

The serving obligation is off-chain and unenforced. A validator that collects
the payment and then quietly stops answering an hour later has broken the
guarantee Fibre sells, and nothing on chain reflects it. Validator-reported
metrics are not evidence — the entity being measured is the entity reporting.

The only trustworthy check is to *be a client*: dial the validator's registered
Fibre endpoint with no special access, ask for the shard exactly as a real
retrieval would, and verify what comes back against the commitment. A Sentinel
does that continuously, from infrastructure that has nothing to do with any
validator. Everything it needs — the validator set, each validator's registered
host, the consensus key that endorses each TLS identity, the assignment — it
reads from the chain and recomputes itself (`fibre-assign`, `fibre-tlsverify`).

### Why repeated measurement across the window

The obligation is to serve **continuously for the whole window**, not to serve
at some single moment. One probe at `t + 2min` says nothing about `t + 3h`. A
validator could answer only when it senses a probe, or go dark for an hour in
the middle, or prune early under disk pressure.

So the Sentinel probes **several points spread across each blob's own window**,
and the points are packed toward the deadline (`x^0.7` spacing, configurable).
That is deliberate: retention breaches concentrate near the end of the window —
a validator under storage pressure prunes early, a validator that has "moved on"
stops responding as the deadline approaches. An evenly-spaced schedule spends
probes on the easy early period where almost nothing fails.

### Why the tolerance is set from the *measured* prune lag

The protocol deadline is exact:

```
must_serve_until  =  creation_timestamp + max(payment_promise_timeout, shard_retention)
```

But an honest fibre server does not delete the blob at that instant. Its prune
loop runs once a minute and keys on a minute-resolution timestamp, so in
practice the blob stays downloadable until **`must_serve_until + ~1m45s`**. This
was measured directly on the devnet: last `SERVED_OK` at `pruneAt + 1m18s`,
first `NOT_FOUND` at `pruneAt + 1m43s`, all three surviving validators flipping
together.

If the Sentinel flagged the first `NOT_FOUND` after `must_serve_until` as a
fault, it would raise a false alarm on **every single publication**, because
that lag *is* the honest behaviour. So the **grace window** — the span after
`must_serve_until` where `NOT_FOUND` is tolerated, not faulted — is set from the
measured lag plus margin (`-prune-tolerance`, default **2m30s**), never from the
protocol number. This is the line between a monitor operators trust and one they
mute. Run against a network with a different `shard_retention` or prune cadence,
re-measure, and set `-prune-tolerance` to match.

The concept behind the tool (a five-layer probe: registry, reachability,
identity, RPC health, retrievability) is described in
`celestia_fibre_sentinel_concept_memo.pdf`. This implementation does the
registry lookup (`x/valaddr`), reachability (L1–L3 below), identity
(`fibre-tlsverify`), and retrievability (L4).

---

## sentinel-scan

At each height it does two things.

**1. Tracks fibre params.** `EventUpdateFibreParams` carries a full `Params`
snapshot. The scanner keeps an ordered history keyed by `(height, tx index)`,
seeded once by an ABCI query of `Query/Params` at the start height, and
re-read from state every 60 blocks so a change that arrived without an event
is still caught. Nothing is hard-coded.

`must_serve_until` is **not** taken from the params at the settlement point.
The server reads the params when the shard is uploaded, which happens
somewhere between the promise height and the settlement tx, so the scanner
evaluates every params entry in force anywhere in `[promise height - 1,
settlement]` and records the **earliest** bound, setting
`must_serve_until_ambiguous` when they do not all agree. The earliest bound is
the one no server could have undershot, so a longer window on the server can
only produce `SERVED_PAST_WINDOW` or `EXPECTED_GONE`, never a fault. Taking
the settlement point's params instead would compute a later deadline, which is
the accusing direction. docs/verdicts.md states the rule in full; an operator
checking a FAULT should compute the deadline that way.

**2. Records publications.** For each single-message `MsgPayForFibre` (the shape
consensus enforces, detected with `x/fibre/types.TryParseFibreTx`) it persists:

| field | source |
|---|---|
| every `PaymentPromise` field | decoded from the tx |
| `promise_hash` | `fibre.PaymentPromise.Hash()` — the on-chain identity |
| `settlement_height` / `settlement_time` / `settlement_tx_hash` / `_tx_index` / `_tx_code` | the block + tx |
| `params_at_publication` | param history at `(settlement_height, tx_index)` |
| `must_serve_until` | `creation_timestamp + max(payment_promise_timeout, shard_retention)`, over the **earliest** params in force in `[promise height - 1, settlement]` |
| `must_serve_until_ambiguous` | set when those params did not all agree, so the deadline is a bound rather than a value |
| `assignment` | `fibre-assign` shard table over the validator set at the **promise height** |

```
sentinel-scan -rpc http://127.0.0.1:26657 -data-dir ./data -start-height 1        # scan to tip, exit
sentinel-scan -rpc http://127.0.0.1:26657 -data-dir ./data -follow                # then keep following
```

Output: `<data-dir>/state.json` (cursor + full param history + protocol-params
fingerprint), `<data-dir>/publications.jsonl` (one record per line,
append-only) and `<data-dir>/payments.jsonl` (one escrow movement per line:
settlement, timeout, deposit, withdrawal request, withdrawal payout).

**Payments.** The scanner records the economy side of `x/fibre` beside the
publications, from the same blocks. A settlement or timeout carries no amount
in any chain event, so the charge is recomputed from the promise's padded
`blob_size` with the module's own formula (`650,000 + 45,000 × ⌈size / 256
KiB⌉` gas at 1 utia/gas), which is exactly what the module charges; the
publisher is the account whose key signed the promise, whoever broadcast the
transaction. A timeout is recorded only when somebody submitted it: an
abandoned promise nobody reports leaves no trace, so the timeout count is a
floor. The collector ingests the file and polls each known publisher's escrow
balance by state query (there is no list-all query); the API publishes it all
under `/v1/market`, `/v1/publishers` and `/v1/publishers/{addr}`, and names
accounts from an optional `publishers.yaml` registry (`observer-api
-publishers`), whose source is shown with every label.

**Assignment constants.** `OriginalRows`, `TotalRows`, `MinRowsPerValidator`,
`LivenessThreshold` are not on chain — see `fibre-assign`. They come from the
pinned `assign.ParamsV10BlobV0` and are valid only for **blob version 0** on a
celestia-app build matching `assign.PinnedCelestiaAppCommit`. Other blob
versions are recorded with an explicit `assignment.error`, never a wrong table;
every record embeds the fingerprint so it is self-describing.

---

## sentinel-probe

Each cycle, for every publication:

**1. Derives a probe schedule** from that record's window
`[settlement_time, must_serve_until]` — a few in-window points (`x^0.7` spacing,
`-in-window-probes`), one **grace** point (`must_serve_until + -grace-offset`),
one **post** point (`must_serve_until + -prune-tolerance + -post-margin`, where
`NOT_FOUND` is the expected answer). The window comes from the record, so the
cadence follows the on-chain params that were in force when the blob was
published.

**2. Probes each assigned validator**, layer by layer, each timed and judged on
its own:

| layer | check |
|---|---|
| L1 DNS | resolve the registered host (skipped for a literal IP) |
| L2 TCP | connect |
| L3 TLS | TLS 1.3 handshake (raw), record version / cipher / peer-cert fingerprint + validity |
| L3 identity | `fibre-tlsverify` — the peer cert's extension must be endorsed by the validator's consensus key for this chain ID |
| L4 retrievability | `DownloadShard`, then verify the returned rows against the commitment (`pkg/rsema1d`) **and** against the recomputed `fibre-assign` assignment |

Hosts come from `x/valaddr` `AllBondedFibreProviders` (latest height, cached);
consensus keys from `/validators` at the promise height. The assignment is
**recomputed** here and cross-checked against the row counts in the scan record
— a mismatch is a hard error, not a silent divergence.

**3. Appends one raw `Measurement`** per probe: vantage, scheduled/started/
finished times + lateness, each layer's separate duration and result, the
identity verdict (with the claimed validity window even on failure), rows
returned and both verification results, and the raw error text. **No scores** —
a reliability view is a separate, later derivation from these records.

**Transport-timeout retry.** A Fibre server's default connection cap (16) is
filled by a single 16-signer upload, so a probe that arrives during an upload
waits for a slot and can time out at TCP or TLS without saying anything about
retention. When the first attempt ends in a transport timeout (TCP connect
timeout, TLS handshake timeout, or gRPC `Unavailable` caused by a timeout) the
prober waits `-retry-delay` (20s) and runs the probe once more; the second
attempt is the recorded measurement and carries the first in its `retry`
field. The retry is skipped when it would land in a different schedule phase
than the first attempt. `-retry-transport-timeout=false` disables it. A
download that started and then ran out of time is not retried.

### Error-class taxonomy

Classification is a fact about one probe (given whether the validator is
assigned this shard, whether the settled promise *proves* it stored the shard,
and which phase the probe's *actual start time* falls in), never an aggregate:

| assigned | attested | phase | outcome | classification |
|---|---|---|---|---|
| yes | no | any | any | **UNATTESTED** (no proof this validator ever stored the shard; outside every rate, in both directions) |
| yes | yes | in-window (`t < must_serve_until`) | `NOT_FOUND` / `INVALID_ROWS` | **FAULT** (identity verified, and it did not serve what the chain proves it holds; a `NOT_FOUND` within 30 s of the deadline is `TOLERATED`) |
| yes | yes | in-window | `SERVER_ERROR` | **SERVER_ERROR** (reached, answered with an application error; not distinguishable from a hiccup from one probe) |
| yes | yes | in-window | `RPC_THROTTLED` | **THROTTLED** (reached, refused with a rate limit; says nothing about the shard) |
| any | any | any | certificate not endorsed by this validator's consensus key | **IDENTITY_MISMATCH** (an unusable endpoint, shown as its status; not a fault) |
| yes | yes | in-window | `DNS_FAIL` / `TCP_*` / `TLS_HANDSHAKE_FAIL` / `RPC_UNAVAILABLE` / `RPC_ERROR` | **UNREACHABLE** (not a fault: from one vantage this is our path too) |
| yes | yes | any | `NO_REGISTERED_HOST` | **NOT_REGISTERED** (jailing and unbonding drop the bonded entry) |
| yes | yes | any | `WRONG_ROWS` / `PARTIAL` whose rows verify against the commitment | **SHADOWED_SHARD** (another promise over the same blob answered) |
| yes | yes | any | lapsed but correctly signed certificate | **IDENTITY_EXPIRED** (a late renewal, not impersonation) |
| yes | yes | in-window | `SERVED_OK` | **HEALTHY** |
| yes | yes | grace (`msu` … `msu + prune-tolerance`) | `NOT_FOUND` / unreachable | **TOLERATED** |
| yes | yes | post (`> msu + prune-tolerance`) | `NOT_FOUND` | **EXPECTED_GONE** |
| yes | yes | post | unreachable | **UNREACHABLE_POST_WINDOW** (not a fault; obligation over) |
| yes | yes | post | `WRONG_ROWS` | **SERVED_PAST_WINDOW** (`DownloadShard` enforces no assignment) |
| yes | yes | post | `SERVED_OK` | **SERVED_PAST_WINDOW** (fine; affects disk accounting) |
| no | — | any | `NOT_FOUND` | **EXPECTED_UNASSIGNED** |
| no | — | any | `SERVED_OK` | **SERVING_UNASSIGNED** (flagged for review) |
| any | — | any | probe could not run / slot elapsed | **PROBE_ERROR** / **NOT_PROBED** |

A **fault** is the only thing said against a validator, and it means both
halves of one sentence: the observer *reached* it, and it failed to hand over
a shard the chain *proves* it stored. Anything short of that has its own class
and stays out of the serve rate in both directions. The rate's population is
in-window probes only; a grace probe can only ever add HEALTHY, so counting
grace rewarded over-retention instead of measuring retention.

"Attested" means the observer verified a signature from that validator over the
settled promise against its consensus key. A Fibre server writes the shard to
its store before it signs, so a verified signature proves storage. The absence
of one does not prove the opposite: the publisher stops collecting signatures at
the safety threshold and keeps delivering in the background, so absence means
*unproven*. `internal/scan/attest.go` explains why the observer verifies rather
than counting the entries the transaction carries.

### Load shape

Probes run on `-concurrency` (8) workers across validators, never more than
one connection to a validator at a time (R4 section 3.5). `publications.jsonl`
is tailed incrementally and a publication is forgotten once every slot of
its schedule has a row. On a (re)start every elapsed slot without a row gets
a `NOT_PROBED` row, however old, so an obligation the prober never reached
is counted as unobserved rather than missing from the obligation total;
`-backfill-missed` (default 0, unbounded) caps how far back that goes for a
fresh prober pointed at a data directory with days of history.
A publication whose settlement tx failed, or whose promise names another
chain than the RPC's, is skipped with one log line.

The gRPC receive limit matches the reference client
(`ProtocolParams.MaxMessageSize()`, about 139 MB), and the download deadline
grows with the expected shard size (`-download-timeout` plus one second per
MiB); a download that still does not finish is `RPC_DEADLINE`, a
`PROBE_ERROR`-class gap, never a fault. Every resolved address of a host is
tried, IPv4 first, and the download talks to the address the TLS check
passed on. A validator with no `x/valaddr` host is `NO_REGISTERED_HOST`,
which counts as unreachable.

Every measurement records `clock_offset_ms` (the observer's clock minus the
chain's latest block time). Phase boundaries are seconds to minutes wide and
are judged against the local clock, so a drifted vantage would mislabel
probes silently; past 30 seconds of offset the prober logs a warning.

### Restart / no hangs

The pending-probe queue is **never persisted** — it is re-derived every cycle
from `publications.jsonl` + `measurements.jsonl`, so a restart resumes exactly.
Each measurement's dedupe key is `(vantage, promise_hash, validator,
scheduled_at)`. Every wait is bounded: the loop sleeps at most `-max-sleep`
(30s) between cycles, every probe layer has its own timeout, a schedule point
older than its lateness allowance is recorded `MISSED` instead of probed
(`-max-lateness`, 90s, raised to `-max-lateness-fraction` of the blob's own
window when that is longer: 12 minutes on a 4-hour window, so the tail of a
hundred-validator point is not dropped because a few dead endpoints held
the worker pool; the phase is decided from the actual start, and the check
is repeated right before each probe), and
SIGINT/SIGTERM stops cleanly. `-once` probes everything currently due and exits;
`-drain` runs until every schedule is in the past; `-deadline` caps the run.

Run one prober per vantage point (`-vantage <name>` is recorded on every
measurement); several probers writing to independent data dirs give you
multi-vantage coverage.

```
<data-dir>/measurements.jsonl   one raw Measurement per line, append-only + fsync
```

---

## How it was verified

**Unit** (`go test ./...`, 14 tests): param-history ordering incl. same-block
updates, `must_serve_until` derivation, `EventUpdateFibreParams` JSON parse, the
assignment-table builder, store dedupe/resume, schedule shape/ordering/spacing,
`PhaseAt` boundaries, and the full taxonomy table.

**End-to-end, scanner** — `./devtest.sh 4 4`: fresh `multi-node-fibre.sh` devnet,
`sentinel-scan` following, 4 blobs published; every commitment recorded with
`must_serve_until == creation + 10m` and a non-degenerate assignment
(`sigma == distinct == 12291`, all 4 validators hold rows), `sentinel-verify`
green.

**End-to-end, prober + fault injection** — `./probe-devtest.sh 4 3` (**~14 min**;
the retention floor is 10 min): scanner + prober against a fresh devnet, then one
assigned validator's fibre server killed mid-window. The prober drains its whole
schedule — 3 blobs × 5 schedule points × 4 validators = **60 measurements** —
and `sentinel-measure-check` confirms the taxonomy held with **zero
misclassifications**:

| classification | count | meaning |
|---|---:|---|
| `HEALTHY` | 27 | live validators, in-window, rows verified against commitment + assignment |
| `FAULT` | 9 | the killed validator, in-window (3 blobs × 3 in-window points) — caught at L2 in <1 ms |
| `TOLERATED` | 12 | grace phase: blob already pruned + the killed validator unreachable |
| `EXPECTED_GONE` | 9 | post phase, live validators, blob pruned as expected |
| `UNREACHABLE_POST_WINDOW` | 3 | post phase, the killed validator — obligation over, not faulted |

Sample outputs from that run are committed under [`sample/`](sample/).

---

## Layout

```
cmd/sentinel-scan          the scanner CLI
cmd/sentinel-probe         the probe scheduler / measurement service
cmd/sentinel-pub           devnet publish helper (fibre.Client.Upload + MsgPayForFibre; -abandon / -timeout / -withdraw for the escrow side)
cmd/sentinel-verify        checks publications.jsonl against expected commitments
cmd/sentinel-verify-export checks a downloaded daily export offline: sidecar, members vs manifest, ed25519 signature (docs/exports-signing.md)
cmd/sentinel-anchor        builds and prints (never broadcasts) the PayForBlobs that would anchor an export's manifest digest on Celestia
internal/uploadprobe       upload-side measurement groundwork: per-validator results from the fibre client's spans (docs/research/R13-upload-probing.md)
cmd/sentinel-measure-check checks measurements.jsonl against the taxonomy
internal/scan              scanner, param history, record schema, store, CometBFT RPC client
internal/probe             schedule, layered probe, measurement store, classifier, prober loop
sample/                    outputs from a real devtest / probe-devtest run
```

## Build

Go 1.23+ with `GOTOOLCHAIN=auto` (celestia-app pins `go 1.26.5` and the toolchain
auto-downloads). `fibre-assign` and `fibre-tlsverify` are sibling modules in this
repo, resolved by relative `replace` directives — build from a full checkout of
the repo, not from this subdirectory alone.

```
cd fibre-sentinel && go test ./... && go build -o bin/ ./cmd/...
```

## License

Apache-2.0. See [`../LICENSE`](../LICENSE) and [`../NOTICE`](../NOTICE).
