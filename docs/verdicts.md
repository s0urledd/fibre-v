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
using the on-chain params in force when the blob settled. The server itself
reads the params when the shard is uploaded, which happens between the
promise height and the settlement tx; the scanner evaluates both ends of that
interval and, if a params change landed in between, records the earlier
bound and sets `must_serve_until_ambiguous` on the publication.

## Outcomes (one per probe, mechanism level)

| outcome | meaning |
|---|---|
| `SERVED_OK` | shard returned, every row verifies against the commitment and the returned indices equal the assigned set |
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
| `FAULT` | assigned validator, in window: `NOT_FOUND`, unreachable at any layer (including no registered host), bad identity, wrong, partial or invalid rows. In grace: bad identity, wrong, partial or invalid rows. In post: bad identity, wrong or invalid rows. Any validator, any phase: bad identity (identity is a property of the endpoint, not of one shard) | the validator broke its retention promise or is not who the chain says it is; this is the only class that counts against a validator |
| `TOLERATED` | assigned validator, grace phase: `NOT_FOUND` or unreachable | honest pruning lag; do not read anything into it |
| `EXPECTED_GONE` | assigned validator, post phase: `NOT_FOUND` | correct behaviour after the window |
| `SERVED_PAST_WINDOW` | assigned validator, post phase: still serving (`SERVED_OK`, or `PARTIAL` with valid rows) | not a fault; the validator keeps data longer than it must |
| `UNREACHABLE_POST_WINDOW` | assigned validator, post phase: unreachable | not a retention fault; the obligation was over. It still feeds the reachability view |
| `EXPECTED_UNASSIGNED` | validator not assigned this shard answered `NOT_FOUND` or was unreachable | normal; only probed when `-probe-unassigned` is on |
| `SERVING_UNASSIGNED` | validator not assigned this shard returned data for it (`SERVED_OK`, `PARTIAL`, `WRONG_ROWS` or `INVALID_ROWS`) | unexpected; either the observer's assignment is wrong or the validator over-serves. Shown for review, never as a fault |
| `PROBE_ERROR` | the observer could not carry out the probe, or gave up on it (`PROBE_ERROR`, `RPC_DEADLINE`) | an observer problem, shown as a gap |
| `NOT_PROBED` | the slot elapsed unprobed (observer down or late), or the download was skipped by policy (`MISSED`, `REACHABLE`) | a gap in observation, never a zero |

## How the dashboard derives its numbers

- **Serve rate** for a validator over a window = `HEALTHY / (HEALTHY + FAULT)`,
  counting only in-window and grace probes of assigned shards. The probe count
  is shown next to every rate.
- **Reachability** on the overview and validator pages is the latest
  evidence per endpoint: the newest heartbeat or probe (any phase, assigned
  or not, gaps excluded) with TCP and TLS both successful. It is "reachable
  now", not a rate: reachability is a property of the endpoint, not of one
  blob.
- **TLS identity status** = the latest identity result: verified, mismatch
  (`IDENTITY_FAIL`), no TLS (`TLS_HANDSHAKE_FAIL`), or unreachable.
- **Reconstructable** for a blob is judged at the latest **complete**
  in-window probe point: the newest point at which every assigned validator
  has a real result (a verdict, not a gap). Grace and post points are never
  used, because "not found" is tolerated or expected there. The distinct row
  indices held by validators whose probe at that point was `SERVED_OK` are
  compared with `OriginalRows` (4096 for blob v0). Reconstructable means at
  least that many with every assigned validator serving; degraded means fewer
  than the full assignment answered but still at least `OriginalRows`; not
  reconstructable means fewer than `OriginalRows` distinct rows were observed
  served. While no in-window point is complete (a sweep still running, or
  validators skipped by the policy) the status is `pending` and the blob is
  left out of the network rate: an absent row is a gap, never a zero. The
  threshold is the row count from `fibre-assign`'s pinned protocol params,
  not a hard-coded fraction of validators. Rows from several vantages count a
  validator once.
- `PROBE_ERROR`, `NOT_PROBED` and `MISSED` are excluded from every rate and
  rendered as gaps.
- Every measurement carries `clock_offset_ms`, the observer's clock minus the
  chain's latest block time when the probe ran. Phases are decided against the
  local clock and the grace span is only a few minutes wide, so a vantage
  whose offset is large can be discounted after the fact. The prober logs a
  warning past 30 seconds.

## Adding a class

A new classification must map to a new case in the taxonomy table in
`fibre-sentinel/internal/probe/probe_test.go`, and if it depends on what rows
came back, to a case in `fibre-assign/assign_test.go`. The dashboard must not
introduce verdicts of its own.
