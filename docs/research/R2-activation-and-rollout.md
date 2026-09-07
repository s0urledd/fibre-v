# R2 — Fibre activation state on mocha-5 and rollout facts

Research date: all sources below were read on **2026-09-06** unless a different "seen" date is noted. Only statements found in the linked sources are recorded; nothing here is inferred about dates. Where a source could not be found, the item is marked **unknown** with a note on what would resolve it.

Access note: the GitHub API was not reachable from this session for `celestiaorg/*` repositories, so GitHub facts come from `raw.githubusercontent.com` file fetches, `github.com` HTML pages, and release `checksums.txt` downloads. Forum facts come from the Discourse JSON API (`forum.celestia.org/t/<id>.json`).

---

## 1. Current state of Fibre on mocha-5

### 1.1 Short answer

As of 2026-09-06 there is **no announced Fibre/v10 activation height, upgrade window, or v10 mocha release** in any public source I could reach. Mocha-5 is running celestia-app **v9.0.6-mocha** (app version 9). v10 (which carries Fibre) exists only as `-corto` pre-release tags for the `corto-1` testnet. The docs say "The next upgrade (v10) has not yet been scheduled."

### 1.2 What each source says

| Source | What it says (verbatim where quoted) | Seen |
|---|---|---|
| [docs: Mocha testnet](https://docs.celestia.org/operate/networks/mocha-testnet/) | Chain-id `mocha-5`; celestia-app **v9.0.6-mocha**; "Mocha-5 is a hardspoon of `mocha-4` at block 13205115". No mention of fibre or v10. | 2026-09-06 |
| [docs: Network upgrades](https://docs.celestia.org/operate/maintenance/network-upgrades/) | "The next upgrade (v10) has not yet been scheduled. This page will be updated when a new upgrade is announced." Page footer: "Last updated on September 3, 2026". Mocha history table ends at v9 (CIP-50, 2026/06/05 08:16:10 UTC, height 11655991, delay 2 days — that is a mocha-4 height). | 2026-09-06 |
| [docs: Networks overview](https://docs.celestia.org/operate/networks/overview/) | "Celestia currently has one public testnet that you can participate in: Mocha testnet". Compatible versions for Mocha: celestia-node v0.32.1-mocha, celestia-app v9.0.6-mocha. "Last updated on September 3, 2026". Corto is not listed. | 2026-09-06 |
| [status: Mocha-5 is live](https://statuspage.incident.io/celestia-org/incidents/qay1t5b2) | Posted Aug 20, 2026: hardspoon of mocha-4 at block 13205115; chain-id mocha-5 from height 1; app v9.0.6-mocha; "DA network will be started next week"; mocha-4 shutdown Sep 1, 2026. No mention of fibre/v10. | 2026-09-06 |
| [status: mocha-5 DA network live, celestia-node v0.32.1-mocha](https://status.celestia.org/incidents/01M1EXTFMDW88T1KGW2AC8FVW8) | Sep 1, 2026 16:46–17:00 (page-local time): DA network live; node v0.32.1-mocha. No mention of fibre/v10. | 2026-09-06 |
| [status: Mocha-4 hardspoon into Mocha-5](https://status.celestia.org/incidents/01KYMP2MTJBS9ZKHRQNT5HCVMQ) | Posted Jul 28, 2026: "On September 1, 2026, at 14:00 UTC, we plan to launch mocha-5." (Actual launch was earlier per the Aug 20 notice above.) No mention of fibre/v10. | 2026-09-06 |
| [status.celestia.org front page](https://status.celestia.org/) | No scheduled maintenance or active incidents. | 2026-09-06 |
| [networks repo, mocha-5/genesis.json](https://raw.githubusercontent.com/celestiaorg/networks/master/mocha-5/genesis.json) | `chain_id: mocha-5`, `genesis_time: 2026-08-18T12:00:00Z`, consensus `version.app = "9"`. `app_state` keys include `signal`, `upgrade`, `blob`, `zkism` etc. but **no `fibre` and no `valaddr`** key. | 2026-09-06 |
| [networks repo, mocha-5 folder](https://github.com/celestiaorg/networks/tree/master/mocha-5) | Files: `genesis.json`, `genesis_hash.txt`, `peers.txt`, `seeds.txt`. No upgrade doc. | 2026-09-06 |
| [celestia-app issue #7768 "Define Fibre configs for mocha"](https://github.com/celestiaorg/celestia-app/issues/7768) | Opened Sep 2, 2026 by rach-id; labels `fibre`, `fibre-mainnet`; open. Body: "We should define which values we want to set for `FullStakeStorageBudget` and `ShardRetention` for mocha and communicate that so validators have time to prepare. These can be changed through gov, so we can update these values onchain after v10 is activated." No comments; no height or date. | 2026-09-06 |
| [celestia-app issue #7769 "Define Fibre configs for Corto"](https://github.com/celestiaorg/celestia-app/issues/7769) | Opened Sep 2, 2026 by rach-id. Body: "Now that we will deploy Fibre to Corto, we need to set values for `ShardRetention` and `FullStakeStorageBudget`. These can be changed through gov, so we can update these values onchain after v10 is activated." | 2026-09-06 |
| [celestia-app tags page](https://github.com/celestiaorg/celestia-app/tags) | Newest tags: `v10.0.1-corto` (Sep 2, 2026, `6e5c64c`), `v10.0.0-corto` (Sep 2, 2026, `69c2d93`), then `v9.0.6`, `v9.0.6-mocha`, `v9.0.6-corto`, `v9.0.6-arabica` (all Aug 17, 2026, `6f4b596`), `v9.0.5-corto`, `v9.0.5-arabica` (Jun 25, 2026), `v9.0.4`, `v9.0.4-mocha` (Jun 13, 2026). **No `v10.*-mocha` tag exists.** | 2026-09-06 |
| [release v10.0.0-corto](https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.0-corto) | Pre-release. "This release is intended for the Corto testnet (`corto-1`). If you are upgrading from v9 please read through the release notes." "v10 introduces **fibre**, a data availability protocol served by validator-operated fibre servers, together with the new `x/fibre` and `x/valaddr` modules." "Validators are recommended to run a fibre server and register their host on-chain; see the fibre server guide." "This release also bumps celestia-core to v0.41.0." Also: "Arabica retired with Mocha chain ID now `mocha-5`" (summary of notes). Release page shows time "02 Sep 13:26" (year not displayed on the page; tags page says 2026). | 2026-09-06 |
| [release v10.0.1-corto](https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.1-corto) | Pre-release, target commit `6e5c64c0e209a1239b1ee32f0a7c8b861fce250a`, page time "03 Sep 05:12 UTC". Single fix: "the multiplexer now forces the inter-block cache on for the embedded v3 app, so the historical v2→v3 signal upgrade replays correctly when syncing from genesis" (PR #7771). OS support: prebuilt Linux amd64/arm64, macOS amd64/arm64; glibc >= 2.38 for `celestia-appd`. | 2026-09-06 |
| [CIP-52 "v10 Network Upgrade" (draft)](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md) | Status **Draft**, created 2026-08-13, author @rach-id, type Meta, requires CIP-45, CIP-46, CIP-51. "This Meta CIP lists the protocol changes included in the v10 network upgrade for Celestia Mainnet Beta. The primary change is CIP-51, which introduces Fibre". "CIP-51 is state breaking and thus requires a breaking network upgrade." Parameter table "from the upgrade height": `consensus.Version.AppVersion` = 10; `fibre.WithdrawalDelay` 24h; `fibre.PaymentPromiseTimeout` 1h; `fibre.PaymentPromiseHeightWindow` 1000; `fibre.ShardRetention` 4h; `fibre.FullStakeStorageBudget` 2 TiB. "The v10 upgrade handler adds the `x/fibre` and `x/valaddr` stores and runs module migrations." No height, no date, no mocha-specific statement. | 2026-09-06 |
| [CIP-51 "Fibre Protocol"](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-051.md) (also at [cips.celestia.org/cip-051.html](https://cips.celestia.org/cip-051.html)) | Status **Implemented**, created 2026-06-30, author @rach-id, discussions-to CIPs PR #400. "Fibre is state-breaking and MUST be activated as part of a coordinated network upgrade. The upgrade MUST enable the `x/fibre` module, the validator provider registry, Fibre data-square construction, and Fibre-compatible binaries." "Consensus nodes must upgrade to Fibre-compatible software before the activation height." No network names or heights. | 2026-09-06 |
| [CIPs commit 6b9c74f](https://github.com/celestiaorg/CIPs/commit/6b9c74f) | Full SHA `6b9c74f21dd3af85d96481f3a5ad8384a533f568`, "docs: correct CIP-51 x/fibre parameter table (#405)", authors rootulp + Claude; 1 file (`cips/cip-051.md`, +8/-5). Removed `fibre.GasPerBlobByte`; added `fibre.ShardRetention`, `fibre.FullStakeStorageBudget`, hardcoded `MaxPromiseClockSkew`; noted `PaymentPromiseRetentionWindow` is derived as `WithdrawalDelay + MaxPromiseClockSkew`. Commit date not displayed on the page; the forum reply linking it is dated 2026-09-03. | 2026-09-06 |
| [docs repo PR #2578 "docs: draft Fibre v10 operator guide"](https://github.com/celestiaorg/docs/pull/2578) | Opened Aug 27, 2026 by jcstein, **Draft**. Described as "preparatory documentation only" with v10 unscheduled and CIP-52 in draft. Listed merge blockers include: CIP-52 finalization; a compatible celestia-app v10 release; "Mocha activation details must be confirmed"; Mainnet Beta upgrade schedule announcement; Fibre pricing/gas curve sign-off. | 2026-09-06 |
| [Forum thread 2288](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288) | Core team reply (Rachid CHAMI, 2026-09-03) — see §4 for quotes. Contains no date or height for Mocha activation. | 2026-09-06 |
| [Blog: Introducing Fibre (Jan 13, 2026)](https://blog.celestia.org/introducing-fibre-1tb-s-of-blockspace/) | "In the near future, the team will roll Fibre out to the Arabica testnet for developers to interact with." Then "testing and preparation to be rolled out to mainnet, in incremental throughput increases". (Arabica has since been retired per the v10.0.0-corto release notes and the node v0.32.1-mocha release.) | 2026-09-06 |
| Arabica sunset announcement (relayed by the owners, 6 Sep 2026; Discord/Telegram text, no public URL) | "Following our announcement on 21 July, the Arabica devnet (arabica-11) will be permanently shut down on 13 August 2026. Arabica is being deprecated due to low usage. Developers should migrate all testing and rollup deployments to Mocha testnet." Faucet: `mocha.celenium.io/faucet` or Discord `#mocha-faucet`. Consequence: the January blog's plan to roll Fibre out to Arabica first is void; Mocha is the first public network Fibre can reach. | 2026-09-06 |
| Task brief statement: Celestia core said on 4 Sep 2026 that Fibre on Mocha is "still some weeks away" | **I could not locate a public URL for this statement.** Forum search for "weeks away" and GitHub org search (`org:celestiaorg "weeks away"`) returned zero results; thread 2288's 4 Sep posts do not contain it. It is most likely from Discord/Telegram (the docs list "Telegram announcement channel", "Discord Mocha announcements" as upgrade channels). Treat as unverified here. | 2026-09-06 |

### 1.3 What "corto" is (from sources only)

| Fact | Source |
|---|---|
| The `celestiaorg/networks` README lists a network table: Mocha (Testnet, `mocha-4`), Arabica (Testnet, `arabica-11`), **Corto (Testnet, `corto-1`)**, Celestia (Mainnet Beta, `celestia`). (Table still says mocha-4, though a `mocha-5/` folder exists.) | [networks README](https://github.com/celestiaorg/networks) (raw fetched 2026-09-06) |
| `corto-1/genesis.json`: `chain_id: corto-1`, `genesis_time: 2026-07-01T14:00:00Z`, consensus `version.app = "9"`. No `corto-1/README.md` (404). | [corto-1/genesis.json](https://raw.githubusercontent.com/celestiaorg/networks/master/corto-1/genesis.json) |
| Tags with `-corto` suffix: `v9.0.5-corto` (Jun 25, 2026), `v9.0.6-corto` (Aug 17, 2026), `v10.0.0-corto`, `v10.0.1-corto` (Sep 2, 2026). | [tags page](https://github.com/celestiaorg/celestia-app/tags) |
| v10.0.x-corto: "This release is intended for the Corto testnet (`corto-1`)." | [v10.0.0-corto](https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.0-corto) |
| "Now that we will deploy Fibre to Corto, we need to set values for `ShardRetention` and `FullStakeStorageBudget`." | [issue #7769](https://github.com/celestiaorg/celestia-app/issues/7769) |
| Corto is **not** on docs.celestia.org (overview says "one public testnet": Mocha). No forum posts mention "corto" (Discourse search returned nothing). | [overview](https://docs.celestia.org/operate/networks/overview/); forum `search.json?q=corto` |

Conclusion from sources: Corto (`corto-1`) is a Celestia testnet, started on app v9 (genesis 2026-07-01), that receives Fibre/v10 first via `-corto` tagged pre-releases. Whether it is public/permissionless, its validator set, or its v10 activation height are **not stated** anywhere I could reach.

### 1.4 The pinned commit

| Fact | Source |
|---|---|
| `0b69316466c3ba02f708c0e2a101f834d5d1827f` = PR #7762 "fix!: count MsgModuleQuerySafe queries against the block message limit", merged **Sep 1, 2026 16:32 UTC** into `main`. | [PR #7762](https://github.com/celestiaorg/celestia-app/pull/7762) |
| `v10.0.0-corto` is 3 commits ahead of `0b69316` (so the pinned commit is an ancestor of the v10.0.0-corto tag): "fix!: count ICA messages in MsgRecvPacket…", "chore(deps): bump celestia-core to v0.41.0 …", "docs: add v10 node operator release notes" (all Sep 2, 2026). | [compare 0b69316...v10.0.0-corto](https://github.com/celestiaorg/celestia-app/compare/0b69316466c3ba02f708c0e2a101f834d5d1827f...v10.0.0-corto) |
| `main` is 6 commits ahead of `v10.0.0-corto` as of Sep 4, 2026 (incl. #7771 inter-block cache fix = v10.0.1-corto, #7766 TLS docs, #7763 ADR-030, #7773, #7772, #7779). | [compare v10.0.0-corto...main](https://github.com/celestiaorg/celestia-app/compare/v10.0.0-corto...main) |

---

## 2. Known vs unknown

| Item | Value | Source | Status |
|---|---|---|---|
| mocha-5 chain-id | `mocha-5` | [docs](https://docs.celestia.org/operate/networks/mocha-testnet/), [genesis](https://raw.githubusercontent.com/celestiaorg/networks/master/mocha-5/genesis.json) | known |
| mocha-5 genesis time | `2026-08-18T12:00:00Z` | [genesis](https://raw.githubusercontent.com/celestiaorg/networks/master/mocha-5/genesis.json) | known |
| mocha-5 hardspoon source height | mocha-4 block 13205115 | [status](https://statuspage.incident.io/celestia-org/incidents/qay1t5b2) | known |
| mocha-5 current celestia-app | `v9.0.6-mocha` (app version 9) | [docs](https://docs.celestia.org/operate/networks/mocha-testnet/), genesis | known |
| mocha-5 genesis has `fibre`/`valaddr` state | No (keys absent) | [genesis](https://raw.githubusercontent.com/celestiaorg/networks/master/mocha-5/genesis.json) | known |
| Fibre carried by which app version | app version 10 | [x/fibre README](https://github.com/celestiaorg/celestia-app/blob/main/x/fibre/README.md), [CIP-52](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md) | known |
| Upgrade CIP for v10 | CIP-52 (Meta), status Draft, created 2026-08-13 | [CIP-52](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md) | known |
| Upgrade name for v10 | none given (CIP-52 title is just "v10 Network Upgrade"; docs table has no name for v10) | CIP-52, [docs](https://docs.celestia.org/operate/maintenance/network-upgrades/) | unknown — resolved when CIP-52/docs add a name |
| v10 mocha release tag | none exists (`v10.0.0-corto`, `v10.0.1-corto` only) | [tags](https://github.com/celestiaorg/celestia-app/tags) | unknown — resolved by a `v10.x.y-mocha` tag |
| v10 activation height on mocha-5 | not announced | [docs](https://docs.celestia.org/operate/maintenance/network-upgrades/) ("has not yet been scheduled") | unknown — resolved by `query signal upgrade` returning a pending upgrade, or a status/docs announcement |
| Mocha upgrade delay after 5/6 quorum | docs: "Typically 1-2 day delays" on Mocha; history table shows 2 days for v6–v9 | [docs](https://docs.celestia.org/operate/maintenance/network-upgrades/) | known (typical), exact for v10 unknown |
| Fibre params on mocha (`ShardRetention`, `FullStakeStorageBudget`) | to be defined; defaults 4h / 2 TiB apply at upgrade unless changed by gov | [#7768](https://github.com/celestiaorg/celestia-app/issues/7768), [CIP-52](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md) | unknown (values); defaults known |
| Corto chain-id / genesis | `corto-1`, genesis 2026-07-01T14:00:00Z, app v9 at genesis | [corto-1 genesis](https://raw.githubusercontent.com/celestiaorg/networks/master/corto-1/genesis.json) | known |
| Corto v10 activation height / whether already upgraded | not stated | — | unknown — resolved by querying a corto-1 RPC (`/signal/v1/upgrade`, header `version.app`) |
| Official Fibre docs page on docs.celestia.org | none published; draft PR #2578 open | [docs sitemap](https://docs.celestia.org/sitemap.xml) (71 URLs, none containing "fibre"), [PR #2578](https://github.com/celestiaorg/docs/pull/2578) | known (absent) |
| "still some weeks away" (4 Sep) source URL | not found | forum + GitHub searches | unknown — resolve by locating the Discord/Telegram message |
| Pinned commit in v10.0.0-corto? | yes (3 commits behind the tag) | [compare](https://github.com/celestiaorg/celestia-app/compare/0b69316466c3ba02f708c0e2a101f834d5d1827f...v10.0.0-corto) | known |

---

## 3. How the Fibre server is packaged in releases

### 3.1 Exact asset names

From `checksums.txt` attached to each release (downloaded 2026-09-06):

**[v10.0.1-corto](https://github.com/celestiaorg/celestia-app/releases/download/v10.0.1-corto/checksums.txt)** and **[v10.0.0-corto](https://github.com/celestiaorg/celestia-app/releases/download/v10.0.0-corto/checksums.txt)** — 12 archives each, plus `checksums.txt`:

```
celestia-app-standalone_Darwin_arm64.tar.gz
celestia-app-standalone_Darwin_x86_64.tar.gz
celestia-app-standalone_Linux_arm64.tar.gz
celestia-app-standalone_Linux_x86_64.tar.gz
celestia-app_Darwin_arm64.tar.gz
celestia-app_Darwin_x86_64.tar.gz
celestia-app_Linux_arm64.tar.gz
celestia-app_Linux_x86_64.tar.gz
fibre_Darwin_arm64.tar.gz
fibre_Darwin_x86_64.tar.gz
fibre_Linux_arm64.tar.gz
fibre_Linux_x86_64.tar.gz
```

SHA-256 for the Linux x86_64 fibre archive: v10.0.1-corto `f618718882970e55f5ce6fc56c51b9764d05e33ff5a16636050aea86a0c34d9b`; v10.0.0-corto `2c1eb0e51c4ec24074c8df75f04ee81320a401b5982e421c147b572ba129af8f`.

**[v9.0.6-mocha](https://github.com/celestiaorg/celestia-app/releases/download/v9.0.6-mocha/checksums.txt)** — 8 archives, **no `fibre_*` assets** (only `celestia-app_*` and `celestia-app-standalone_*`). So the presence of `fibre_*` assets is itself a v10 marker.

The release page reports "Assets 15" for v10.0.x-corto (12 archives + checksums.txt + the two GitHub source archives).

### 3.2 Release workflow (`.goreleaser.yaml` on `main`)

Source: [.goreleaser.yaml](https://github.com/celestiaorg/celestia-app/blob/main/.goreleaser.yaml) (raw fetched 2026-09-06).

- Comment in the file: "The fibre server is a separate binary that validators run alongside celestia-appd. It has no cgo dependencies of its own, but it is built with CGO_ENABLED=1 to match celestia-appd: the two binaries then have the same linking profile and the same libc requirements on a given platform."
- Four builds `fibre-{darwin,linux}-{amd64,arm64}` with `main: ./fibre/cmd`, `binary: fibre`, ldflags `-X main.version={{ .Version }} -X main.commit={{ .FullCommit }} -X main.buildDate={{ .CommitDate }}`.
- Archive id `fibre`, format `tar.gz`, name template `fibre_{{ title .Os }}_{{ x86_64 | arm64 }}` — which yields exactly the names above.
- Other archives: id `multiplexer` → `celestia-app_<OS>_<arch>.tar.gz` (builds with `multiplexer` tag; embeds v3.12.0, v4.1.0, v5.0.12, v6.4.4, v7.0.2-mocha, v8.0.8, v9.0.4 binaries via `scripts/download_binary.sh`), id `standalone` → `celestia-app-standalone_<OS>_<arch>.tar.gz`.
- `checksum.name_template: "checksums.txt"`; `release.prerelease: auto`; `git.prerelease_suffix: "-"` (any tag containing `-`, e.g. `-corto`, `-mocha`, is a pre-release).
- The top-level [README](https://github.com/celestiaorg/celestia-app/blob/main/README.md): "Each release also ships the Fibre server as its own archive (`fibre_Linux_x86_64.tar.gz`, `fibre_Darwin_arm64.tar.gz`, etc.) for the same four platforms, built the same way and versioned in lockstep with `celestia-appd`."
- [Makefile](https://github.com/celestiaorg/celestia-app/blob/main/Makefile): targets `build-fibre-server` (→ `build/fibre`) and `install-fibre-server` (→ `$GOPATH/bin/fibre`), both `go build ./fibre/cmd`.
- [fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md): "The Linux archives are dynamically linked and require **glibc >= 2.34**, so Ubuntu 22.04 and Debian 12 work. This is lower than the **glibc >= 2.38** floor of the multiplexer `celestia-appd` build". Also notes "a Linux arm64 host reports `aarch64` from `uname -m`, but the archive is `arm64`."

---

## 4. Validator's minimal Fibre setup per official sources

There is **no page on docs.celestia.org yet** (sitemap has no fibre URL; docs PR #2578 is a draft). The official operator guide is in the celestia-app repo: **[fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md)**, referenced from the [v10 release notes](https://github.com/celestiaorg/celestia-app/blob/main/docs/release-notes/release-notes.md) as "the fibre server guide". Quotes below are from these two files plus [x/valaddr README](https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md) and [specs/src/fibre_server.md](https://github.com/celestiaorg/celestia-app/blob/main/specs/src/fibre_server.md), all read 2026-09-06.

### 4.1 Prerequisites (fibre/cmd/README.md, "Prerequisites" checklist, verbatim)

- "A `celestia-appd` node runs on the same host (or a trusted host-local network). The server's app link (`--app-grpc-address`) and signer link (`--signer-grpc-address`) are **not** TLS-protected".
- "The chain is on **app version 10 or later**. The `x/fibre` and `x/valaddr` modules the server depends on do not exist in earlier versions."
- "The node's application gRPC endpoint is enabled (default `127.0.0.1:9090`)."
- "The node's privval gRPC endpoint is enabled … If the consensus key lives in an external KMS, the KMS must support the privval `SignRawBytes` message".
- "The fibre listen port (default `7980`) is reachable by clients from outside your network."
- "Your validator is bonded. The server derives its storage budget from your stake; a validator outside the active set gets no budget and no traffic."

### 4.2 Config and addresses

| Setting | Value / default | Source |
|---|---|---|
| Config file | `$FIBRE_HOME/server_config.toml` (default `~/.celestia-fibre/server_config.toml`); precedence "flag > config file > default" | fibre/cmd/README.md |
| Start | `fibre start` (or `fibre start --home /path` / `FIBRE_HOME=...`) | fibre/cmd/README.md |
| `--app-grpc-address` | `127.0.0.1:9090` | fibre/cmd/README.md; fibre_server.md defaults |
| `--server-listen-address` | `0.0.0.0:7980` | same |
| `--signer-grpc-address` | `127.0.0.1:26669` | same |
| Node `config.toml` | `priv_validator_grpc_laddr = "127.0.0.1:26669"` — "The privval gRPC endpoint is enabled by default when running `celestia-appd init` on `127.0.0.1:26669`." | fibre/cmd/README.md "Signing" |
| Release-notes wording | "Every node now runs a privval gRPC endpoint, which the fibre server uses for payment-promise endorsements and its TLS identity. The default listen address moved from `127.0.0.1:26659` to `127.0.0.1:26669`. Nodes that don't serve fibre can disable it by clearing `priv_validator_grpc_laddr` in `config.toml`." | release-notes.md v10.0.0 |
| Shard retention | not a local setting: "The server reads the current value from the app node on every upload (returned by `ValidatePaymentPromise`), so there is no local setting to configure." | fibre/cmd/README.md |
| Signing flow | "1. Fibre connects to the node's PrivValidatorAPI gRPC endpoint (default `127.0.0.1:26669`) 2. Fibre fetches the validator's public key via `GetPubKey` RPC … 3. Payment promises are signed via `SignRawBytes` RPC calls" | fibre/cmd/README.md |
| Observability | `--otel-endpoint` (OTLP/HTTP, traces+metrics), `--pprof`, `--pyroscope-endpoint`; server metrics `fibre.server.upload_shard.*`, `fibre.server.download_shard.*`, `fibre.server.store.*`, `fibre.server.sign.duration`, `fibre.server.prune.*`; Grafana dashboard at `fibre/dashboards/fibre-dashboards.json` | fibre/cmd/README.md |

### 4.3 TLS (verbatim, fibre/cmd/README.md "Transport security (TLS)")

- "The Fibre server↔client link is always TLS-encrypted, and it is fully automatic: there are no certificates to obtain, configure, or renew."
- "On startup the server generates its own certificate and has it endorsed once by your validator's consensus key (through the signer). Clients verify that endorsement against the validator set … no certificate authority involved."
- "Identity is bound to the consensus key, not the network address, so the host you register on-chain can be an IP literal or a DNS name."
- "Downloads are public — any peer can read shards. Uploads are still gated by the payment-promise check."
- "There is no plaintext fallback, so every Fibre server and client on the network must run a TLS-capable build."
- Spec detail (fibre_server.md): TLS 1.3; endorsement carried in X.509 extension OID `1.3.6.1.4.1.66463.1.1` (IANA PEN 66463 = Celestia); "There is no client certificate requirement and no mTLS."

### 4.4 On-chain registration (x/valaddr)

- Command: `celestia-appd tx valaddr set-host 203.0.113.7:7980 --from <validator-account-key>` — "signing with your validator's account key". ([fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md), [x/valaddr README](https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md))
- Host rules: "`host` must be in `host:port` form: a non-empty host part (IP literal or DNS name …) followed by a numeric port in the range [1, 65535]. Schemes (`http://`, `dns:///`, …) and URL paths are rejected." "`host` must be at most 100 characters." Record is keyed by the validator's **consensus** address.
- Verify: `celestia-appd query valaddr provider <celestiavalcons-address>` (consensus address from `celestia-appd comet show-address`); list all bonded: `celestia-appd query valaddr providers`. gRPC `celestia.valaddr.v1.Query`; REST `/valaddr/v1/fibre-provider-info/{validator_consensus_address}` and `/valaddr/v1/all-bonded-fibre-providers`.
- Timing (verbatim): "`set-host` is only accepted once the chain runs app version 10; before the v10 upgrade activates, the `x/valaddr` module does not exist and the transaction is rejected." "Prepare everything else — install the binary, configure the signer links, verify the KMS supports `SignRawBytes` — before the upgrade, then start the server and register once v10 is live." "Start the server **before** registering: a registered-but-unreachable host makes clients dial and time out against you."
- GC: entry "is deleted once its validator has either been removed from staking state entirely, or has been jailed and unbonded for longer than a 7-day grace period (`JailedGracePeriod`)".
- "A running server alone receives no traffic: fibre clients discover servers through the on-chain `x/valaddr` registry and only dial hosts registered there."

### 4.5 KMS / signing latency (release-notes.md v10.0.0, verbatim)

- "The horcrux deprecation announced in the v7 release notes is superseded. Any KMS infrastructure may be used, provided it meets both requirements:"
- "**Arbitrary-byte signing**: the KMS must support the privval `SignRawBytes` message, which is required for fibre payment-promise endorsements and the fibre TLS identity."
- "**Signing latency**: median signing latency must stay at or below 10ms. Nodes using a remote signer expose `cometbft_privval_signing_latency_*` Prometheus metrics and log a warning when the median latency of the last 50 signatures exceeds 10ms."
- "Horcrux is currently under-maintained. Additionally, a couple of slashing incidents have occurred due to misconfigured horcrux setups."

### 4.6 Hardware, storage, bandwidth guidance

There is **no Fibre-specific hardware page**. What sources say:

| Topic | Statement | Source |
|---|---|---|
| Storage obligation | "one governance parameter, `FullStakeStorageBudget`, fixes the shard storage a 100%-stake validator should hold, and each server derives its own budget from that and its stake." "`budget(v) = FullStakeStorageBudget × assignmentFraction(v)`". "The budget is a requirement, not a function of the disk a node happens to have — it tells each operator the minimum storage their stake obliges them to provide." assignmentFraction "about `min(1, 3s)`, with the `MinRowsPerValidator` floor for the smallest". Default `FullStakeStorageBudget` = 2 TiB (PR #7689). | [ADR-029 fibre upload rate limit](https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-029-fibre-upload-rate-limit.md); [x/fibre README](https://github.com/celestiaorg/celestia-app/blob/main/x/fibre/README.md) |
| Storage backend roadmap | ADR-030 (added Sep 3, 2026, PR #7763): "Fibre shard capacity is limited by each validator's local disk." "This ADR moves durable shard payloads to S3-compatible object storage." "The first object-storage implementation will support Cloudflare R2 and AWS S3". | [ADR-030](https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-030-fibre-elastic-shard-storage.md) |
| Max message size | ADR-029 sizing example: MaxShardSize "129.83 MiB", full in-flight message "~132 MiB". | ADR-029 |
| `tools/cpu_requirements` | This tool is for the **v6 "128MB/6s" upgrade**, not Fibre: "tests whether your hardware is compatible with the **128MB/6s** upgrade"; recommends "32 CPU cores (or more)", CPUs with **GFNI** and **SHA-NI**; docker image `ghcr.io/celestiaorg/128mb-6s-benchmark:v0.0.1`. | [tools/cpu_requirements/README.md](https://github.com/celestiaorg/celestia-app/blob/main/tools/cpu_requirements/README.md) |
| Fibre benchmark machines (not a requirement) | Blog: 498 GCP machines with 48-64 vCPUs, 90-128 GB RAM, 34-45 Gbps links used for the 1 Tb/s test. | [blog](https://blog.celestia.org/introducing-fibre-1tb-s-of-blockspace/) |
| glibc | fibre: >= 2.34; multiplexer celestia-appd: >= 2.38 | fibre/cmd/README.md; v10.0.1-corto release |

### 4.7 Core team statements on serving obligations (forum thread 2288)

Source: [forum.celestia.org/t/2288](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288), JSON read 2026-09-06. Poster `chamirachid1` (display name "Rachid CHAMI"; @rach-id is the CIP-51/52 author and v10 release author).

- 2026-09-03 19:34 UTC: "Thanks for flagging this, we fixed it in docs: correct CIP-51 x/fibre parameter table (#405) · celestiaorg/CIPs@6b9c74f".
- Same post: "Yes, we rely on an honest majority to serve the data for the specified period of time. And by design, we only need 1/3rd of the validator set to be honest to be able to retrieve the rows. And if that's not possible, that means more than 2/3rd of the validator set are malicious and social consensus is needed."
- Same post: "We're planning to implement rate limiting for downloads in the next iterations." and "the community will generally build dashboards that will: Periodically check whether the provided fibre server addresses, in the x/valaddr module are reachable; Whether validators are serving the data; etc".
- 2026-09-04 16:03 UTC: "definitely, feel free to write something and we can review it" (on a rate-limiting write-up).
- Community measurement in the same thread (utku, 2026-09-04 11:00 UTC): "actual prune happens at pruneAt + ~1m45s in practice, from the 60s prune loop plus the minute-granularity prune key. Any monitoring that treats the first post-pruneAt NOT_FOUND as a fault will produce false positives on every publication." Follow-up thread: [t/2295 "Rate limiting Fibre's read path without IP allowlists"](https://forum.celestia.org/t/rate-limiting-fibres-read-path-without-ip-allowlists/2295) (2026-09-04).

---

## 5. How celestia-app upgrades are activated on mocha

Sources: [docs: Network upgrades](https://docs.celestia.org/operate/maintenance/network-upgrades/), [x/signal README](https://github.com/celestiaorg/celestia-app/blob/main/x/signal/README.md), [release-notes.md](https://github.com/celestiaorg/celestia-app/blob/main/docs/release-notes/release-notes.md), [ADR-018](https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-018-network-upgrades.md), [CIP-52](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md). All read 2026-09-06.

- Mechanism (docs): "Celestia has implemented CIP-10, which establishes two methods for coordinating network upgrades: Pre-programmed height: Used for the Lemongrass network upgrade (v2); In-protocol signaling: Used for all subsequent upgrades (v3+)". "Under the in-protocol signaling mechanism, validators submit messages to signal their readiness and preference for the next version. The upgrade activates automatically once a quorum of 5/6 of validators have signaled for the same version." Not a governance proposal.
- Delay (docs): Mocha "Typically 1-2 day delays"; Mainnet Beta 7 days. Mocha history table: v6 Matcha 2025/10/03 height 8236886 delay 2 days; v7 Hibiscus CIP-47 2026/02/23 height 10209986, 2 days; v8 CIP-49 2026/04/16 height 10941526, 2 days; v9 CIP-50 2026/06/05 height 11655991, 2 days (all mocha-4 heights). Note the docs say "v7 (Hibiscus) was skipped on Mainnet Beta due to a bug discovered after testnet activation; v8 … was activated instead."
- Release-notes v3 section (the canonical command set): "Upgrades now use the `x/signal` module to coordinate the network to an upgrade height." Signal: `celestia-appd tx signal signal 3 <plus transaction flags>`; tally: `celestia-appd query signal tally 3`; "Once 5/6+ of the voting power have signalled, the upgrade will be ready. There is a hard coded delay between confirmation of the upgrade and execution to the new state machine." Pending height: `celestia-appd query signal upgrade` → example output "An upgrade is pending to app version 3 at height 2348907." v5 note: "This expedited release will have no upgrade delay. The moment 5/6ths signal and the `MsgTryUpgrade` is successful, the network will upgrade to v5." (so the delay is per-version, set in the binary).
- x/signal README: "The signal module acts as a coordinating mechanism for the application on when it should transition from one app version to another." CLI: `celestia-appd query signal tally`, `celestia-appd tx signal signal`, `celestia-appd tx signal try-upgrade`. gRPC `celestia.signal.v1.Query/VersionTally`, example `grpcurl -plaintext localhost:9090 celestia.signal.v1.Query/VersionTally`.
- Where the app version lives on chain (ADR-018): "the app version displayed in each block and agreed upon by all validators is the version that the transactions are both validated and executed against." CIP-52: at the upgrade height `consensus.Version.AppVersion` becomes 10 and "The v10 upgrade handler adds the `x/fibre` and `x/valaddr` stores and runs module migrations."
- Docs on binary handling: "You do not need to use a tool like cosmovisor to upgrade the binary. Please upgrade your binary before signaling support for the new version." Release notes v10: "Node operators MUST upgrade their binary to this version prior to the v10 activation height."
- Announcement channels (docs overview): "Telegram announcement channel", "Discord Mainnet Beta announcements", "Discord Mocha announcements"; docs network-upgrades page also links Celenium upgrade-status pages for signaling upgrades.

---

## 6. Recommended activation-detection signals for the observer

Ordered roughly by how early they fire. Each is grounded in a source read 2026-09-06.

| # | Signal | How to watch | Meaning | Source |
|---|---|---|---|---|
| 1 | New `v10.*-mocha` release tag with `fibre_*` assets | Poll GitHub releases/tags; match `^v10\.\d+\.\d+-mocha$`; check `checksums.txt` lists `fibre_Linux_x86_64.tar.gz` | Binary published for Mocha; precedes signaling | [tags](https://github.com/celestiaorg/celestia-app/tags), [.goreleaser.yaml](https://github.com/celestiaorg/celestia-app/blob/main/.goreleaser.yaml) (`prerelease_suffix: "-"`), §3.1 (v9.0.6-mocha has no fibre assets) |
| 2 | Docs page text change | Diff [network-upgrades page](https://docs.celestia.org/operate/maintenance/network-upgrades/) for removal of "The next upgrade (v10) has not yet been scheduled." | Official schedule announced | docs page, last updated 2026-09-03 |
| 3 | Signal tally for version 10 | gRPC `celestia.signal.v1.Query/VersionTally` (REST `/signal/v1/tally/{version}` with `10`); `GetMissingValidators` (`/signal/v1/missing/{version}`) | Validators signaling; quorum is 5/6 of voting power | [signal query.proto](https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/signal/v1/query.proto), [x/signal README](https://github.com/celestiaorg/celestia-app/blob/main/x/signal/README.md), [docs](https://docs.celestia.org/operate/maintenance/network-upgrades/) |
| 4 | Pending upgrade height | gRPC `celestia.signal.v1.Query/GetUpgrade` (REST `/signal/v1/upgrade`); CLI `celestia-appd query signal upgrade` | Returns the exact activation height once `try-upgrade` succeeded ("An upgrade is pending to app version 3 at height 2348907" format) | [release-notes.md](https://github.com/celestiaorg/celestia-app/blob/main/docs/release-notes/release-notes.md), proto |
| 5 | Block header `version.app` == 10 | Read `header.version.app` from RPC `/block` or `/header`; compare to previous | The authoritative activation moment; CIP-52 says `consensus.Version.AppVersion` = 10 "from the upgrade height" | [CometBFT data structures spec — Version.App](https://github.com/cometbft/cometbft/blob/main/spec/core/data_structures.md) ("App version is decided on by the application"; `block.Version.App == state.Version.Consensus.App`), [CIP-52](https://github.com/celestiaorg/CIPs/blob/main/cips/cip-052.md), [ADR-018](https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-018-network-upgrades.md) |
| 6 | Module availability | gRPC `celestia.valaddr.v1.Query/AllBondedFibreProviders` (REST `/valaddr/v1/all-bonded-fibre-providers`) and `celestia.fibre.v1.Query/Params`; before v10 these return errors because "the `x/valaddr` module does not exist"; optionally enumerate services via `cosmos.reflection.v1.ReflectionService/FileDescriptors` | Module stores added by the v10 upgrade handler | [valaddr query.proto](https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/valaddr/v1/query.proto), [fibre query.proto](https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/fibre/v1/query.proto), [fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md), [cosmos-sdk reflection.proto](https://github.com/cosmos/cosmos-sdk/blob/main/proto/cosmos/reflection/v1/reflection.proto) (celestia-app's [go.mod](https://github.com/celestiaorg/celestia-app/blob/main/go.mod) requires `cosmos-sdk v0.50.13` but replaces it with `github.com/celestiaorg/cosmos-sdk v0.52.11`, so the effective SDK is the Celestia fork at v0.52.11; CIP-52 says "v0.52.10", a minor drift between the CIP text and the build) |
| 7 | First `MsgSetFibreProviderInfo` / `EventSetFibreProviderInfo` | Scan tx results for msg type `celestia.valaddr.v1.MsgSetFibreProviderInfo`; event attributes `validator_consensus_address`, `host` | First validator registered a Fibre host; only accepted at app v10 | [x/valaddr README](https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md), [valaddr tx.proto](https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/valaddr/v1/tx.proto) |
| 8 | First `MsgPayForFibre` / `EventPayForFibre` | Scan for msg `celestia.fibre.v1.MsgPayForFibre`; typed event `celestia.fibre.v1.EventPayForFibre` with fields `signer`, `namespace`, `commitment`, `validator_count`; also `EventDepositToEscrow` precedes it | First Fibre blob settled on chain (end-to-end working) | [x/fibre README](https://github.com/celestiaorg/celestia-app/blob/main/x/fibre/README.md), [fibre tx.proto](https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/fibre/v1/tx.proto) |
| 9 | Registered hosts reachable | For each host from `providers`, open TLS 1.3 to `host:port` (default 7980) and check the certificate carries OID `1.3.6.1.4.1.66463.1.1`; call `celestia.fibre.v1.Fibre/DownloadShard` | Off-chain liveness; what core said "the community will generally build dashboards" for | [fibre_server.md](https://github.com/celestiaorg/celestia-app/blob/main/specs/src/fibre_server.md), [forum 2288](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288) |
| 10 | Status page / announcement channels | Poll status.celestia.org incidents; Telegram/Discord Mocha announcements | Human-readable schedule; historically each Mocha upgrade had a status incident (e.g. v9.0.4-mocha, Matcha v6) | [status](https://status.celestia.org/), [docs overview](https://docs.celestia.org/operate/networks/overview/) |

Caveats grounded in sources:
- Monitoring shard availability must tolerate pruning lag: prune loop runs once per minute and `pruneAt = max(ExpiresAt, creation_timestamp + ShardRetention)` ([fibre_server.md](https://github.com/celestiaorg/celestia-app/blob/main/specs/src/fibre_server.md)); measured lag "~1m45s" ([forum 2288](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288)).
- `DownloadShard` has no per-peer rate limiting today and core plans to add it ([fibre_server.md](https://github.com/celestiaorg/celestia-app/blob/main/specs/src/fibre_server.md) "Concurrency And DoS Controls"; forum 2288), so a probe budget should be conservative.
- Heavy RPC endpoints (`block`, `block_results`, `tx_search`, gRPC block/validator-set endpoints) are gated by `max_concurrent_heavy_requests` (default 20) in celestia-core v0.41.0 and return HTTP 503 / gRPC `ResourceExhausted` when exceeded ([release-notes.md v10](https://github.com/celestiaorg/celestia-app/blob/main/docs/release-notes/release-notes.md)).

---

## Appendix — source list with seen dates

All seen 2026-09-06:

- https://docs.celestia.org/operate/networks/mocha-testnet/
- https://docs.celestia.org/operate/maintenance/network-upgrades/
- https://docs.celestia.org/operate/networks/overview/
- https://docs.celestia.org/sitemap.xml
- https://statuspage.incident.io/celestia-org/incidents/qay1t5b2
- https://status.celestia.org/incidents/01M1EXTFMDW88T1KGW2AC8FVW8
- https://status.celestia.org/incidents/01KYMP2MTJBS9ZKHRQNT5HCVMQ
- https://status.celestia.org/
- https://github.com/celestiaorg/celestia-app/tags
- https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.0-corto
- https://github.com/celestiaorg/celestia-app/releases/tag/v10.0.1-corto
- https://github.com/celestiaorg/celestia-app/releases/download/{v10.0.1-corto,v10.0.0-corto,v9.0.6-mocha}/checksums.txt
- https://github.com/celestiaorg/celestia-app/blob/main/.goreleaser.yaml
- https://github.com/celestiaorg/celestia-app/blob/main/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/Makefile
- https://github.com/celestiaorg/celestia-app/blob/main/docs/release-notes/release-notes.md
- https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/fibre/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/x/fibre/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/x/valaddr/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/x/signal/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/specs/src/fibre_server.md
- https://github.com/celestiaorg/celestia-app/blob/main/tools/cpu_requirements/README.md
- https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-018-network-upgrades.md
- https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-029-fibre-upload-rate-limit.md
- https://github.com/celestiaorg/celestia-app/blob/main/docs/architecture/adr-030-fibre-elastic-shard-storage.md
- https://github.com/celestiaorg/celestia-app/blob/main/proto/celestia/{signal,fibre,valaddr}/v1/{query,tx}.proto
- https://github.com/celestiaorg/celestia-app/blob/main/go.mod
- https://github.com/celestiaorg/celestia-app/issues/7768 , /issues/7769 , /issues/7774
- https://github.com/celestiaorg/celestia-app/pull/7762
- https://github.com/celestiaorg/celestia-app/compare/0b69316466c3ba02f708c0e2a101f834d5d1827f...v10.0.0-corto
- https://github.com/celestiaorg/celestia-app/compare/v10.0.0-corto...main
- https://github.com/celestiaorg/celestia-app/issues?q=is%3Aissue+is%3Aopen+label%3Afibre
- https://github.com/celestiaorg/celestia-app/milestones (only one open milestone, unrelated: "Implement ZK Execution ISM")
- https://github.com/celestiaorg/networks (README), /tree/master/mocha-5, raw mocha-5/genesis.json, raw corto-1/genesis.json
- https://github.com/celestiaorg/CIPs/blob/main/cips/cip-051.md , /cip-052.md , commit 6b9c74f
- https://cips.celestia.org/cip-051.html
- https://github.com/celestiaorg/docs/pull/2578
- https://forum.celestia.org/t/2288 and /t/2295 (Discourse JSON), forum search.json for fibre / corto / v10 / mocha-5 / "weeks away"
- https://blog.celestia.org/introducing-fibre-1tb-s-of-blockspace/
- https://github.com/cometbft/cometbft/blob/main/spec/core/data_structures.md
- https://github.com/cosmos/cosmos-sdk/blob/main/proto/cosmos/reflection/v1/reflection.proto
