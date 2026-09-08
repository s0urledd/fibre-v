# ADR 0001: Fibre observer architecture

Status: proposed, for owner review. Date: 6 September 2026.

## Context

`plsgiveup/fibre` already contains everything that talks to the chain and to
validators: `sentinel-scan` records publications, params and assignments;
`sentinel-probe` schedules probes across each blob's retention window, runs
the layered probe (DNS, TCP, TLS, identity, download, verify) and classifies
each measurement. Both write append-only JSONL. There is no query layer, no
aggregation, no web view, no deployment story.

`R1-fibre-protocol-surface.md` establishes the constraints that shape the
design:

- `AllBondedFibreProviders` returns only bonded validators and only a
  `host:port` string. There is no unregister message. Endpoint history has
  to be reconstructed by the observer from `set_fibre_provider_info` events
  and from diffing the query result over time.
- `EventPayForFibre` does not carry the promise; the scanner must decode the
  tx body, which `sentinel-scan` already does.
- `DownloadShard` returns the validator's whole assigned shard. A probe cannot
  request a subset of rows, so the byte cost of a probe is fixed by the
  validator's row count and the blob's row size. This makes the probe policy
  (`R4-probe-etiquette.md`) a byte budget, not a request budget.
- The Fibre server allows 16 concurrent connections in total. An observer
  that holds connections open competes with real readers; every probe must
  dial, download, and close.
- Reconstruction needs 4096 unique verified rows of 16384. The "1/3" in the
  brief is a voting-power liveness target, not the reconstruction rule.
- Nothing observer-relevant changed between the pinned commit and celestia-app
  main; the pin should move to the `v10.x-mocha` tag when it is cut, not
  before.

## Decision

Five components, all Go except the web app, deployed on one VM for the MVP.

```
chain RPC ──> collector ──┐
                          ├──> store (SQLite for MVP, same schema on Postgres) ──> api ──> web
validators <── prober ────┘
```

### collector

Wraps `sentinel-scan` as a library, not a fork. It follows one CometBFT RPC
endpoint and writes to the store:

- `publications`: one row per `MsgPayForFibre`, every field the scanner
  already records (promise, settlement, params in force, `must_serve_until`,
  assignment table with the protocol-params fingerprint).
- `assignments`: one row per (publication, validator) with row count and the
  row index list.
- `validators`: consensus address, operator address, moniker, voting power,
  bonded status, sampled at each height the collector cares about (promise
  heights and a daily snapshot).
- `endpoints`: history, not just current. A new row whenever
  `set_fibre_provider_info` is seen or `AllBondedFibreProviders` returns a
  different host for a validator, with `first_seen_height` and
  `last_seen_height`. A validator that disappears from the bonded set gets
  its endpoint row closed, not deleted.
- `params_history`: the scanner's param history, so `must_serve_until` is
  always recomputable.

The collector starts following mocha-5 before activation. Until Fibre is
active it records zero publications and an empty endpoint set, and the
dashboard shows exactly that.

### prober

Wraps `sentinel-probe`'s schedule, probe and classify packages as a library.
Adds two things the sentinel does not have:

1. A **policy layer** implementing `R4-probe-etiquette.md`: per-validator
   byte and request budgets, deterministic unbiased sampling when the
   publication rate exceeds the budget, backoff on failure, one connection
   per validator at a time, and a global egress cap. Sampled-out blobs are
   recorded as `NOT_PROBED` so the gap is visible.
2. A **reachability probe** independent of publications: every registered
   endpoint gets a lightweight dial (TCP + TLS + identity, no download) on a
   fixed cadence, so the network overview can show reachability and TLS
   identity status for validators that have not been assigned anything
   recently.

Measurements are written to `probes` with every field of the sentinel's
`Measurement` record. The verdict is the sentinel's classification, unchanged
(`docs/verdicts.md`).

### store

SQLite for the single-node MVP, with a schema written so the same migrations
run on Postgres. Tables: `validators`, `endpoints`, `publications`,
`assignments`, `probes`, `params_history`, `snapshots`
(pre-aggregated daily per-validator counts by classification, plus probe
counts, so dashboard reads are cheap), and `observer_runs` (start and stop
times of collector and prober, so a gap in observation is rendered as a gap).

Why SQLite first: one binary, one file, trivially backed up, and the write
rate is low (a few rows per minute at early-Fibre volumes). Postgres is a
config switch, not a redesign, provided no SQLite-only SQL is used.

### api

Read-only HTTP JSON under `/v1/`. Endpoints mirror the three views:

- `GET /v1/network` (overview numbers, each with its probe count and the
  time window it covers)
- `GET /v1/validators`, `GET /v1/validators/{consaddr}`
- `GET /v1/blobs`, `GET /v1/blobs/{promise_hash}`
- `GET /v1/probes?validator=&blob=&since=` (raw rows, paginated)
- `GET /v1/meta` (chain id, pinned celestia-app commit, protocol-params
  fingerprint, vantage name, observer version, data window start)

Every aggregate response includes `probe_count`, `window_start`,
`window_end`, and `vantage`. No number without its denominator.

### web

Next.js, statically exportable, no client secrets, three pages matching the
API. Reads as an explorer: rows keyed on moniker plus consensus address,
sorted by voting power, one strong colour for `FAULT`, muted for tolerated
classes, nothing highlighted for healthy (`R3-prior-art.md` section 5.5).
Every page carries the line "observed from one location" until a second
vantage exists.

### deploy

One VM. systemd units for collector, prober and api; Caddy in front of api and
web. A docker-compose file as the alternative. Both documented in the README
and both exercised in CI at least to the point of `docker compose config`
and `systemd-analyze verify`.

## Amendment (8 September 2026): raw files first

Implemented with one refinement. `sentinel-scan` and `sentinel-probe` are
not wrapped as libraries writing to SQLite; they keep writing their
append-only JSONL files, and `observer-collector` tails those files into
SQLite with byte-offset cursors and idempotent keys. This keeps the raw
record and the derived store separate (R8's pattern: the database can be
deleted and rebuilt from the files), lets the existing devnet scripts run
unchanged, and needs no change to the sentinel binaries beyond the policy
hook and the `-policy` flag. The new services live inside the
`fibre-sentinel` module so they import `internal/scan` and `internal/probe`
without moving them.

## Consequences

- The existing modules stay libraries. The collector and prober import
  `internal/scan` and `internal/probe`, which means those packages either
  move out of `internal/` or the new services live inside the
  `fibre-sentinel` module. Recommendation: move `scan` and `probe` to
  exported packages in `fibre-sentinel` in a small, test-preserving refactor,
  so the JSONL tools and the store-backed services share one implementation.
- Every dashboard number is a query over `probes` or `snapshots`, and the
  README shows the query. This is what makes "no claim that cannot be
  reproduced by a command" achievable.
- The probe policy makes the observer's traffic bounded and publishable
  before activation.
- Multi-region, alerting and operator accounts are unaffected by this design
  and remain phase 2: a second prober with a different `vantage` name writes
  to the same store.

## Alternatives considered

- **Postgres from day one.** Rejected for MVP: adds an operational dependency
  before there is any load. Kept as the migration target.
- **Grafana over Prometheus metrics.** Rejected: the dashboard needs
  per-blob and per-validator rows with provenance, not time series, and a
  public Grafana would expose more surface than a read-only JSON API.
- **Forking sentinel logic into the services.** Rejected by the brief and by
  the verification argument: the differential tests and the devnet
  fault-injection run only prove the code they exercise.
- **Probing only assigned validators.** Rejected for the overview: without
  the reachability probe, a validator with a registered endpoint and no
  recent assignment would have no data at all.

## Open items

- Whether to include the observer's own mocha-5 validator in headline stats
  (`R0-open-decisions.md`, item 4).
- Public RPC choice for the collector: the pinned celestia-core in main
  (v0.41.0) rate-limits heavy RPCs at 20 concurrent; the collector should run
  against an RPC the team controls or with a low concurrency setting.
- Exact byte and request numbers for the policy: `R4-probe-etiquette.md`.
