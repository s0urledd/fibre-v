# R11 — Ecosystem delta since R3: who else builds on Fibre, and what changed upstream

Research date: 18 September 2026. Baseline: `R3-prior-art.md` (6 September 2026). This note records only what is new or different since that baseline, plus the changes it forces on this observer. Search tools: web search, direct page fetch, GitHub repo/code search via the public search pages, forum JSON. Where a claim could not be verified it says so.

## 0. Short answer

- **No third party has published a Fibre observer, exporter, dashboard or explorer integration since 6 September.** The only third-party Fibre repositories on GitHub are still `iyopan/acqv-fibre` (plus its sibling `iyopan/iyopanACQV`), a one-shot "adversary cost of a qualifying verdict" computation with no probing. Every "new" repository the searches surface is our own lineage (`plsgiveup/fibre`, `s0urledd/fibre-v`).
- **Everything new is on the Celestia side**, and several items change what this observer must do: `celestia-app v10.1.0-mocha` (17 September) with Fibre "on Mocha this week, Mainnet Beta a few weeks after"; an open `DownloadShardStream` RPC; server connection caps of 16 connections × 13 streams; an object-storage backend; new server metrics; a validator setup docs page in preview; and celestia-node and lumina Fibre client support.
- **Nobody measures what we measure.** The official Grafana dashboard's "DownloadShard success rate" is the server's own report over the requests it handled; an unreachable server emits nothing, and no official tool knows which validator owed which rows. `tools/fibre-reader` downloads whole blobs and counts any error as a failure without a validator label. The forum's only operator write-up (TTT-VN, 12 September) is a checklist, not code.

## 1. Third-party repositories, explorers, operators (all unchanged)

| Query family | Result | Seen |
|---|---|---|
| GitHub repos `celestia fibre created:>2026-09-01`; `fibre pushed:>2026-09-06 celestia` | 0 new; only our own `s0urledd/fibre-v`, `celestiaorg/celestia-app`, `celestiaorg/go-square` | 2026-09-18 |
| GitHub repos/code `fibrescope`, `fibre observer`, `fibre-probe`, `fibre-sentinel`, `fibre-watch`, `fibre-exporter` | optics and medical repos only | 2026-09-18 |
| GitHub code `"celestia.fibre.v1" NOT org:celestiaorg`, `DownloadShard`, `MsgPaymentPromiseTimeout`, `AllBondedFibreProviders`, `"tx valaddr set-host"`, `fibre_server_download_shard` | one plain fork (`DataAvailabilityLayerNovel/celestia-app`, 0 stars) and `trevormil/cosmsg`, a generic Cosmos message catalogue that lists the Fibre types | 2026-09-18 |
| Celenium `api-mainnet.celenium.io/v1/enums` (127 message types), `api-mocha.celenium.io/v1/enums` (153) | no PayForFibre, valaddr, SetHost, PaymentPromise or escrow entries; no fibre PRs or issues in `celenium-io/celestia-indexer` | 2026-09-18 |
| Mintscan, Numia, Range, Modular Cloud, Blobscan | no Fibre pages, fields or code; Blobscan is Ethereum-only. Mintscan is a SPA and could not be inspected beyond absence of evidence | 2026-09-18 |
| Operator repos and sites: kj89/testnet_manuals (pushed 7 September), P-OPSTeam, Cumulo-pro, Chainode, Polkachu, ITRocket, Nodes.Guru, Kiln, Chorus One, Stakin, Everstake, Imperator, Lavender.Five, Cosmostation | no Fibre content; grafana.com has only the generic Celestia dashboards (16610, 21116, 18723, 22070, 21625, 18459) | 2026-09-18 |
| X, YouTube, Modular Summit, Telegram `@CelestiaOrg` | only the January announcement tweet | 2026-09-18 |
| Forum `fibre order:latest` | three Fibre topics: 2288, 2295 (no replies since 4 September), and **2299 (new, 12 September)** | 2026-09-18 |

**Forum 2299**, "Fibre from the validator side (v10 lab) — operator runbook & readiness checklist", by TTT-VN: three processes (appd, fibre, privval), ports, a systemd unit (`StateDirectory=celestia-fibre`, `LimitNOFILE=65535`, `Requires=celestia-appd.service`), five health checks (process up, port 7980 listening, `valaddr` host registered, shard I/O observed, filesystem under 80%). Rachid's reply the same day: TLS for the privval endpoint "in the upcoming month"; TmKMS and Horcrux both support `SignRawBytes`; a disk-occupancy metric is "for the operator to figure out"; port 7980 "should be exposed. We're currently working on rate limiters for this"; DNS names recommended over IPs in `set-host`. No repository, dashboard or exporter accompanies it.

## 2. Upstream changes that matter to this observer

### 2.1 Release and activation

- **`celestia-app v10.1.0-mocha`**, released 2026-09-17, for `mocha-5`. Fibre ships as its own archive (`fibre_Linux_x86_64.tar.gz`) versioned in lockstep. Validator instruction: `celestia-appd tx valaddr set-host <public-host>:7980 --from <key> --chain-id mocha-5`; verify with `celestia-appd query valaddr providers`.
- **status.celestia.org, 2026-09-15** "Mocha and Mainnet Beta Fibre Requirements Sheet": "V10 along with Fibre on Mocha should go live this week and Mainnet Beta a few weeks after that." Both start at 148 MB/s; Mainnet Beta "bump to 2.2GB/s sometime in November". The linked sheet: 2 TiB full-stake budget, 4 h retention, per-validator ingress and storage scaled by stake (1% stake → 0.053 Gbps, 79.5 GB). No SLA, uptime or monitoring expectation is stated anywhere in it.
- **No v10 activation height for `mocha-5` was published** as of this date (status page, networks repo, Telegram all checked).
- **Mainnet** is on v9.0.8 (15 September); no Fibre.
- **Pin check.** `git ls-remote` shows `v10.1.0-mocha` and `v10.1.0-corto` are the same commit, `fa5b523b7e3b2b83bd16bc072a45cbd3819fa369`, which is the commit this observer pins. `fibre/protocol_params.go` and `fibre/blob.go` were also fetched at both refs and diffed: identical. The assignment constants (`ParamsV10BlobV0`) and the TLS endorsement code the observer recomputes from are therefore exactly what Mocha runs; `go.mod` keeps the `-corto` tag because a tag rename with the same commit changes nothing but the module checksum.

### 2.2 Protocol and server changes (celestia-app PRs after 6 September)

| Change | State | Effect on this observer |
|---|---|---|
| #7857 `feat(proto): add fibre DownloadStream`: `DownloadShardStream` returns a `ShardHeader` then `BlobRow` messages so the server never holds a whole shard in memory | open, 16 September | A second read RPC. The prober must eventually try both, record which one a host serves, and the taxonomy must not turn "unimplemented stream RPC" into a fault |
| #7841 configurable connection caps: `max_connections` default 16, `max_concurrent_streams` default 13; README: worst-case RAM ≈ 16 × 13 × 132 MiB | merged, 14 September | A busy validator may legitimately refuse a probe's connection. We already file `TCP_REFUSED` and `RPC_UNAVAILABLE` as `UNREACHABLE` (held out). But the probe opens two connections (certificate read, then download), so it occupies two of sixteen slots where a client occupies one; folding the identity check into the download connection halves our footprint |
| #7793 bound `DownloadShard` request size in the server codec | merged, 9 September | none: our requests are small |
| #7861 / #7863 downloads without a keyring | merged, 16 September | none: we use our own client |
| Object storage backend (S3/R2): #7786, #7792, #7794, #7795, #7797, #7798, #7808 (open), #7828–#7840; audit tracker #7827 | in progress | Serve latency will vary by backend; a NOT_FOUND may come from an object-store miss ("missing payloads" is an explicit outcome in #7810). That is still the validator's data loss and still a fault. Throughput over the download step alone (PR #22) is the right basis for comparing backends |
| #7810 backend GET pressure metrics: duration, in-flight reads, bytes; outcomes success, missing payloads, timeouts, cancellation, throttling, other errors | merged, 11 September | operator-side only |
| #7821 (issue): store put/get histogram buckets stop at 1 s | open | the official latency histograms saturate at 1 s; ours do not |
| #7851, #7852, #7859 dashboard: `observability/docker/grafana/dashboards/fibre.json` resynced (43 panels), rate-interval fixes, validator dropdown from the storage-budget gauge | merged, 15–17 September | Correction to R3: the dashboard lives at `observability/docker/grafana/dashboards/fibre.json`; there is no `fibre/dashboards/` directory on main |
| #7842 mTLS for the fibre signer gRPC | open | privval side; none |
| #7854 two-validator local devnet; #7869 talis `fibre-throughput --successful-only` | merged | test tooling; none |

**Server error semantics** (`specs/src/fibre_server.md`, current): `DownloadShard` returns `InvalidArgument` (bad blob ID or version), `NotFound` (no shard), `Internal` (store read failure). Verbatim: "The implementation does not currently return FailedPrecondition, PermissionDenied, AlreadyExists, or ResourceExhausted." Uploads do return `ResourceExhausted` under ADR-029. Prune runs once a minute at `max(ExpiresAt, creation_timestamp + ShardRetention)`. Consequence: our `THROTTLED` class has no producer today; it exists for the rate limiter Rachid says is being built, and stays.

### 2.3 What the official tools measure

- **Server metrics** (`fibre/server_metrics.go`, verbatim instrument names): `fibre.server.upload_shard.{in_flight,duration,bytes,rejected,occupancy_bytes,budget_bytes}`, `fibre.server.download_shard.{in_flight,duration,bytes}` with attributes `success` and `shard_size`, `fibre.server.store.{put,get}.duration`, `fibre.server.sign.duration`, `fibre.server.prune.{entries,duration}`. Push-only over OTLP/HTTP (`--otel-endpoint`); no Prometheus scrape endpoint and no health or readiness endpoint. `--pprof` on localhost:6060, `--pyroscope-endpoint`.
- **Client metrics** (`fibre/client_metrics.go`): `fibre.client.upload.*`, `fibre.client.upload_to.{duration,rpc_latency}` and `fibre.client.download_from.{duration,rpc_latency}` carry a `validator_address` attribute.
- **Grafana `fibre.json`**: "Server DownloadShard Success Rate", "Per-Validator Success Rate by Validator", "Prune Health", over `fibre_server_download_shard_duration_seconds_count{success}` and friends. This is the validator's own report of the requests it handled: an unreachable server produces no series, and nothing in it knows which rows a validator was assigned or whether it signed for them.
- **`tools/fibre-reader`** ("a third-party reading simulator"): subscribes to new blocks, shards blobs across reader instances by `commitment[0:8] % reader_count`, trail-only, calls `fibreClient.Download(ctx, blobID)` for the whole blob. Metrics `fibre_reader.blobs_seen/owned/skipped_not_owned`, `downloads_success/failed`, `downloaded_bytes_total`, `download_latency_ms`, `e2e_latency_ms`, `inclusion_to_download_latency_ms`. Any error is a failure; no validator label; no not-found versus unreachable split.
- **`tools/talis`** (`fibre.md`): load-test orchestration, per-block throughput JSONL; `fibre-txsim`: upload load only.
- **celestia-node #5220 "add fibre support"** (merged 8 September): Submit/Upload/Download and escrow operations; metrics `fibre_upload_duration_seconds`, `fibre_submit_duration_seconds` with `error_type` in {unavailable, timeout, canceled, unknown}. Upload side only.
- **lumina `celestia-fibre` crate 1.1.0-rc.1** (15 September): `FibreClient`, `ValidatorConnector`, `HostRegistry` traits; a throughput evaluation harness against mock validators (#1019, no-merge). No probe or health API.

### 2.4 Docs

- **docs PR #2596** "add Mocha Fibre validator setup" (rach-id, 18 September, draft, blocked on celestia-app #7875). Preview: install, ports (9090 app gRPC, 9098 core gRPC, 26669 privval gRPC, 7980 public), activation check via `/abci_info` `app_version >= 10`, `set-host`, a troubleshooting table. No metrics, health or uptime guidance; no serving obligation is stated.
- **docs PR #2578** "draft Fibre v10 operator guide" (jcstein, 27 August, draft): the only official text naming what to watch: `fibre.server.sign.duration` (median signing at or under 10 ms), `fibre.server.store.put/get.duration`, `cometbft_privval_signing_latency_*`, disk against the derived budget, "server availability and upload failures".
- The live docs.celestia.org Fibre path still returns 404.

## 3. Methodology comparison

| Source | Unit rated | Vantage | Unreachable vs not found | Assignment-aware | Signatures |
|---|---|---|---|---|---|
| Official server metrics and `fibre.json` | each RPC the server handled, self-reported `success` | the validator itself | no: an unreachable server emits nothing | no | no |
| `tools/fibre-reader` | each whole-blob `Download()`; any error is a failure | one reader host, shardable | no; no validator label | no | no |
| celestia-node Fibre metrics | each upload or submit | the client | `error_type` on uploads only | no | no |
| `iyopan/acqv-fibre` | a static threshold (validators to silence) | one, one-shot | not applicable | yes (`Set.Assign`) | no |
| TTT-VN runbook (forum 2299) | operator self-checks | own host | reachability only | no | no |
| Explorers | none | none | none | none | none |
| **This observer** | one observation per proven obligation, judged by its newest in-window probe; per-probe rate published beside it | external, one vantage today, schema carries `vantage` for more | yes: dial, TLS, gRPC `NotFound`, `Internal`, rate limit and identity are separate classes; unreachable is never a fault; prune lag tolerated | yes (`fibre-assign` recomputation, `ShardMap.Verify`) | yes (`fibre-tlsverify` endorsement, settled-promise signatures) |

## 4. Changes this forces, in priority order

1. **No re-pin needed for Mocha.** `v10.1.0-mocha` is the pinned commit under a second tag (section 2.1). The pin must be re-checked at the next tag that is not `fa5b523`, and the observer's `pin_status` check will say `chain_ahead` on its own if Mocha upgrades past app version 10.
2. **One connection per probe.** With `max_connections` at 16, the probe's two connections (certificate read, then verifying download) take two slots. Fold the identity check into the download connection's `VerifyConnection` callback and keep per-layer timings from the handshake callbacks. Reduces our footprint on a busy validator by half and removes the "second backend behind the same host" ambiguity noted in `probe.go`.
3. **`DownloadShardStream`.** When #7857 lands: probe the unary RPC first; on `Unimplemented`, try the stream; record which one answered on the row (`rpc_kind`), and never file `Unimplemented` on one of them as a fault while the other serves.
4. **Read the last known host from `registry.jsonl` in the prober**, so a jailed validator's remaining obligations keep being probed at the host it registered after a prober restart, instead of falling to `NOT_REGISTERED`. Independent of upstream, found during PR #22.
5. **Do nothing** about `THROTTLED` (no producer yet, rate limiter announced), object-store misses (already a fault, correctly), or the official dashboards (operator-side, not comparable).

## 5. Sources (seen 2026-09-18)

- Forum 2299: https://forum.celestia.org/t/2299
- status.celestia.org, 15 September: https://status.celestia.org/incidents/01M2JMQ568VR39JYH97QFKGT02
- Requirements sheet: https://docs.google.com/spreadsheets/d/1P3k-KQrZxbRjRwIxWkJBNwgAFlFMJUdT7OupA_WoKa0
- Release v10.1.0-mocha: https://github.com/celestiaorg/celestia-app/releases/tag/v10.1.0-mocha
- Docs previews: https://celestiaorg.github.io/docs-preview/pr-2596/operate/consensus-validators/fibre/ and https://celestiaorg.github.io/docs-preview/pr-2578/operate/consensus-validators/fibre-server/
- Metrics: https://github.com/celestiaorg/celestia-app/blob/main/fibre/server_metrics.go , https://github.com/celestiaorg/celestia-app/blob/main/fibre/client_metrics.go , https://github.com/celestiaorg/celestia-app/blob/main/observability/docker/grafana/dashboards/fibre.json
- fibre-reader: https://github.com/celestiaorg/celestia-app/tree/main/tools/fibre-reader
- DownloadStream PR: https://github.com/celestiaorg/celestia-app/pull/7857
- Connection caps PR: https://github.com/celestiaorg/celestia-app/pull/7841
- celestia-node Fibre: https://github.com/celestiaorg/celestia-node/pull/5220
- lumina crate: https://docs.rs/celestia-fibre/latest/celestia_fibre/
- acqv-fibre: https://github.com/iyopan/acqv-fibre
- Pin diff: `fibre/protocol_params.go` and `fibre/blob.go` at `v10.1.0-mocha` and at `fa5b523b7e3b2b83bd16bc072a45cbd3819fa369` (raw.githubusercontent.com), identical.
