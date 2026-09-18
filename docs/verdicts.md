# Verdict taxonomy

Every row the observer stores and every number the dashboard shows is built
from two fields that `fibre-sentinel` already records per probe: the
mechanism-level **outcome** (what happened on the wire) and the
**classification** (what that means given the phase of the retention window
and whether the validator was assigned the shard). The dashboard adds no new
verdict classes. It only groups and counts the existing ones.

Source of truth:

- outcomes and classifications: `fibre-sentinel/internal/probe/classify.go`
  (`Classify`), tested by the taxonomy table in
  `fibre-sentinel/internal/probe/probe_test.go`
- served / wrong rows / partial: `fibre-assign/verify.go` (`ShardMap.Verify`),
  tested in `fibre-assign/assign_test.go` (translated from celestia-app's
  `fibre/validator/set_test.go`)
- phases: `fibre-sentinel/internal/probe/schedule.go` (`PhaseAtWindow`)

## Phases

| phase | span | what a reader should conclude |
|---|---|---|
| `in_window` | settlement to `must_serve_until` | the validator is under its retention obligation |
| `grace` | `must_serve_until` to `must_serve_until + prune tolerance` (default 2m30s) | an honest server prunes late by 1 to 2 minutes; nothing here counts against a validator |
| `post` | after the grace span | the obligation is over; the blob is expected to be gone |

`must_serve_until = creation_timestamp + max(payment_promise_timeout, shard_retention)`,
using the on-chain params. The server itself reads the params when the shard
is uploaded, which happens somewhere between the promise height and the
settlement tx; the scanner evaluates every params entry in force anywhere in
that interval and, if they do not all agree, records the earliest bound and
sets `must_serve_until_ambiguous` on the publication. The earliest bound is
the one no server could have undershot, so a later window on the server can
only produce `SERVED_PAST_WINDOW` or `EXPECTED_GONE`, never a fault. The
scanner also re-reads the params from state every 300 blocks: a governance
change announces itself with an event, an upgrade handler does not, and a
change that arrived silently is recorded as in force from the next block.

## Who is actually obliged

A validator is assigned rows by `fibre-assign`, but assignment is the
publisher's arithmetic, not the validator's consent. The validator becomes
obliged only once it has the shard. The only on-chain evidence of that is a
signature from the validator on the settled `MsgPayForFibre`, because the
Fibre server writes the shard to its store **before** it signs
(celestia-app `fibre/server_upload.go`).

The observer verifies those signatures itself rather than counting them. The
chain's own check runs in the ante handler and is skipped in
`ExecModeFinalize` and on a node-local cache hit (`x/fibre/ante/ante.go`),
and the message server never repeats it, so a settled transaction carries no
state-machine guarantee that its signature entries are valid. Each entry is
tried first against the validator at the same position in the set at the
promise height (the reference implementation builds the list positionally,
with nil entries for non-signers) and then, if that fails, against every
other member. Verification decides; position is only a hint.

The absence of a signature is **not** evidence the validator did not store
the shard. The reference client snapshots the signature list the moment the
safety threshold is reached and keeps delivering to the rest in the
background (`fibre/client_upload.go`), so a validator can hold a shard whose
signature never reached the chain. Absence means *unproven*, never *absent*.

That is why an assigned but unattested probe becomes `UNATTESTED` whatever
the wire outcome was, and sits outside the serve rate in both directions: a
failure the validator was never proven to owe cannot count against it, and a
success it was never proven to owe cannot count for it. The outcome field
still records exactly what happened.

Source: `fibre-sentinel/internal/scan/attest.go`, tested in
`attest_test.go`; the classification in `internal/probe/classify.go`, tested
by `TestClassify_UnattestedIsNeverAFault`.

## Outcomes (one per probe, mechanism level)

| outcome | meaning |
|---|---|
| `SERVED_OK` | shard returned, every row verifies against the commitment and the returned indices equal the assigned set |
| `SERVER_ERROR` | the endpoint was reached, completed TLS, proved its identity and answered the RPC with an application error (gRPC `Internal`, `Unknown`, `DataLoss`, `Aborted`). Deliberately not a reachability failure: calling it "unreachable" would be false about a server the observer just talked to. Deliberately not a fault either: the server did not say it lacks the shard |
| `RPC_THROTTLED` | the endpoint was reached, proved its identity and refused the request with `ResourceExhausted` that is not the observer's own receive bound: a server-side limit. Celestia has said a per-peer rate limiter is coming to the Fibre server. Never a fault, and not the observer's error either; the probe policy counts it as a failure for backoff |
| `PARTIAL` | fewer rows than assigned, the ones returned are valid |
| `WRONG_ROWS` | rows returned but not the assigned set (`ShardMap.Verify` failed) |
| `INVALID_ROWS` | rows returned but they fail commitment verification |
| `NOT_FOUND` | the server answered cleanly that it has no such shard |
| `DNS_FAIL` | registered host name did not resolve |
| `TCP_REFUSED` | connection actively refused |
| `TCP_TIMEOUT` | TCP connect timed out |
| `TCP_UNREACHABLE` | network or host unreachable, DNS fine |
| `TLS_HANDSHAKE_FAIL` | TCP fine, TLS 1.3 handshake failed |
| `IDENTITY_FAIL` | handshake fine, certificate extension is not endorsed by this validator's consensus key |
| `RPC_UNAVAILABLE` | gRPC Unavailable after a good handshake |
| `RPC_DEADLINE` | the download did not finish within the observer's own deadline (base plus shard size at 1 MiB/s); the observer gave up, the validator was not judged |
| `RPC_ERROR` | any other gRPC error |
| `NO_REGISTERED_HOST` | the validator has no fibre host in `x/valaddr`, so nobody can fetch its rows; treated as unreachable |
| `PROBE_ERROR` (no coder) | the observer could not build the verifier for this blob's `(original_rows, total_rows)`, so no download was attempted |
| `REACHABLE` | TCP, TLS and identity passed and the download was deliberately skipped (heartbeat, or policy backoff); no retention verdict |
| `PROBE_ERROR` | the observer's own probe failed (bug or config), not the target |
| `MISSED` | the scheduled point elapsed before the prober ran it |

## Classifications (the verdict)

One sentence each, and what a reader should conclude.

| classification | when | conclude |
|---|---|---|
| `HEALTHY` | assigned validator returned `SERVED_OK` in window or in grace | the validator kept its promise at this point in time |
| `FAULT` | an identity-verified endpoint, for a shard the chain proves it stored, **said it has no such shard** (`NOT_FOUND` in window), **returned bytes that do not verify against the commitment** (`INVALID_ROWS`, any phase), or **returned rows outside this promise's assignment** that verify against nothing (`WRONG_ROWS`/`PARTIAL` in window or grace) | the validator broke its retention promise; this is the only class that counts against a validator. Three conditions, each reproducible by anyone who repeats the probe. One margin: a `NOT_FOUND` whose answer arrives within 30 s of `must_serve_until` is graded as grace (`TOLERATED`) and the row says `phase_note: not_found_at_deadline`, because the server prunes on a minute tick against its own clock and the RPC reaches it tens of seconds after the probe's phase was fixed. The fault count on the overview and per validator counts every phase; the serve rate's population is in-window only |
| `UNREACHABLE` | assigned and attested, in window, and the observer could not complete a conversation at all: `DNS_FAIL`, `TCP_REFUSED`, `TCP_TIMEOUT`, `TCP_UNREACHABLE`, `TLS_HANDSHAKE_FAIL`, `RPC_UNAVAILABLE`, `RPC_ERROR` | we could not get to it. From one vantage that is not distinguishable from a route, firewall or peering problem on the observer's own path, so it is published in full beside the serve rate and kept out of it |
| `NOT_REGISTERED` | assigned validator with no Fibre host in `x/valaddr` at the time of the probe (`NO_REGISTERED_HOST`) | a registry state, not a refusal. Jailing and unbonding remove a provider from `AllBondedFibreProviders` while the chain keeps the entry: it is garbage-collected only once the validator is gone from staking state, or jailed and unbonded for longer than the unbonding time plus seven days |
| `SHADOWED_SHARD` | assigned validator returned rows that **verify against the blob commitment**, are not this promise's assignment (`WRONG_ROWS` or `PARTIAL` with `commitment_verified`), and are **exactly the row set another settled promise over the same commitment assigns to this validator** (`shadowed_by` names it) | that promise answered in this one's place. `DownloadShard` is addressed by the commitment alone and a store keeps one shard per commitment, so the validator has no way to tell the two apart. Never a fault. Without a matching promise the same wire result is an incomplete or wrong delivery of this shard and is a `FAULT` in window and in grace: "shadowed" is shown, not assumed, and the row carries the returned indices so anyone can check |
| `IDENTITY_EXPIRED` | certificate endorsed by the right consensus key, but its signed validity window has lapsed or has not started | a renewal running late. Endpoint hygiene, not impersonation and not a retention failure |
| `IDENTITY_MISMATCH` | certificate not endorsed by this validator's consensus key, any validator, any phase (judged before attestation: a certificate is a property of the endpoint) | no client will download from this endpoint, so it is as unusable as one that does not answer. Shown as the endpoint's status and in the endorsement rate, held out of the serve rate: a wrong certificate proves nothing about any shard |
| `SERVER_ERROR` | assigned and attested, in window, and the endpoint answered with an application error instead of the shard (`SERVER_ERROR` outcome) | the server was reached and did not say it lacks the shard. From one probe this is not distinguishable from a transient fault (an overloaded process, a disk hiccup), so it is shown beside the rate and never inside it; a server that errors at every point is visible as such on its own page. In grace it is `TOLERATED`, after the window `UNREACHABLE_POST_WINDOW` |
| `THROTTLED` | assigned and attested, in window, and the endpoint refused the download with a rate limit (`RPC_THROTTLED` outcome) | the server was reached and declined to serve this request. That says nothing about the shard, so it is shown beside the rate and never inside it; the prober backs off from a validator that says so, and a limit set tight enough to turn away real clients is visible as such on the validator's own page. In grace it is `TOLERATED`, after the window `UNREACHABLE_POST_WINDOW` |
| `TOLERATED` | assigned validator, grace phase: `NOT_FOUND` or unreachable | honest pruning lag; do not read anything into it |
| `EXPECTED_GONE` | assigned validator, post phase: `NOT_FOUND` | correct behaviour after the window |
| `SERVED_PAST_WINDOW` | assigned validator, post phase: still serving (`SERVED_OK`, `PARTIAL`, or `WRONG_ROWS`) | not a fault; the validator keeps data longer than it must. `WRONG_ROWS` is here rather than under FAULT because `DownloadShard` performs no assignment check at all — assignment is enforced only at upload — so rows outside an assignment, after the obligation ended, are not a rule the validator broke |
| `UNREACHABLE_POST_WINDOW` | assigned validator, post phase: unreachable | not a retention fault; the obligation was over. It still feeds the reachability view |
| `EXPECTED_UNASSIGNED` | validator not assigned this shard answered `NOT_FOUND` or was unreachable | normal; only probed when `-probe-unassigned` is on |
| `SERVING_UNASSIGNED` | validator not assigned this shard returned data for it (`SERVED_OK`, `PARTIAL`, `WRONG_ROWS` or `INVALID_ROWS`) | unexpected; either the observer's assignment is wrong or the validator over-serves. Shown for review, never as a fault |
| `UNATTESTED` | assigned validator, any phase and any outcome, where no verified signature from that validator appears on the settled promise | nothing on chain proves this validator ever stored the shard, so no verdict is owed either way. Outside every rate, in both directions |
| `PROBE_ERROR` | the observer could not carry out the probe, or gave up on it (`PROBE_ERROR`, `RPC_DEADLINE`) | an observer problem, shown as a gap |
| `NOT_PROBED` | the slot elapsed unprobed (observer down or late), or the download was skipped by policy (`MISSED`, `REACHABLE`) | a gap in observation, never a zero |

## How the dashboard derives its numbers

- **The rate's population** is probes of an assigned shard while the validator
  was *under obligation*: `assigned = 1 AND phase = 'in_window'`. The grace
  phase is deliberately outside it. A grace probe can only ever add `HEALTHY`,
  because `NOT_FOUND` and unreachability there are `TOLERATED` by design, so
  counting grace gave a validator that prunes promptly a **lower** rate than
  one that over-retains with identical in-window behaviour — the opposite of
  what the number claims to measure, on exactly the axis the "worst first"
  table sorts by.
- **Serve rate per probe** over that population = `HEALTHY / (HEALTHY + FAULT)`
  (`serve_rate`). Every other class is published beside it under its own
  name, never folded in, and the API carries both the counts
  (`serve_rate_held_out`) and the reason each class is out
  (`serve_rate_excluded_classes`) so the dashboard cannot describe the
  exclusions differently from the API. It is published, not headlined: see
  the next two entries.
- **Verdict coverage** (`serve_rate_coverage`) = `(HEALTHY + FAULT)` over every
  probe in that population. A high rate over low coverage is a statement about
  a handful of probes, and without this figure a reader cannot tell the two
  apart. The `probe_count` and `probe_gaps` fields are over *all* probes, a
  different population; they are not this.
- **Obligations** (`obligations`, whose `rate` is repeated as
  `serve_rate_by_obligation`) count one observation per (validator, blob)
  the settled promise proves (`COALESCE(attested, 1) = 1`), and this is the
  headline on every page. The schedule visits the same validator and blob
  four times in window, and the minimum-rows floor assigns every bonded
  validator every blob, so the probes inside one obligation are
  near-perfectly correlated — one lapsed certificate produces four `FAULT`
  rows for one event. Any confidence interval is drawn around the obligation
  count, never around the probe count, and the dashboard states it as an
  **upper bound on the fault rate**, because that is the direction an
  accusation is made in.
- **Which obligations.** `attested = 1` only: an obligation the settled
  promise proves. `attested = 0` is nothing to keep or break; `attested`
  NULL (a record from before signatures were verified) is not evidence
  either way and is outside the count, reported as
  `attestation.unknown_probes`. An obligation belongs to a window by its
  publication's `settlement_time`, not by each probe's `started_at`, so it
  is judged whole or not at all. The window's end is the moment the verdict
  is drawn; an obligation whose `must_serve_until` is later is `pending` and
  in no rate.
- **An obligation is judged by its newest in-window probe**, the same rule
  the per-blob reconstructability verdict uses; a `NOT_PROBED` or
  `PROBE_ERROR` row is never the newest while a real probe exists. The
  buckets:
  - `served` — newest probe `HEALTHY`, no `FAULT` anywhere;
  - `broken` — any probe `FAULT`;
  - `end_unobserved` — a `HEALTHY` probe earlier, but the newest probe
    produced no verdict;
  - `unobserved` — no `HEALTHY` and no `FAULT` at all, split by what the
    probes did see: `unobserved_reachable` (at least one download attempt
    with `tls_ok = 1`: the endpoint completed a handshake and answered with
    `SERVER_ERROR`, `THROTTLED`, an `RPC_*` failure or an unusable
    certificate), `unobserved_unreachable` (attempts, none of which
    completed TLS), `unobserved_not_probed` (no attempt: backoff, a load
    cap, a slot that elapsed).

  Only `served` and `broken` enter the rate. The old rule, "kept when no
  probe of it faulted", let a validator that served at the first point and
  answered 500 at the next three count as fully kept — the profile of a
  server that pruned early, which is the finding this observer exists to
  make. Under the new rule that obligation is `end_unobserved`, and a
  validator that never hands anything over is `unobserved_reachable`: not a
  fault, but not a clean record either, and counted on its own line beside
  the rate. The split uses each probe row's own `tls_ok`, not the heartbeat,
  because the probe made its own handshake at the moment that matters.
- Below **20** rated observations the percentage is printed without a gauge
  and the validator is not ranked by it in either direction (it sorts with
  the rows that have no rate at all). A single unlucky probe used to
  render as "0.0%" beside a named validator and sort it above one with a
  hundred real faults.
- **Suspect points** (`vantage_health.suspect`). At any in-window schedule
  point where at least `min_validators` (3) and at least `threshold` (50%)
  of the distinct validators probed were `UNREACHABLE`, or at least
  `fault_threshold` (50%) and three were `FAULT`, every probe row at that
  `scheduled_at` is left out of the per-probe rate, the coverage and
  held-out counts, the obligation buckets, the per-point breakdown and the
  fault count, network-wide and per validator alike. Validators fail
  independently; one observer's network, or one observer's stale
  assignment, does not. The points, the shares and the number of rows
  removed are published so the exclusion is visible, and the rows keep
  their classification in the store: a verifier sees what was excluded and
  why.
- **Stale assignment pin.** The prober polls `abci_info` and stamps the
  chain's `app_version` on every row. When it is above the celestia-app
  major the assignment constants are pinned to
  (`fibre-assign.PinnedCelestiaAppMajor`), every probe that depends on
  assignment is `PROBE_ERROR` ("assignment pin stale"), never `HEALTHY` or
  `FAULT`; identity and registry verdicts, which do not depend on
  assignment, are still drawn. The row says `observer.pin_stale`, so the
  gap is attributable after the fact.
- **Evidence on the row.** Every measurement records, beside the verdict:
  `download.row_indices` (the indices returned, in returned order),
  `download.rows_sha256` (SHA-256 over the returned row payloads in that
  order), `download.rpc_code` (the gRPC status code of a failed download),
  `download.shadowed_by` (the promise whose assignment the returned rows
  match), `observer.build` (the observer's VCS revision), and
  `observer.assign_pin` / `observer.app_version`. The store keeps them as
  columns (`row_indices`, `rows_sha256`, `rpc_code`, `shadowed_by`,
  `observer_build`, `app_version`) and `/v1/probes` publishes them. A
  classification is a function of the wire result and the code; with these
  fields both halves are on the row.
- **Reachability** (`reachability_window`) is heartbeats that completed TLS
  over heartbeats sent, per validator and network-wide. The numerator is
  `tcp_ok = 1 AND tls_ok = 1`; whether the certificate was the right one is
  the separate `identity_rate_window` ("Endorsed"). Heartbeats exist only
  while the validator is in `AllBondedFibreProviders`, so a jailed or
  unbonded validator's denominator stops growing and the table prints no
  percentage for it. The table's status word is liveness only: the chain's
  own `jailed` and `bond_status` first, then `host`, then the latest
  handshake; a fault never appears there, it has its own column. "Reachable
  now" (`reachable`) is the newest heartbeat or probe (any phase, assigned or
  not, gaps excluded) with TCP and TLS both successful. `last_reachable_at`
  and `last_unreachable_at` say how long the current state has held.
- **A closed endpoint row** (`closed_reason = left_bonded_provider_list`)
  no longer lends its host to the validator row. `host` is empty, and
  `last_host` / `endpoint_closed_at` say what was registered and when it left
  the list. The host used to be back-filled from the newest probe row, which
  made the word for a jailed validator depend on whether the prober had
  restarted since it left: "down" while the prober's in-memory last-known
  host kept being dialled, "no host" after a restart.
- **Throughput** (`serve_bytes_per_second`) is the median of
  `bytes_returned * 1000 / download_ms` over `HEALTHY` in-window probes that
  carry a byte count (`serve_throughput_sample`); records from before schema
  8 have no byte count and are outside the sample, never zero. It is over the
  download step alone because the dial, handshake and identity check cost
  the same for a 148-row shard as for a 4,096-row one, so a whole-probe
  figure rises with stake by construction; and it is bytes rather than rows
  because a row is as wide as its blob's square. `serve_latency_p50_ms` and
  `_p95_ms` remain the whole probe, dial to verified rows.
- **TLS identity status** = the latest identity result: verified; expired
  (`IDENTITY_FAIL` with a stale reason: the right key, a lapsed window);
  mismatch (any other `IDENTITY_FAIL`); unverified (TLS completed, no
  identity verdict recorded); no TLS (`TLS_HANDSHAKE_FAIL`); or unreachable.
  Observer-side `PROBE_ERROR` heartbeats are left out of every reachability
  figure, as their probe-side twins are.
- **Reconstructable** for a blob is judged at the latest **complete**
  in-window probe point: the newest point at which every assigned validator
  has a real result (a verdict, not a gap). Grace and post points are never
  used, because "not found" is tolerated or expected there. The distinct row
  indices held by validators whose probe at that point was `SERVED_OK` are
  compared with `OriginalRows` (4096 for blob v0). Reconstructable means at
  least that many with every assigned validator serving; degraded means fewer
  than the full assignment answered but still at least `OriginalRows`; not
  reconstructable means fewer than `OriginalRows` distinct rows were observed
  served. "Every assigned validator" means every **attested** assigned
  validator: a validator with no proof of storage cannot demote a blob by
  staying quiet. A row that was served counts toward reconstruction whether
  or not the server's storage was proven, because the rows came back either
  way; attestation decides blame, never availability. While no in-window point is complete (a sweep still running, or
  validators skipped by the policy) the status is `pending` and the blob is
  left out of the network rate: an absent row is a gap, never a zero. The
  threshold is the row count from `fibre-assign`'s pinned protocol params,
  not a hard-coded fraction of validators. Rows from several vantages count a
  validator once.
- **Reconstructable** is published as four numbers, not one: `yes` (every
  validator proven to owe the blob served), `degraded` (the rows were all
  there but someone stayed quiet), `no`, and separately `pending`/`unknown`.
  "Degraded" is never folded into the numerator, because "the blob can be
  rebuilt" and "everyone kept their promise" are different statements. The
  response also carries `publications_in_window`, `publications_examined` and
  `sample_limit`, so a rate over the newest 2000 of 50000 cannot be read as a
  rate over the window.
- `PROBE_ERROR`, `NOT_PROBED` and `MISSED` are excluded from every rate and
  rendered as gaps. `UNATTESTED` is also excluded, but it is not a gap: the
  probe ran and its outcome is recorded. It is excluded because no obligation
  was proven, which is a different statement and is labelled differently.
- Records written before the observer verified signatures carry no
  attestation at all. Their `attested` column is NULL, not 0, and they are
  counted under the older taxonomy and reported separately as
  `attestation.unknown_probes`. "Not recorded" is never rendered as "did not
  attest".
- Every measurement carries `clock_offset_ms`, the observer's clock minus the
  chain's latest block time when the probe ran. Phases are decided against the
  local clock and the grace span is only a few minutes wide, so a vantage
  whose offset is large can be discounted after the fact. The prober logs a
  warning past 30 seconds.

## What a fault is, and what it is not

The only thing this site says against a validator is that it was **reached**
and failed to hand over a shard the chain **proves** it stored. Everything
that falls short of both halves of that sentence has its own class and its own
column:

| the observer saw | class | why it is not a fault |
|---|---|---|
| no signature from this validator on the settled promise | `UNATTESTED` | nothing proves it was ever sent the shard |
| no answer from the endpoint at all | `UNREACHABLE` | from one vantage, indistinguishable from the observer's own path failing |
| no Fibre host in the registry | `NOT_REGISTERED` | jailing and unbonding remove the provider from the bonded list; the chain keeps the entry |
| exactly another settled promise's rows for the same blob | `SHADOWED_SHARD` | `DownloadShard` takes a commitment, not a promise hash; the validator cannot tell them apart. Genuine rows matching no promise's assignment are a `FAULT`: an incomplete delivery |
| a lapsed but correctly signed certificate | `IDENTITY_EXPIRED` | a late renewal, not someone else answering |
| a certificate signed by the wrong consensus key | `IDENTITY_MISMATCH` | an unusable endpoint, which is a statement about the endpoint (its status says so), not about a shard |
| an application error instead of the shard | `SERVER_ERROR` | the server did not say it lacks the shard; from one probe a hiccup and a loss look the same |
| a rate limit instead of the shard | `THROTTLED` | the server declined this request; it said nothing about the shard |
| an outcome the taxonomy does not recognise | `PROBE_ERROR` | "we have not taught the observer about this" is not evidence |
| a local socket error, a cancelled probe, a verification that timed out | `PROBE_ERROR` | the packets never left this machine |

This is why the serve rate moved after the audit. It did not get more
forgiving; it stopped making claims the evidence did not support.

## Known limits of a probe

These are properties of how the observer measures, not of any validator. They
are written down because a reader comparing two validators deserves to know
what the measurement cannot separate.

- **One address per probe.** Every resolved address is tried at the TCP layer
  and the first that connects is the endpoint every later layer talks to. If
  that address accepts TCP and then fails at the RPC layer, the probe does not
  fall back to the next one, so a host whose backends differ can be recorded
  as unreachable on the strength of one of them. The alternative, letting
  gRPC re-resolve as the reference client does, would let the download land on
  a different peer from the one whose certificate was checked, and the record
  could no longer say which endpoint it describes. The result is `UNREACHABLE`
  either way, which is outside the serve rate.
- **Two connections per probe.** One to read the certificate, one to download.
  A Fibre server admits a bounded number of connections, so this observer
  occupies two slots where the reference client occupies one, and a busy
  server is correspondingly more likely to look unreachable to it.
- **Shadowing needs the other promise.** `SHADOWED_SHARD` requires the
  prober to know the other promise over the same commitment. It knows every
  live publication in `publications.jsonl`; a promise settled in a block the
  scanner could not read (a scan gap) is unknown to it, and rows answered
  from that promise's shard would be filed as a `FAULT`. The gap is listed
  on the overview and the row carries the returned indices, so the verdict
  is contestable with the missing publication in hand.
- **One vantage.** Every reachability observation comes from a single network
  path. `/v1/network` publishes the worst schedule point in the window by how
  many validators were unreachable at once, because validators fail
  independently and one network does not.

## Adding a class

A new classification must map to a new case in the taxonomy table in
`fibre-sentinel/internal/probe/probe_test.go`, and if it depends on what rows
came back, to a case in `fibre-assign/assign_test.go`. The dashboard must not
introduce verdicts of its own.
