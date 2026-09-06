> Note for readers of this repository: the "closest prior art" identified below, `plsgiveup/fibre`, is this project's own code (Utku, Huginn Tech). The finding stands as written: no third party ships anything comparable, and the search was run without excluding our own work so the result is honest.

# R3 — Prior art: independent Fibre observers / dashboards for Celestia

Research date: 6 September 2026. All "seen" dates are 2026-09-06 unless stated. Search tools: web search, direct page fetch, GitHub code/repo search. Where a page could not be fetched (JS-only SPA, 403), that is stated rather than guessed.

## 0. Short answer

- **Nobody ships a public, independent Fibre observer/dashboard today.** No hosted page anywhere reports validators' `x/valaddr` endpoint reachability, TLS identity, or shard-serving.
- **Closest thing:** `plsgiveup/fibre` (author "utku | Huginn"), a CLI-only "independent observer" (`fibre-sentinel`) published 3 Sep 2026 and announced on the Celestia forum on 4 Sep 2026. It scans `MsgPayForFibre`, recomputes assignment, probes assigned validators (DNS → TCP → TLS → identity → download → verify), and writes JSONL. No web UI, no public deployment, no metrics endpoint.
- **Celestia core expects the community to build exactly this.** Rachid Chami (CIP-51 author, forum user `chamirachid1`) wrote on 3 Sep 2026 that "the community will generally build dashboards that will: Periodically check whether the provided fibre server addresses, in the `x/valaddr` module are reachable" and "Whether validators are serving the data."
- **Fibre status:** shipped in `celestia-app v10.0.0-corto` (2 Sep 2026) for the internal Corto testnet only. Mainnet Beta is on v9.0.6; docs.celestia.org has no Fibre operator page yet. None of the 18 validator/tooling providers checked mention Fibre.

---

## 1. Existing Fibre monitoring / dashboards / explorers / tooling

### 1.1 Web search queries run

| Query | Result relevant to Fibre observability | Source seen |
|---|---|---|
| `celestia fibre dashboard` | Only generic Celestia dashboards (Grafana 22036, celestiastats.com, celestiabridge.com, Blockworks). No Fibre dashboard. | [search result set](https://grafana.com/grafana/dashboards/22036-celestia2/) |
| `celestia fibre monitor` | Cumulo bridge-monitor, Celestia blog "Introducing Fibre". Nothing Fibre-specific. | [Cumulo-pro/Celestia-monitoring](https://github.com/Cumulo-pro/Celestia-monitoring/blob/main/bridge-monitor/README.md) |
| `celestia fibre explorer` | Celenium, Mintscan, explorers.guru, dipdup celestia-explorer. None mention Fibre. | [celenium.io](https://celenium.io/) |
| `valaddr celestia` | Only celestia-app releases, pkg.go.dev for `x/valaddr/types`, CIP-51. | [pkg.go.dev x/valaddr/types](https://pkg.go.dev/github.com/celestiaorg/celestia-app/v8/x/valaddr/types) |
| `fibre server celestia validator tool` | CIP-51, fibre-da-spec, the forum thread (§4). No third-party tool. | [CIP-51](https://cips.celestia.org/cip-051.html) |
| `celestia fibre grafana` | celestia-app v9.0.4 release notes: "add fibre Grafana dashboard with validator filtering" (operator self-monitoring, see 1.4). | [v9.0.4 release](https://github.com/celestiaorg/celestia-app/releases/tag/v9.0.4) |
| `celestia fibre prometheus` | Node/bridge exporters only (easy2stake, kj89, f5nodes). Nothing Fibre. | [easy2stake/celestia-bridge-metrics](https://github.com/easy2stake/celestia-bridge-metrics) |
| `CIP-51 dashboard celestia` | CIP-51 itself and docs "Data, dashboards, and analytics" page (§3). No CIP-51 dashboard. | [CIP-51](https://cips.celestia.org/cip-051.html) |
| `fibre celestia github` | celestiaorg/celestia-app, celestiaorg/fibre-da-spec (archived). | [fibre-da-spec](https://github.com/celestiaorg/fibre-da-spec) |
| `"fibre" celestia validators "dashboard" OR "monitor" OR "checker" 2026` | Generic validator Grafana/monitoring only. | [Grafana 21116](https://grafana.com/grafana/dashboards/21116-celestia-consensus-validator-node/) |
| `celestia fibre "fibre server" reachability checker validators twitter OR x.com` | Nothing. | none found |
| `celestia corto testnet fibre validators` | Nothing about Corto; only Mocha guides. | none found |
| `site:docs.celestia.org "fibre server"` / `site:docs.celestia.org fibre validator` | No docs page. | none found |

### 1.2 GitHub searches run

| Search | Hits outside celestiaorg | Notes |
|---|---|---|
| code: `MsgPayForFibre NOT org:celestiaorg` | 28 files; all in `DataAvailabilityLayerNovel/celestia-app` (a fork), `ldcss/GADR` (ADR dataset), `reclear-io/llmref` (docs mirror), `trevormil/cosmsg` (msg registry JSON) | No tool code. Seen 2026-09-06. |
| code: `valaddr celestia NOT org:celestiaorg` | 224 hits; the celestia-related ones are the same fork plus chain-registry JSONs (keplr, oraichain, picasso). `rollchains/tiablob` hits are unrelated `valaddr` variable names. | No tool code. |
| code: `"fibre-server" celestia NOT org:celestiaorg` | 1 (fork `server_config.go`) | none |
| code: `FibreProviderInfo NOT org:celestiaorg` | 30; all fork + `trevormil/cosmsg` | none |
| code: `"all-bonded-fibre-providers" NOT org:celestiaorg` | 0 | none |
| code: `"DownloadShard" NOT org:celestiaorg` | 645, all unrelated projects (sdfs, YTSDK, boulder…) except the fork | none |
| code: `fibre org:celenium-io` | 0 | Celenium has no Fibre code yet |
| code: `valaddr org:celenium-io` | 2 (a `chains.js` and a `signal_test.go`) — incidental string matches, not module support | — |
| code: `fibre repo:celestiaorg/docs` | 2: `public/SKILL.md` ("Fibre is an upcoming protocol-level addition to Celestia"), and the Foundation Delegation Program page ("Run Fibre once it is live") | No operator guide in docs yet. [FDP page](https://github.com/celestiaorg/docs/blob/main/app/operate/consensus-validators/foundation-delegation-program/page.mdx) |
| code: `fibre repo:celestiaorg/awesome-celestia` | 0 | — |
| repos: `celestia fibre` | `celestiaorg/celestia-app-fibre` (archived), `iyopan/acqv-fibre` | see 1.3 |
| repos: `celestia fibre pushed:>2026-01-01` | adds `plsgiveup/fibre` (created 2026-09-03), `iyopan/iyopanACQV`, `jonas089/zoda-cu` (CUDA ZODA, not monitoring) | see 1.3 |
| repos: `org:celestiaorg fibre` | `celestia-app-fibre` (archived, default branch `feature/fibre`), `fibre-da-spec` (archived) | No explorer/monitor repo in the org |
| repos: `topic:celestia-fibre` | 0 | — |
| repos: `topic:celestia pushed:>2026-03-01` | 26 repos; none Fibre-related except `iyopan/acqv-fibre` | — |
| repos: `valaddr` | 1 unrelated Rust crate (`f42h/valaddr_rs`) | — |
| repos: `MsgPayForFibre` | 0 | — |
| repos: `fibre-sentinel OR fibre-tlsverify OR fibre-assign` | only astronomy repos | — |

### 1.3 The two third-party Fibre repos that exist

| Repo | What it is | Observer-relevant? | Source |
|---|---|---|---|
| [plsgiveup/fibre](https://github.com/plsgiveup/fibre) | Go monorepo, Apache-2.0, created 2026-09-03, 1 commit visible, pinned to celestia-app commit `0b69316`. Modules: `fibre-tlsverify` (verifies the consensus-key-endorsed TLS cert extension, stdlib only), `fibre-assign` (recomputes row assignment, `ShardMap.Verify`), `fibre-sentinel` (`sentinel-scan` walks CometBFT RPC for `MsgPayForFibre` → `publications.jsonl`; `sentinel-probe` probes assigned validators "layer-by-layer (DNS, TCP, TLS, identity verification, data retrieval)" → `measurements.jsonl`), `fibre-devnet` (multi-node script). Classifications: `HEALTHY`, `FAULT`, `TOLERATED` (grace window, default `-prune-tolerance` 2m30s), `EXPECTED_GONE`. Multi-vantage via `-vantage <name>`. | **Yes — this is the closest prior art.** It is explicitly "An independent observer for Celestia Fibre". But: CLI only, JSONL output, "no web dashboard or public URL", no Prometheus export, no hosted instance. It probes only validators assigned to real publications; it does not enumerate `x/valaddr` for a full-set reachability table. | [README](https://raw.githubusercontent.com/plsgiveup/fibre/main/README.md), [fibre-sentinel README](https://raw.githubusercontent.com/plsgiveup/fibre/main/fibre-sentinel/README.md), seen 2026-09-06 |
| [iyopan/acqv-fibre](https://github.com/iyopan/acqv-fibre) | Go, created 2026-08-08, 3 commits. "Adversary Cost of a Qualifying Verdict" measurement: reads the live bonded set, runs celestia-app's `validator.Set.Assign`, computes how many validators must be silenced (73 / 98% stake) and notes `slash_fraction_downtime = 0.0`. | No. Economic/security measurement, one-off, no endpoint probing, no dashboard. | [repo](https://github.com/iyopan/acqv-fibre), seen 2026-09-06 |

### 1.4 celestiaorg's own Fibre observability (operator-side, not external)

| Item | What it is | Source |
|---|---|---|
| `fibre/cmd/README.md` | Fibre server ships OTEL metrics (`--otel-endpoint`), pprof, Pyroscope; "Pre-built dashboard available at `fibre/dashboards/fibre-dashboards.json`". No health endpoint. Says to verify registration with `celestia-appd query valaddr provider <address>`. | [fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md) |
| PR #7021 "chore(observability): add fibre Grafana dashboard with validator filtering" | Merged 15 Apr 2026. Grafana `fibre.json`, validator template variable on `service_instance_id`, upload/download success-rate stats, p99 latency, amplification factor. Data: OTEL collector → Prometheus. Audience: operators self-monitoring. | [PR #7021](https://github.com/celestiaorg/celestia-app/pull/7021) |
| `fibre/README.md`, `x/valaddr/README.md`, `specs/src/fibre_server.md`, `specs/src/fibre_registry_module.md` | No mention of community dashboards, explorers, or external monitoring. | [fibre/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/README.md), [x/valaddr/README.md](https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md), [fibre_server.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md), [fibre_registry_module.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_registry_module.md) |
| CIP-51 | Status "Implemented", author @rach-id, dated 2026-06-30. "No mentions of monitoring systems, dashboards, observers, or community tooling." Parameter table includes ShardRetention 4h [10m–168h]. | [CIP-51](https://cips.celestia.org/cip-051.html) |
| celestia-app v10.0.0-corto | Released 2 Sep 2026: "The fibre and valaddr modules are enabled by default; build tags are removed", fibre server ships as its own archive, "Arabica is retired in favor of the internal Corto testnet (`corto-1`)". Validators told to run a fibre server and register host; points to "the fibre server guide" (= `fibre/cmd/README.md`; no docs.celestia.org page found). | [v10.0.0-corto](https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.0-corto) |
| Mainnet status | Mainnet Beta docs list celestia-app v9.0.6; no fibre/valaddr/app v10 mentioned. status.celestia.org: v9.0.4 mainnet upgrade 17 Jun 2026, app version 9 activated 24 Jun 2026. | [Mainnet Beta docs](https://docs.celestia.org/operate/networks/mainnet-beta/), [status: v9.0.4](https://status.celestia.org/incidents/01KVB1N9SZSTTJ26MJACEEJK98) |

Facts an observer needs, confirmed from the specs (seen 2026-09-06):
- Registry: `MsgSetFibreProviderInfo{signer: celestiavaloper…, host: "host:port"}` (≤100 chars, no scheme/path). REST `GET /valaddr/v1/fibre-provider-info/{valcons}` and `GET /valaddr/v1/all-bonded-fibre-providers`; CLI `celestia-appd query valaddr providers`. Event `set_fibre_provider_info{validator_consensus_address, host}`. Records GC'd in EndBlock when a validator leaves the set or stays jailed >7 days. ([x/valaddr README](https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md))
- gRPC `celestia.fibre.v1.Fibre`: `UploadShard`, `DownloadShard`. Server TLS: ephemeral keypair, cert carries X.509 extension OID `1.3.6.1.4.1.66463.1.1` with a consensus-key signature over the TLS pubkey and validity window; client verifies against the expected consensus pubkey and chain ID; no mTLS. Errors: `InvalidArgument`, `Internal`, `NotFound`. No rate limiting, no health endpoint. Prune loop runs once per minute; `pruneAt = max(ExpiresAt, created + ShardRetention)`. ([fibre_server.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md))

---

## 2. Validator / tooling providers

Seen 2026-09-06 unless noted. "Fibre?" = any mention of Fibre, valaddr, or fibre server on the page(s) fetched.

| Provider | Celestia tooling found | Fibre? | Monitoring or install guides? | Source |
|---|---|---|---|---|
| kjnodes | Installation/upgrade guides, snapshots, state sync, RPC, **Slashboard**, explorer, restake, peers, Telegram proposal bot | No | Slashboard is monitoring (missed-block/slashing); rest is install/infra | [services.kjnodes.com/mainnet/celestia](https://services.kjnodes.com/mainnet/celestia/) |
| itrocket | RPC/API/gRPC, bridge endpoint, peers/seeds, **peers scanner**, snapshots, **node sync status checker**; docs also list itrocket "consensus signal tracker" and "decentralization" pages | No | Mix: peer scanner and sync checker are monitoring; rest install | [itrocket.net/services/mainnet/celestia](https://itrocket.net/services/mainnet/celestia/), [Mainnet Beta docs](https://docs.celestia.org/operate/networks/mainnet-beta/) |
| nodes.guru | Explorer (celestia.explorers.guru), "Nodes Monitoring" product, setup guide | No | Explorer + generic monitoring product | [nodes.guru/celestia](https://nodes.guru/celestia) |
| Cumulo | Dashboard, guides, endpoints/peers, live peers, snapshot, state-sync, monitoring, RPC/API scan, validator resources, activity tracker, CIP tracker; GitHub `Celestia-monitoring` (blobstream-monitor, bridge-monitor, grafana_consensus) | No | Monitoring (Grafana/Prometheus/Telegram) + guides | [cumulo.pro/services/celestia](https://cumulo.pro/services/celestia/), [Cumulo-pro/Celestia-monitoring](https://github.com/Cumulo-pro/Celestia-monitoring) |
| P-OPS Team | GitHub `celestia-tools` (blobstreamx-installer, grafana), `rpc-monitor`, governance alerter | No | Grafana dashboards + installers | [github.com/P-OPSTeam](https://github.com/P-OPSTeam), [celestia-tools](https://github.com/P-OPSTeam/celestia-tools) |
| Qubelabs | PayForBlob tracker (pfb-tracker.qubelabs.io), bots, faucets | No | Analytics (PFB/gas), not validator monitoring | [search](https://pfb-tracker.qubelabs.io/) |
| Validatus | Medium posts (100 Gbit/s infra, jailing, Lotus upgrade), staking guides | No | Neither; infra marketing/blog | [validatus.medium.com](https://validatus.medium.com/scaling-celestia-mainnet-100-gbits-infrastructure-and-enterprise-rpc-for-rollups-bd8491a951c8) |
| Celenium | Explorer + API + indexer (`celenium-io/celestia-indexer`, `celenium-interface`) | No (0 hits for `fibre` in org code; API validator object has no fibre fields) | Explorer | [celenium.io](https://celenium.io/), [API validators](https://api-mainnet.celenium.io/v1/validators?limit=2) |
| Numia | celestiadata.com analytics, Looker Studio blocks/rollups dashboards, public RPC | No | Analytics | [celestiadata.com](https://celestiadata.com/), [docs resources](https://docs.celestia.org/learn/celestia-101/resources/) |
| Range | `teamscanworks/celestia-monitoring` — tx/block alert rules (LargeTransfer, DoubleSignEvidence…), arabica-2 only, "highly experimental" | No | Monitoring (chain events), not endpoints | [repo](https://github.com/teamscanworks/celestia-monitoring), [Range blog](https://www.range.org/blog/range-celestia-modular-fellowship) |
| Stakin | Staking page, dashboard.stakin.com (delegator rewards), blog | No | Neither | [stakin.com/stake/celestia](https://stakin.com/stake/celestia) |
| Chorus One | Staking page, explainer articles | No | Neither | [chorus.one celestia](https://chorus.one/crypto-staking-networks/celestia) |
| Everstake | Staking page, how-to-stake blogs | No | Neither | [everstake.one/staking/celestia](https://everstake.one/staking/celestia) |
| Polkachu | Installation guide, cheatsheet, snapshots, state-sync, seeds, live peers, **network scan** (open RPC ports) | No (site returned 403 to fetch; checked via search result pages) | Mostly install; network scan is a light form of external probing | [polkachu.com/network_scans/celestia](https://polkachu.com/network_scans/celestia) |
| Lavender.Five | State sync, snapshots, RPC/API/gRPC, seeds, metrics endpoint, cosmovisor, guides | No | Install/infra + metrics docs | [lavenderfive.com/tools/celestia/overview](https://www.lavenderfive.com/tools/celestia/overview) |
| Enigma | Staking, API staking, RPC; DA node-type/geolocation dashboard (per search snippet) | No | Light analytics | [enigma-validator.com](https://www.enigma-validator.com/) |
| Brightlystake | **RPC status** and **gRPC status** checkers at celestia-tools.brightlystake.com | No | **External endpoint monitoring** — the nearest analogue in spirit (probe public endpoints from outside, show status). Page body could not be parsed by fetch. | [celestia-tools.brightlystake.com](https://celestia-tools.brightlystake.com/), [Medium: gRPC status](https://medium.com/@staking7pc/grpc-status-for-celestia-endpoints-190a16f7d741) |
| StakeLab | Listed as validator only; no tooling page found | No | none found (query: `StakeLab celestia services`) | — |

Other community tools surfaced by awesome-celestia (none mention Fibre): DTEAM bridge checker, STAKEME exploreme.pro (has an "Uptime" tab), Smart Stake analytics, F5 Nodes celestia-collector + public Grafana, Openbitlab Telegram validator monitor, Chainode CelestiaTools (Grafana validator dashboard + bridge exporter). ([awesome-celestia](https://github.com/celestiaorg/awesome-celestia), [Chainode/CelestiaTools](https://github.com/Chainode/CelestiaTools))

---

## 3. docs.celestia.org "Data, dashboards, and analytics"

URL: https://docs.celestia.org/learn/celestia-101/resources/ (seen 2026-09-06). The older `/learn/resources` path returns 404.

| Section | Entry | Description on page |
|---|---|---|
| Useful links | celestia.org/learn, glossary, celestia-app specs (celestiaorg.github.io/celestia-app), awesome-celestia | — |
| Data analytics & dashboards | Celenium | "Blockchain explorer and analytics platform for Celestia" |
| | Celestia Data (celestiadata.com) | "Comprehensive analytics and metrics for the Celestia network" |
| | L2BEAT DA page | "Data availability tracking and metrics by L2BEAT" |
| | ProbeLab (probelab.io/celestia) | "Network monitoring and topology analytics for Celestia" |
| Bridge & node data | Bridge node data dashboard (Looker Studio) | — |
| Research & analytics | Blockworks research (paid); Numia blocks dashboard; Numia rollups dashboard (Looker Studio) | — |
| Rollup tracking | rollup.wtf | "Rollup ecosystem tracking and analytics" |

**Fibre coverage: none.** No entry mentions Fibre, valaddr, or CIP-51. Tone: plain, one-line descriptions, no marketing copy.

The Mainnet Beta page's analytics list is: AlphaB, itrocket consensus signal tracker, itrocket decentralization, kjnodes Slashboard; explorers: Celenium, explorers.guru, Celestia Valopers, NodeStake, ITRocket, Mammoblocks, Mintscan. No Fibre. ([Mainnet Beta](https://docs.celestia.org/operate/networks/mainnet-beta/))

---

## 4. Forum

Forum search (`forum.celestia.org/search.json?q=fibre`, seen 2026-09-06) returns only two Fibre threads, both by `utku` in the Research category (id 5):

| Thread | Created | Posts |
|---|---|---|
| [Fibre: ShardRetention, and what happens when an assigned validator doesn't serve](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288) | 2026-09-02 | 5 |
| [Rate limiting Fibre's read path without IP allowlists](https://forum.celestia.org/t/rate-limiting-fibres-read-path-without-ip-allowlists/2295) | 2026-09-04 | 1 |

(Other hits — "On Data Availability Pricing", "TIA future state…" — match "fibre" only incidentally.)

### 4.1 Thread 2288 (posts via `/t/2288.json`)

| # | Author | Date (UTC) | Content |
|---|---|---|---|
| 1 | utku | 2026-09-02 16:38 | Reading `specs/src/fibre_server.md` and CIP-51 "ahead of Fibre going live"; asks about ShardRetention, what happens when an assigned validator doesn't serve, and rate limiting on `DownloadShard`. |
| 2 | chamirachid1 (Rachid CHAMI) | 2026-09-03 19:34 | Confirms ShardRetention added to the CIP-51 parameter table ([commit](https://github.com/celestiaorg/CIPs/commit/6b9c74f21dd3af85d96481f3a5ad8384a533f568)). "we rely on an honest majority to serve the data for the specified period of time"; only 1/3 of the set needs to be honest to retrieve. **On dashboards:** "the community will generally build dashboards that will: Periodically check whether the provided fibre server addresses, in the `x/valaddr` module are reachable" and "Whether validators are serving the data." |
| 3 | utku | 2026-09-04 11:00 | Says he built an external verifier that watches for `MsgPayForFibre`, performs "shard assignment for the promise height, and probes only the validators actually assigned rows." Measured "p50 ~14ms, p95 ~24ms for dial plus DownloadShard plus full row verification"; prune lands at "pruneAt + ~1m45s in practice". Links code: [github.com/plsgiveup/fibre](https://github.com/plsgiveup/fibre). |
| 4 | chamirachid1 | 2026-09-04 16:03 | Encourages a formal write-up on rate limiting. |
| 5 | utku | 2026-09-04 20:33 | Links thread 2295. |

Identity notes: forum profile for `chamirachid1` shows name "Rachid CHAMI" ([profile JSON](https://forum.celestia.org/u/chamirachid1.json)); CIP-51's author is `@rach-id` ([CIP-51](https://cips.celestia.org/cip-051.html)) — same person, i.e. a Celestia core statement. `utku`'s profile name is "Utku | Huginn" ([profile JSON](https://forum.celestia.org/u/utku.json)); Huginn appears in search as a Turkish validator community from Celestia's testnet era; X handle `@pls_giveup` matches the GitHub user ([twicopy](https://twicopy.com/en/pls_giveup/)).

### 4.2 Thread 2295 (utku, 2026-09-04, no replies as of 2026-09-06)

Argues for classifying `DownloadShard` requests rather than per-IP limits. Directly relevant statements:
- "Honest external observers, tools that re-derive shard assignment from chain state and deliberately query validators (including off-window, where `NOT_FOUND` is the correct answer) to check they're actually serving what they signed for."
- "That lets a validator pass every check a monitor throws at it while quietly dropping real readers … Good external observability is a reason not to special-case the observer's traffic." (i.e. he argues **against** allowlisting observer IPs.)
- "The tolerance for a missing shard also has to account for real pruning lag. I measured prune landing at `pruneAt + ~1m45s` … Without that, honest late prunes get flagged as something worse."

### 4.3 Anyone else announcing a dashboard?

None found. Queries: `site:forum.celestia.org fibre`, `forum.celestia.org fibre rate limiting utku`, `"fibre" celestia validator "reachab" OR "endpoint" monitoring community dashboard forum discord september 2026`. Blog post "Introducing Fibre" (Al-Bassam & Lubov, 13 Jan 2026) has no mention of monitoring or dashboards ([blog](https://blog.celestia.org/introducing-fibre-1tb-s-of-blockspace/)).

---

## 5. UI conventions to borrow

### 5.1 Celenium — celenium.io/validators (page + source, seen 2026-09-06)

| Aspect | Observation |
|---|---|
| Tabs | Active / Inactive / Jailed, chosen via a dropdown. |
| Columns (in order) | Validator · Voting Power (tooltip with staking share % for active) · Outgoing Rewards · Commissions · Rate · Max Rate · Max Change Rate · Version |
| Sorting | None; API order (voting power desc). |
| Pagination | 20 per page; first/prev/next/last buttons and "page N of M". |
| Uptime | **No uptime column on the list.** Per-validator uptime exists in the API (`/v1/validators/{id}/uptime` → `uptime` string plus a `blocks[]` of `{height, signed}`), and the validator page uses a radar chart ("Block missed", "Commission", "Operation time", "Self delegation", "Votes") comparing against Top 25/50/100. |
| Status colour | CSS variables `--validator-jailed`, `--validator-active`, `--validator-inactive` (the latter two commented out in source). Jailed tooltip: "This validator is jailed and cannot propose or sign blocks". |
| Detail page | Stats: Voting Power, Outgoing Rewards, Commissions, Commission Rate, Max Rate, Max Change Rate, Min Self Delegation, Version, Delegator/Consensus address with copy buttons, Identity. Tabs: Delegators · Proposed Blocks · Jails · Votes · Signals · Messages. |
| API validator object fields | `id, version, cons_address, moniker, website, identity, contacts, details, rate, max_rate, max_change_rate, min_self_delegation, stake, rewards, commissions, voting_power, jailed, messages_count, creation_time, address.hash, delegator.hash`. No endpoint/host field. |

Sources: [pages/validators.vue](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/pages/validators.vue), [ValidatorOverview.vue](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/validator/ValidatorOverview.vue), [ValidatorMetrics.vue](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/validator/ValidatorMetrics.vue), [API](https://api-mainnet.celenium.io/v1/validators?limit=2), [uptime API](https://api-mainnet.celenium.io/v1/validators/1/uptime?limit=5).

Takeaway: Celenium keys everything on `cons_address` / valoper and moniker; a Fibre table that adds "Host (x/valaddr)" next to Version would slot in naturally.

### 5.2 Mintscan — mintscan.io/celestia/validators

The page is a JS SPA; direct fetch returned only "Mintscan" (seen 2026-09-06), so observations are from secondary sources:
- List: "active and inactive validators, sorted by voting power. Each row shows the validator name, commission rate, voting power share, and uptime indicators" ([atom-cosmos guide](https://atom-cosmos.com/blog/cosmos-explorer-mintscan-complete-beginner-friendly-guide)). List header also shows cumulative voting power, total delegators and 24h changes ([search snippet](https://www.mintscan.io/cosmos/validators)).
- Detail page: an **Uptime** block labelled "Recently 100 Blocks" with **Signed / Proposed / Missed** counts ([search snippets of validator pages](https://www.mintscan.io/cosmos/validators/cosmosvaloper18kz84x6fv7ep7xvme8x2e4254t404efvhjqst8)). Colour of the grid not verifiable from fetched text.

### 5.3 tenderduty — github.com/blockpane/tenderduty (README, docs/README.md, example-config.yml, seen 2026-09-06)

| Aspect | Observation (quotes from docs) |
|---|---|
| Layout | Dense status table, one row per validator/chain; "Designed intentionally for maximum density for validators on a lot of chains"; dark/light modes; optional live log stream. |
| Block grid | "The last 512 blocks are displayed on the status grid." Grid "heavily influenced by the uptime display on ping.pub". |
| Colours | "Signed blocks aren't highlighted. This should be the nominal state." Missed = **bright orange**; proposed = **bright green/yellow**. Distinguishes "missed" where a pre-commit was sent but not included ("might indicate peering issues"). |
| Time windows / thresholds (config) | `consecutive_missed: 5` (critical); `percentage_missed: 10` within a window (warning); `stalled: 10m` (no new blocks); node down after 3 min; alerts for inactive / jailed / tombstoned. |
| Outputs | Web dashboard on :8888 (option to hide logs for public deployment), Prometheus exporter on :28686, PagerDuty/Discord/Telegram/Slack, dead-man's-switch healthcheck. |

Sources: [README](https://raw.githubusercontent.com/blockpane/tenderduty/main/README.md), [docs/README.md](https://raw.githubusercontent.com/blockpane/tenderduty/main/docs/README.md), [example-config.yml](https://raw.githubusercontent.com/blockpane/tenderduty/main/example-config.yml). (ping.pub's Celestia uptime page returned 404 on both `/celestia/uptime` and `/celestia/uptime/overview`.)

### 5.4 Other explorers checked (for column vocabulary)

- explorers.guru (nodes.guru): `#` · Validator · Voting Power · Cumulative Share · Delegators · commission; Active/Inactive tabs; sorted by voting power; minimal colour; "Slash Events" link; no uptime on the list. ([page](https://celestia.explorers.guru/validators))
- STAKEME exploreme.pro: separate "Uptime" utility tab in nav. ([page](https://celestia.exploreme.pro/))

### 5.5 Conventions to adopt (derived from the above)

- Row identity: moniker + valoper + consensus address (copy buttons), sorted by voting power; Active/Inactive/Jailed filter; ~20 rows/page.
- "Nominal = unhighlighted; fault = one strong colour" (tenderduty). Use one colour for FAULT, muted for TOLERATED/EXPECTED_GONE, none for HEALTHY.
- Fixed-size recent-window grid (Mintscan 100 blocks, tenderduty 512) rather than free time pickers; label the window explicitly ("last N probes" / "last 24h").
- Explicit threshold semantics in config (consecutive vs percentage-in-window) — mirrors tenderduty and avoids arguing about "uptime %".
- The forum-established classification vocabulary — `HEALTHY / FAULT / TOLERATED / EXPECTED_GONE` and a prune-lag tolerance (~1m45s observed, 2m30s default) — is already public; reusing it makes results comparable with `fibre-sentinel`.

### 5.6 Celestia docs tone

Plain second-person, no adjectives, short sentences. Examples (seen 2026-09-06): "This tutorial will guide you through setting up a validator node on Celestia. Validator nodes allow you to participate in consensus in the Celestia network." ([validator node](https://docs.celestia.org/operate/consensus-validators/validator-node/)); "Metrics are a powerful tool for monitoring the health and performance of a system. Celestia provides support for metrics to make sure, as an operator, your system continues to remain up and running." ([metrics](https://docs.celestia.org/operate/consensus-validators/metrics/)). Resource pages are one-line descriptions with no ranking or endorsement. Marketing language lives on celestia.org, not docs.

---

## 6. Verdict

**No one ships an independent, public Fibre observer.** As of 6 September 2026:

1. Fibre is only live on the internal Corto testnet (`celestia-app v10.0.0-corto`, 2 Sep 2026); Mainnet Beta is on v9.0.6 with no `x/valaddr` registrations to probe yet.
2. The only external Fibre observation code in existence is `plsgiveup/fibre` / `fibre-sentinel` (utku | Huginn, 3 Sep 2026): a CLI that scans `MsgPayForFibre`, recomputes assignment, probes assigned validators through DNS/TCP/TLS/identity/download/verify and logs JSONL. It has no web UI, no hosted instance, no metrics endpoint, and does not present a per-validator reachability table from `x/valaddr`. It is the closest prior art and a plausible dependency (its `fibre-tlsverify` and `fibre-assign` are `go get`-able, Apache-2.0).
3. Celestia core (Rachid Chami, CIP-51 author) explicitly expects "the community" to build dashboards that check `x/valaddr` reachability and shard serving; nothing on docs.celestia.org, awesome-celestia, or the resources page lists one.
4. celestiaorg's own Fibre Grafana dashboard (PR #7021) is operator self-monitoring from OTEL metrics, not external observation.
5. None of the 18 validator/tooling providers checked mention Fibre. The nearest analogue in product shape is Brightlystake's RPC/gRPC status checker (external probing of endpoints, tabular status) and Polkachu's network scan; neither touches Fibre.

Gap our project fills: a hosted table keyed on the bonded set × `x/valaddr` host, with per-validator reachability, TLS-identity verification against the consensus key, and assignment-aware shard-serve checks, using the classification vocabulary already established in the forum.
