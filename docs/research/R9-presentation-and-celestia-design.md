# R9 — Presentation research and Celestia-aligned design spec for the Fibre observer dashboard

Date of research: 7 September 2026. Every claim below carries a link and the date it was seen. Where a value could not be read from a page or repository, that is stated instead of guessed. "Seen" means fetched from this research environment on 7 Sep 2026; a few sites (Spark's filspark.com hosts) could not be reached through our proxy and are marked as such.

Scope reminder: public, read-only Next.js dashboard; three views (network overview, validator detail, blob detail); verdict classes `HEALTHY / FAULT / TOLERATED / EXPECTED_GONE / UNREACHABLE_POST_WINDOW / NOT_PROBED`; per-blob "reconstructable" (needs 4096 of 16384 rows); probe counts beside every rate; "observed from one location" label; gaps rendered as gaps.

---

## 0. Fibre facts the presentation depends on (for reference)

| Fact | Value | Source (seen 7 Sep 2026) |
|---|---|---|
| Erasure coding | "total rows: 16384", "original rows: 4096", "encoding ratio: 0.25", "minimum row size: 64 bytes" | [celestia-app specs/src/fibre_encoding.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_encoding.md) |
| Validator gRPC | `service Fibre { rpc UploadShard(...); rpc DownloadShard(...); }`; "The Fibre server-to-client gRPC link is TLS-only."; "Clients verify the presented TLS key against the expected validator consensus public key and chain ID." | [fibre_server.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md) |
| Retention | Upload returns `ExpiresAt` and `ShardRetention` ("on-chain, governance-changeable parameter, default 4h"); `pruneAt = max(ExpiresAt, creation_timestamp + ShardRetention)` | [fibre_server.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md) |
| Endpoint registration | `MsgSetFibreProviderInfo { signer: celestiavaloper..., host: "host:port" }`; queries `celestia-appd query valaddr provider <cons-address>` / `providers` | [fibre_registry_module.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_registry_module.md) |
| Failure mode | "If too many assigned validators are unavailable, pruning early, misconfigured, or unreachable, readers may fail to reconstruct the blob even though the on-chain commitment exists." | [CIP-51](https://cips.celestia.org/cip-051.html) |
| Enforcement | No slashing; "we only need 1/3rd of the validator set to be honest to be able to retrieve the rows"; beyond that "social consensus is needed". Core team expects community dashboards that "Periodically check whether the provided fibre server addresses … are reachable" and verify serving; the author proposes using on-chain state to identify assigned validators and probe only them. | [forum.celestia.org thread by utku, 2 Sep 2026](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288) |
| FDP relevance | Mandatory: "Must run Fibre once operational". Optional criterion: "Develop and maintain developer tooling, services, applications, and dashboards". "validator uptime performance is paramount". | [docs.celestia.org FDP page](https://docs.celestia.org/community/foundation-delegation-program) |

Note: the spec's server document does not contain an explicit sentence that validators must serve until expiry; the obligation is implied by `ExpiresAt`/`pruneAt` and by the forum thread's "continuous serving rather than initial acceptance" (seen 7 Sep 2026). The dashboard's methodology page should quote the spec and the thread rather than assert a rule the spec does not state.

---

## 1. Celestia's visual identity on its own properties

### 1.1 Summary table

| Property | Stack | Default theme | Body font (where loaded) | Heading font | Mono | Primary/accent | Background | Source (seen 7 Sep 2026) |
|---|---|---|---|---|---|---|---|---|
| celestia.org | Next.js 16 App Router, Tailwind, `next/font/local` | Light (`<body class="text-black font-untitledSans">`, `bg-white` ×15 in HTML; no `prefers-color-scheme`/`data-theme` found) | `untitledSans` 400/500, self-hosted `/_next/static/media/*.woff2` | `youth` 400; also `nuberNext` 400/500 and `nuberNextWide` 400–700 | `robotoMono` 100–700 | Brand page: Indigo #5640D1, Amethyst #7C68F2 | Frost #FDFCFF (light), Void #0E1014 (dark) | [celestia.org HTML/CSS via curl](https://celestia.org/); [celestia.org/brand](https://celestia.org/brand/); [celestiaorg/celestia.org tailwind.config.js](https://raw.githubusercontent.com/celestiaorg/celestia.org/main/tailwind.config.js) |
| docs.celestia.org | Next.js + Nextra (MDX), static export | `next-themes` `defaultTheme: "system"` (found in page payload); page ships both `:root` and `.dark` token sets | `UntitledSans` 400/500 from `/fonts/untitled-sans/untitled-sans-{regular,medium}.woff2`; `body { font-family: 'UntitledSans', sans-serif }` | `h1…h6 { font-family: 'Youth' }` from `/fonts/youth/Youth-Regular.woff2` | Nextra default `ui-monospace, SFMono-Regular, Menlo…` (no custom mono found) | Nextra primary: `:root --nextra-primary-hue: 212deg; saturation 100%; lightness 45%`; `.dark hue 204deg; lightness 55%` (i.e. a blue, not Indigo) | `--nextra-bg: 250,250,250` (light) | [docs.celestia.org HTML via curl](https://docs.celestia.org/); [celestiaorg/docs repo](https://github.com/celestiaorg/docs) |
| blog.celestia.org | Ghost ("Powered by Ghost") | Light | System stack in HTML (`-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto…`); theme fonts not readable from the HTML we fetched | — | — | not readable | white | [blog.celestia.org](https://blog.celestia.org/) |
| forum.celestia.org | Discourse ("Powered by Discourse") | `<meta name="color-scheme" content="light">` in crawler HTML | not readable (no CSS in fetched HTML) | — | — | not readable | not readable | [forum.celestia.org](https://forum.celestia.org/) |
| status.celestia.org | incident.io status page | Ships Tailwind `slate` light + `dark:` classes; default not determinable from static HTML | not readable | — | — | Component box class `…__operational` | `bg-white dark:bg-global` | [status.celestia.org](https://status.celestia.org/) |
| Celenium (celenium.io) | Nuxt (Vue), SCSS | Dark: the `:root` block in `assets/styles/base.scss` holds the dark values; `[theme="light"]` and `[theme="dimmed"]` are overrides | Inter 400/500/600/700 via Google Fonts `<link>` in `nuxt.config.ts` | Inter | JetBrains Mono 700 (`.mono` class in `text.scss`), Source Code Pro also linked | `--brand: #18d2a5` (dark), `#0ade71` (light) | `--app-background: rgb(23,25,27)` dark; `rgb(235,235,235)` light | [base.scss](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/base.scss); [text.scss](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/text.scss); [nuxt.config.ts](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/nuxt.config.ts) |

Where "not readable" appears, the value was not present in the HTML/CSS we could fetch and is not stated as a brand value anywhere we found.

### 1.2 celestia.org brand page — published palette

From [celestia.org/brand](https://celestia.org/brand/) (seen 7 Sep 2026). The page says "Frost and Void anchor every surface; Indigo and Amethyst carry the brand." It names no fonts.

| Family | 50 | 200 | 400 | 600 | 800 |
|---|---|---|---|---|---|
| Primary | Frost #FDFCFF · Void #0E1014 · Indigo #5640D1 · Amethyst #7C68F2 | | | | |
| Neutral | #EAECEE | #C4C8CE | #808890 | #4A5058 | #242830 |
| Blue | #EAEDF0 | #A9C2D4 | #5595C3 | #32688F | #254459 |
| Purple | #E3E1F2 | #C8C3E5 | #7C68F2 | #4331A5 | #231860 |
| Beige | #EDECEB | #C6BDB7 | #9A897E | #6C5E55 | #473E38 |

Logo: "Symbol" (standalone) and "Logotype", each in Frost, Void, Indigo, SVG and PNG; a "Download Brand Kit" link; "Cells" generative particle-field visual language. The page contains no third-party usage rules (see §2.4).

Discrepancy to note: the public repo's `tailwind.config.js` still carries an older palette (`black.DEFAULT "#17141A"`, `purple.DEFAULT "#7b2bf9"`, `red "#EC5643"`, `green "#42D885"`) ([tailwind.config.js](https://raw.githubusercontent.com/celestiaorg/celestia.org/main/tailwind.config.js), seen 7 Sep 2026). The brand page's Indigo #5640D1 is the published guidance; use it, not the repo purple.

Also: the docs repo's `app/globals.css` on `main` contains `--background: #ffffff / #0a0a0a`, `--foreground: #171717 / #ededed` and Geist font variables ([globals.css](https://raw.githubusercontent.com/celestiaorg/docs/main/app/globals.css), seen 7 Sep 2026), but the live site serves UntitledSans/Youth and Nextra hue tokens via inline styles. Treat the live site as the reference.

### 1.3 Celenium — concrete tokens from the repository

All from `assets/styles/base.scss` ([raw](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/base.scss), seen 7 Sep 2026). MIT licence per the repo root ([repo](https://github.com/celenium-io/celenium-interface)).

| Token | `:root` (dark, default) | `[theme="light"]` |
|---|---|---|
| `--base-width` | 992px | 992px |
| `--app-background` | rgb(23, 25, 27) | rgb(235, 235, 235) |
| `--card-background` | rgb(32, 34, 37) | rgb(255, 255, 255) |
| `--outline-background` | #2b2d30 | #e5e5e5 |
| `--txt-primary` | rgba(255,255,255,90%) | rgba(0,0,0,90%) |
| `--txt-body` | rgba(255,255,255,75%) | rgba(0,0,0,75%) |
| `--txt-secondary` | rgba(255,255,255,65%) | rgba(0,0,0,70%) |
| `--txt-tertiary` | rgba(255,255,255,35%) | rgba(0,0,0,40%) |
| `--brand` | #18d2a5 | #0ade71 |
| `--blue` | #379bff | #0b84fe |
| `--red` | #eb5757 | #eb5757 |
| `--orange` | #ff5a17 | #ff5a17 |
| `--yellow` | #e6c525 | #e5c10b |
| `--green` | #0ade71 | #26c071 |
| `--neutral-green` | #33a853 | #33a853 |
| `--validator-active` / `-inactive` / `-jailed` | #18d2a5 / #1ca7ed / #f8774a | (not redefined) |
| `--block-progress-fill-background` | #33a853 | #33a853 |

Typography (`assets/styles/text.scss`, [raw](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/text.scss)): size utilities `.fz--{8,10,11,12,13,14,15,16,18,20,24,26,32,36,38,40}`, weights 400–700, line-heights 1.0–1.8, `.mono { font-family: JetBrains Mono }`, `.tabular { font-variant: tabular-nums }`. Body stack in `base.scss`: `Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, …`. Fonts are loaded via `<link>` to `fonts.googleapis.com` (Inter 400–700; JetBrains Mono 700; Source Code Pro 200–900) in `nuxt.config.ts` ([raw](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/nuxt.config.ts)).

Density: a 992px content width with a 12–14px table type scale; the live validators table shows eight columns ("Validator", "Voting Power", "Outgoing Rewards", "Commissions", "Rate", "Max Rate", "Max Change Rate", "Version") sorted by voting power descending with "1 of 5" pagination ([celenium.io/validators](https://celenium.io/validators), seen 7 Sep 2026). Exact default page size was not readable from the rendered page.

Iconography: Celenium uses its own inline SVG icon set; we did not catalogue it. Celestia's own sites do not publish an icon set.

### 1.4 How Celenium renders identifiers, identity, namespaces, time

| Item | Celenium behaviour | Evidence (seen 7 Sep 2026) |
|---|---|---|
| Hex hashes (cons address, block hash) | `shortHex`: if longer than 16 chars → first 8 + ` ••• ` + last 8, e.g. `07E5EAFE ••• E6AC63AE` | [services/utils/general.js](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js); [celenium.io/block/1](https://celenium.io/block/1) |
| Bech32 addresses | `splitAddress`: prefix kept, then ` ••• ` and the **last 4** chars — `celestiavaloper ••• z3tl`, `celestiavalcons ••• xxxx`, `celestia ••• z8v7` | same file; [celenium.io/validator/2](https://celenium.io/validator/2) |
| Copy | `<CopyButton :text="validator.address.hash" />` next to addresses and identity | [ValidatorOverview.vue](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/validator/ValidatorOverview.vue) |
| Validator identity | Moniker as text; `website` as external link; `identity` (16-hex Keybase key suffix) shown as text with copy button; consensus address via `shortHex(validator.cons_address)`; operator address via `splitAddress(validator.address.hash)`; **no avatar** rendered in `ValidatorOverview.vue` or the validators list | same file; [celenium.io/validators](https://celenium.io/validators) |
| Keybase avatar pattern (other explorers) | ping.pub: `https://keybase.io/_/api/1.0/user/lookup.json?key_suffix=${identity}&fields=pictures` | [ping-pub/explorer useStakingStore.ts](https://raw.githubusercontent.com/ping-pub/explorer/master/src/stores/useStakingStore.ts) |
| Tx hashes in lists | first 4 + space + last 4 (`7877 31F3`); tx page URL uses the full lower-case 64-hex hash | [celenium.io/txs](https://celenium.io/txs) |
| Namespaces | Leading `00` bytes stripped for display (`getNamespaceID`), then first 4 ` ••• ` last 4 (`29c6 ••• 7a77`); alias shown when known (`eclipse`); "Version" column; URL keeps the full 56-hex ID including leading zeros, e.g. `/namespace/00000000000000000000000000000000000000000065636c69707365`; validation regex `/^[0-9a-f]{56}$/` | [general.js](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js); [celenium.io/namespaces](https://celenium.io/namespaces) |
| Commitments | Not shown on the block page we fetched; Celenium exposes base64↔hex helpers (`base64ToHex`) so both encodings occur | [general.js](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js) |
| Time | Relative + absolute side by side: `4 sec. ago Sep 7, 4:42 PM`; block page: `Oct 31, 2023, 2:00 PM` for a block whose API time is `2023-10-31T14:00:00Z`. Luxon `DateTime.fromISO(...).setLocale("en").toFormat("ff")` / `"TT"`; no `setZone`/`toUTC` calls found in the components read, so times render in the viewer's local zone; no "UTC" label | [celenium.io/txs](https://celenium.io/txs); [BlockOverview.vue](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/block/BlockOverview.vue); [API /v1/validators/2](https://api-mainnet.celenium.io/v1/validators/2) |
| Block height links | `/block/<height>`; proposer moniker links to `/validator/<celenium-id>` | [celenium.io/block/1](https://celenium.io/block/1) |
| Bytes | `formatBytes` → `Bytes, KiB, MiB, GiB, TiB, PiB` (binary units) | [general.js](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js) |

Other properties: docs.celestia.org and blog.celestia.org show absolute dates in `Mon D, YYYY` form ("Jan 13, 2026 • 5 min read") ([blog](https://blog.celestia.org/), seen 7 Sep 2026). status.celestia.org shows a quarter window label "Jun 2026–Sep 2026" and the string "UTC" appears once in its HTML; its timestamp format could not be read from the static page ([status](https://status.celestia.org/), seen 7 Sep 2026).

---

## 2. Community conventions: explorers, URL patterns, API, brand use

### 2.1 Which explorers operators expect

docs.celestia.org lists for Mainnet Beta (chain-id `celestia`): celenium.io, celestia.explorers.guru, celestia.valopers.com, explorer.nodestake.top/celestia, mainnet.itrocket.net/celestia, mammoblocks.io, mintscan.io/celestia, stakeflow.io/celestia ([Mainnet Beta page](https://docs.celestia.org/operate/networks/mainnet-beta/), seen 7 Sep 2026). The docs "Data, dashboards, and analytics" page lists Celenium first ("Blockchain explorer and analytics platform for Celestia"), then celestiadata.com, L2BEAT's DA page, ProbeLab, Looker Studio dashboards, Blockworks Research (paid), Numia dashboards, rollup.wtf ([resources page](https://docs.celestia.org/learn/celestia-101/resources/), seen 7 Sep 2026). Celenium is the first-listed explorer on both pages; link to it by default and offer Mintscan as a secondary link for validators (Mintscan is the only other explorer listed in docs that renders Keybase avatars in our experience — not verified in this research, so do not claim it in the UI).

### 2.2 Celenium URL patterns (verified 7 Sep 2026)

| Entity | Mainnet | Mocha | Notes |
|---|---|---|---|
| Validator | `https://celenium.io/validator/<celenium numeric id>` e.g. `/validator/2` (200) | `https://mocha.celenium.io/validator/<id>` (`/validators` returns 200 on mocha) | `/validator/<celestiavaloper…>` returns **404** (curl). Resolve operator address → id with `GET /v1/search?query=<celestiavaloper…>` (returns `{"type":"validator","result":{"id":2,…}}`). Sources: [celenium.io/validator/2](https://celenium.io/validator/2), [search API](https://api-mainnet.celenium.io/v1/search?query=celestiavaloper14ntfv0qkg8522xe0pvrfgqxcmnj5x466v8z3tl) |
| Block | `https://celenium.io/block/<height>` | `https://mocha.celenium.io/block/<height>` | [celenium.io/block/1](https://celenium.io/block/1) |
| Tx | `https://celenium.io/tx/<64-hex lower-case>` | same path on mocha | [celenium.io/txs](https://celenium.io/txs) |
| Namespace | `https://celenium.io/namespace/<56-hex incl. leading zeros>` | same path on mocha | [celenium.io/namespaces](https://celenium.io/namespaces) |
| Lists | `/validators`, `/blocks`, `/txs?message_type=MsgPayForBlobs`, `/namespaces` | same | [mocha.celenium.io](https://mocha.celenium.io/) |

### 2.3 Celenium API for enrichment

| Item | Value | Source (seen 7 Sep 2026) |
|---|---|---|
| Base URLs | `https://api-mainnet.celenium.io/v1/`, `https://api-mocha.celenium.io/v1/` | [api-docs.celenium.io](https://api-docs.celenium.io/) |
| Validators | `GET /v1/validators?limit=&offset=&jailed=`; fields: `id, version, cons_address, moniker, website, identity, contacts, details, rate, max_rate, max_change_rate, min_self_delegation, stake, rewards, commissions, voting_power, jailed, messages_count, creation_time, address.hash (celestiavaloper…), delegator.hash (celestia1…)`. Live sample: `"identity":"17D0A114A90FD55F"` for Anchorage Digital. No avatar/logo field; `identity` is the Keybase key suffix. | [Get validators](https://api-docs.celenium.io/api-8204946); [live call](https://api-mainnet.celenium.io/v1/validators?limit=1&offset=0) |
| Single validator | `GET /v1/validators/<id>` | [live](https://api-mainnet.celenium.io/v1/validators/2) |
| Head | `GET /v1/head` → `chain_id`, `last_height`, `last_time` (mocha head on 7 Sep 2026 16:45 UTC: `mocha-4`, height 14,585,997) | [live mocha head](https://api-mocha.celenium.io/v1/head) |
| Rate limits | Plans page: free "3" req/s, "100,000" req/day, "attribution required"; live response headers on the free tier: `x-ratelimit-limit-second: 5`, `x-ratelimit-limit-day: 100000`, `ratelimit-reset: 1`; `access-control-allow-origin: *` (browser calls allowed) | [api-plans.celenium.io](https://api-plans.celenium.io/); live headers from `curl -D - https://api-mainnet.celenium.io/v1/head` |
| Terms | Free tier: "you must mention this on your website or app by stating 'Powered by Celenium API', 'Built with Celenium API' or 'Data provided by Celenium API' with a direct link to celenium.io." Paid keys go in the `apikey` header. | [api-docs.celenium.io](https://api-docs.celenium.io/) |

Implication for a static-friendly Next.js site: fetch monikers/websites/identities at build or in a scheduled job (one `GET /v1/validators` page per 100 validators), cache them in the collector's output, and print the required attribution line in the footer. Avatars: Celenium itself does not render them; if we want them, use the Keybase lookup on `identity` as ping.pub does (§1.4), server-side, cached, with a neutral placeholder when absent.

### 2.4 Brand and trademark guidance

- [celestia.org/brand](https://celestia.org/brand/) (seen 7 Sep 2026) provides marks, colours and Cells but "provides no explicit guidelines restricting or permitting third-party use of the name or logo."
- [celestia.org/press](https://celestia.org/press/) (seen 7 Sep 2026): "Quick links" to Branding and Enquiries; no trademark text.
- [celestia.org/tos](https://celestia.org/tos/) (effective 16 Jan 2023, seen 7 Sep 2026): "The Company's name, the Company's logo and all related names, logos, product and service names, designs and slogans are trademarks of the Company or its affiliates or licensors." Reproduction without permission is restricted except for open-source components.
- No separate trademark-policy page was found by search ([search result set](https://celestia.org/tos/)).

Practical rule for us: do not use the Celestia logo or the word mark as our product name; call the site something like "Fibre Observer" with a plain-text line "Independent monitor of Celestia Fibre serving. Not affiliated with the Celestia Foundation." Use the brand palette for surfaces only (Frost/Void/Neutral) — those are ordinary colours, not marks — and do not use Cells.

---

## 3. Presentation patterns from dashboards that must be trusted

### 3.1 What each reference does

| Reference | Rate + denominator | "No data" vs "failure" | Colour semantics | Single-vantage / uncertainty labelling | Time windows | Mobile | Source (seen 7 Sep 2026) |
|---|---|---|---|---|---|---|---|
| L2BEAT | Metrics shown with explanatory suffixes ("34.0% with additional trust assumptions"); every risk slice has a written criterion; glossary terms are anchor-linked (`/glossary#…`) | DA risk table uses explicit "N/A" cells rather than blanks | Rosette: green = lowest risk / strongest requirements met, yellow = "acceptable but compromised", red = "fail to meet minimum safety standards" | FAQ: "the L2BEAT team DOES NOT DO SECURITY AUDIT"; "All the information that we present on our site should be independently verified"; framework "gets frequently updated based on new circumstances" | Chart range selectors not readable (page truncated); no "last updated" stamp found on the summary or Celestia DA page | not assessed | [Summary](https://l2beat.com/scaling/summary); [FAQ](https://l2beat.com/faq); [Rosette framework](https://forum.l2beat.com/t/the-risk-rosette-framework/292); [DA risk table](https://l2beat.com/data-availability/risk); [Celestia DA page](https://l2beat.com/data-availability/projects/celestia/blobstream); [Glossary](https://l2beat.com/glossary) |
| Filecoin Spark | RSR = successful / total retrieval checks; API `GET /retrieval-success-rate?from=<day>&to=<day>`; per-miner `/miner/:id/retrieval-success-rate/summary`; `nonZero=true` excludes "Miners with no successful retrievals" | Zero-success providers are a distinct class (excludable), not silently averaged in | not readable (dashboard unreachable from our proxy) | Checkers are grouped into committees per round using drand randomness; "The committee will then come to a consensus about the result of the retrieval" — i.e. no single checker's result is trusted alone | Dashboard shows RSR "over the past 30 days"; records start 7 Apr 2024; provider page shows median TTFB | not assessed | [spark-stats README](https://github.com/CheckerNetwork/spark-stats); [roadmap issue #192](https://github.com/CheckerNetwork/roadmap/issues/192); search snippets for [docs.filspark.com committee-building](https://docs.filspark.com/measurement/committee-building) and [dashboard.filspark.com/provider/f0101020](https://dashboard.filspark.com/provider/f0101020) — **filspark.com hosts returned DNS timeout / proxy 502 on 7 Sep 2026; quotes come from search snippets and GitHub only** |
| tenderduty | Grid of "the last 512 blocks"; alerting "based on consecutive misses or on a percentage missed within the slashing window" | Signed = "not highlighted. This should be the nominal state." (absence of colour is nominal) | Missed = "bright orange"; proposed = "bright green/yellow"; pre-vote/pre-commit-not-included is a separate state that "might indicate peering issues" | Not applicable (local monitor) | Rolling last-N blocks | "intentional design for maximum density"; dark/light modes | [tenderduty docs/README.md](https://github.com/blockpane/tenderduty/blob/main/docs/README.md) |
| ping.pub uptime | Rolling grid padded to 50 blocks per validator | — | `bg-green-500` committed, `bg-yellow-500` precommitted, `bg-red-500` missed; a legend row (`uptime.legend`) with the three states | Not applicable | Tabs Overall / Blocks / Customize | not assessed | [uptime/index.vue](https://raw.githubusercontent.com/ping-pub/explorer/master/src/modules/%5Bchain%5D/uptime/index.vue) |
| Grafana | Units/decimals configurable; "No value" default "-" | State timeline: null values are drawn as gaps; "Connect null values: Never / Always / Threshold"; Special value mappings map `Null`, `NaN` to explicit text and colour | Default thresholds "Base = green", "80 = red" (documented as a default, not a recommendation) | — | Time-range picker (standard) | responsive panels | [Thresholds](https://grafana.com/docs/grafana/latest/panels-visualizations/configure-thresholds/); [Standard options](https://grafana.com/docs/grafana/latest/panels-visualizations/configure-standard-options/); [State timeline](https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/state-timeline/); [Value mappings](https://grafana.com/docs/grafana/latest/panels-visualizations/configure-value-mappings/) |
| Atlassian Statuspage | Uptime % per component | "Days with no data are represented by a gray color." Any downtime "immediately jumps to half-way between green and yellow" so zero-downtime days are distinguishable | Operational (green), Degraded Performance (yellow), Partial Outage (orange), Major Outage (red), Under Maintenance (blue); uptime bar green → yellow → orange → red | — | 90-day showcase plus full `/uptime` history | responsive | [Top-level status](https://support.atlassian.com/statuspage/docs/top-level-status-and-incident-impact-calculations/); [Historical uptime](https://support.atlassian.com/statuspage/docs/display-historical-uptime-of-components/) |
| incident.io / status.celestia.org | "100% uptime" per component | — | "grey for 'no impact', yellow for 'degraded performance', orange for 'partial outage', or red for 'full outage'" | — | Quarter label "Jun 2026–Sep 2026"; "View history" | responsive | [incident.io severity docs](https://docs.incident.io/status-pages/severity); [status.celestia.org](https://status.celestia.org/) |

### 3.2 Patterns to adopt (each with the source that motivates it)

1. Every rate has its denominator next to it. Spark's RSR is successful/total and exposes zero-success providers as a separate class ([spark-stats](https://github.com/CheckerNetwork/spark-stats)). We show `97.1% (34/35)` and never a bare percentage.
2. "No data" is a distinct state with its own neutral colour and its own word. Statuspage uses grey for "no data" days ([Statuspage](https://support.atlassian.com/statuspage/docs/display-historical-uptime-of-components/)); Grafana draws nulls as gaps and lets you map `Null` to explicit text ([Grafana](https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/state-timeline/)). `NOT_PROBED` is rendered as an empty cell with text "not probed", never as a failure.
3. Nominal state is quiet. tenderduty does not highlight signed blocks ([tenderduty](https://github.com/blockpane/tenderduty/blob/main/docs/README.md)). `HEALTHY` gets a low-saturation fill; `FAULT` gets the strongest colour.
4. Four-step severity, not two. Statuspage and incident.io both use grey/green → yellow → orange → red ([Statuspage](https://support.atlassian.com/statuspage/docs/top-level-status-and-incident-impact-calculations/), [incident.io](https://docs.incident.io/status-pages/severity)). Our classes map onto this without inventing new semantics (see §6.3).
5. Every number has a written definition one click away. L2BEAT anchors glossary terms and writes a criterion for every rosette slice ([L2BEAT rosette](https://forum.l2beat.com/t/the-risk-rosette-framework/292), [glossary](https://l2beat.com/glossary)). Each verdict badge and each column header links to the methodology anchor for that term.
6. Say what you are not. L2BEAT: "DOES NOT DO SECURITY AUDIT" and "should be independently verified" ([FAQ](https://l2beat.com/faq)). Our banner: "Observed from one location. Not an availability proof."
7. Multiple vantage points are the honest fix; until we have them, label the limitation. Spark uses per-round committees of checkers that reach consensus ([docs.filspark.com committee-building](https://docs.filspark.com/measurement/committee-building), via search snippet). We have one vantage, so the label is mandatory on every page.
8. Fixed windows with explicit boundaries. Spark reports "past 30 days"; Statuspage shows 90 days; status.celestia.org shows a quarter. We use fixed `24h / 7d / 30d` selectors plus the blob's own protocol window (settlement → `ExpiresAt` → grace → post), and print the window's start and end timestamps in UTC under every chart.
9. Update stamp. None of L2BEAT's pages we read show a "last updated" stamp; we do the opposite, because a probe dashboard's value decays quickly: collector height and last probe time in the footer on every page (§5.4).

---

## 4. Accessibility and honesty conventions

| Convention | Rule | Source (seen 7 Sep 2026) |
|---|---|---|
| Never colour alone | WCAG 2.2 SC 1.4.1: "Color is not used as the only visual means of conveying information, indicating an action, prompting a response, or distinguishing a visual element." Sufficient technique: information "also available in text". | [WCAG 1.4.1](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html) |
| Text contrast | SC 1.4.3: 4.5:1 for normal text, 3:1 for large text (≥18pt or 14pt bold). SC 1.4.11 non-text contrast 3:1 applies to icons and UI boundaries. | [WCAG 1.4.3](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html) |
| Status tags carry words | GOV.UK: "should not use colour alone to convey information, because it's not accessible"; keep the same colour for the same tag everywhere; tags are adjectives, never interactive. | [GOV.UK Tag](https://design-system.service.gov.uk/components/tag/) |
| Colour-blind-safe palette | Okabe-Ito: orange #E69F00, sky blue #56B4E9, bluish green #009E73, yellow #F0E442, blue #0072B2, vermilion #D55E00, reddish purple #CC79A7, black #000000. Wilke's caveat: "It is very possible to use a cvd-safe scale and yet produce a figure a person with cvd cannot decipher" — test in a simulator; small marks are harder than large areas. | [Wilke, Fundamentals of Data Visualization, ch. 19](https://clauswilke.com/dataviz/color-pitfalls.html); original design rationale at [jfly.uni-koeln.de/color](https://jfly.uni-koeln.de/color/) (hex values not on that page) |
| Alternative palette | Paul Tol bright: #4477AA #EE6677 #228833 #CCBB44 #66CCEE #AA3377 + grey #BBBBBB; vibrant: #EE7733 #0077BB #33BBEE #EE3377 #CC3311 #009988 + #BBBBBB; high-contrast: #004488 #DDAA33 #BB5566. Grey #BBBBBB is the scheme's "bad data" colour. | [Paul Tol's notes](https://sronpersonalpages.nl/~pault/) |
| Numeric tables | "When comparing columns of numbers, align the numbers to the right"; use `<caption>` and `scope`. | [GOV.UK Table](https://design-system.service.gov.uk/components/table/) |
| Counts with rates | Spark: RSR is a ratio of counts and zero-success providers are a distinct class (§3). | [spark-stats](https://github.com/CheckerNetwork/spark-stats) |
| Confidence for small n | Wilson score interval "can be safely employed with small samples and skewed observations", does not overshoot [0,1] and has coverage closer to nominal than the Wald interval; Evan Miller (2009) shows why sorting by raw proportion misleads when n is tiny (2/2 vs 100/101). **We found no monitoring dashboard among the references that displays Wilson intervals; this would be our addition, and the methodology page must say so.** | [Wikipedia: Binomial proportion CI](https://en.wikipedia.org/wiki/Binomial_proportion_confidence_interval); [Evan Miller](https://www.evanmiller.org/how-not-to-sort-by-average-rating.html) |
| Explicit methodology + limitations pages | L2BEAT keeps its framework as a versioned forum document and links it from every risk slice; FAQ states what the site does not do. | [L2BEAT rosette](https://forum.l2beat.com/t/the-risk-rosette-framework/292); [FAQ](https://l2beat.com/faq) |

Measured contrast ratios for the tokens proposed in §6 (WCAG formula, computed 7 Sep 2026 from the hex values above):

| Pair | Ratio | Passes |
|---|---|---|
| Void #0E1014 on Frost #FDFCFF (and inverse) | 18.63:1 | AAA |
| Neutral 600 #4A5058 on Frost | 7.96:1 | AA text |
| Neutral 400 #808890 on Frost | 3.52:1 | large text / non-text only |
| Neutral 200 #C4C8CE on Void | 11.33:1 | AAA |
| Indigo #5640D1 on Frost | 6.74:1 | AA text |
| Amethyst #7C68F2 on Void | 4.64:1 | AA text |
| Okabe-Ito bluish green #009E73 on Frost | 3.35:1 | non-text only |
| Okabe-Ito vermilion #D55E00 on Frost | 3.78:1 | non-text only |
| Okabe-Ito orange #E69F00 on Frost | 2.20:1 | **fails 3:1** |
| Okabe-Ito blue #0072B2 on Frost | 5.07:1 | AA text |
| Void on solid bluish green / vermilion / orange / sky blue | 5.57 / 4.92 / 8.45 / 8.25 | AA text |
| Frost on solid blue #0072B2 | 5.07:1 | AA text |

Consequence: in light mode, status colours must not be used for text and the orange fill cannot be the only boundary of a badge. The badge design in §6.3 therefore uses Void text, a Neutral border, and an icon glyph, so colour is a third channel, not the first.

---

## 5. Product presentation for the three audiences, and information architecture

### 5.1 What each audience looks for first

| Audience | First question | What the page must show within one screen | Motivating source |
|---|---|---|---|
| Celestia Foundation reviewer | "Does validator X keep its Fibre promise, and is this tool maintained?" | Network overview with per-validator verdict counts over 30d; a methodology page that quotes the spec; a visible collector height and last-probe time (proof the tool is alive); a changelog/pinned `celestia-app` commit. FDP lists "Develop and maintain developer tooling, services, applications, and dashboards" and requires validators to "run Fibre once operational"; uptime "is paramount". | [FDP page](https://docs.celestia.org/community/foundation-delegation-program) |
| Validator operator | "Where is my row, and what exactly failed?" | Search by moniker or `celestiavaloper…`; a permalink per validator; per-probe evidence (time, height, row index, gRPC error string, TLS check result); the registered `host:port` we probed; link to the Celenium validator page. Operators already read tenderduty/ping.pub grids, so a per-blob cell grid is familiar. | [fibre_registry_module.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_registry_module.md); [tenderduty](https://github.com/blockpane/tenderduty/blob/main/docs/README.md) |
| Rollup team | "Is my blob still retrievable, and until when?" | Blob detail: `reconstructable` (rows held ≥ 4096 of 16384), `ExpiresAt`, `pruneAt`, per-namespace filter, per-validator cell grid; link to Celenium namespace and tx pages. | [fibre_encoding.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_encoding.md); [fibre_server.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md) |
| Delegator | "Is this validator reliable?" | Same as operator view, read-only; 30d verdict share with counts; link to Celenium for stake/commission (we do not duplicate those). | [celenium.io/validators](https://celenium.io/validators) |

### 5.2 Site map

```
/                      Network overview
/validators/[valoper]  Validator detail (permalink by celestiavaloper address; also accepts consensus address and redirects)
/blobs/[id]            Blob detail (id = height + commitment hex, or the MsgPayForFibre tx hash — pick one and redirect the other)
/blobs?namespace=…     Blob list with namespace filter
/methodology           Definitions of every verdict class, window, "reconstructable", probe procedure, sampling
/about                 Who runs it, vantage location/ASN, limitations, non-affiliation, data licence, attribution to Celenium
/api                   Public read API docs (JSON endpoints mirrored from the static export)
```

Top nav (left → right): `Overview · Validators · Blobs · Methodology · About · API`, plus a network switch `Mainnet Beta | Mocha` that changes a path prefix (`/mocha/...`) rather than a query string, so permalinks are stable. This mirrors docs.celestia.org's plain sidebar grouping (Learn/Build/Operate/Status) and Celenium's flat header (Browse, Transactions, Blocks, Addresses, Validators, Namespaces) ([docs](https://docs.celestia.org/), [mocha.celenium.io](https://mocha.celenium.io/), seen 7 Sep 2026).

### 5.3 Page contents

**Network overview `/`**
- Header line: `Mainnet Beta · chain-id celestia · collector height N · last probe 2026-09-07 16:42:10 UTC (3 min ago)`.
- Banner (persistent, not dismissable): `Observed from one location (city, ASN). One probe is one sample; it is not an availability proof.`
- Four stat tiles, each with count and denominator: `Blobs in window 1,204`, `Reconstructable 1,198 / 1,204`, `Validators with FAULT in 24h 3 / 100 assigned`, `Probes in 24h 41,320`.
- Validator table (see §6.2).
- A single stacked bar per validator is optional; the table is the primary artefact.

**Validator detail `/validators/[valoper]`**
- Identity block: moniker, `celestiavaloper1… ` full address with copy, consensus address hex with copy, website (external), Celenium link (resolved via `/v1/search`), registered Fibre `host:port`, TLS identity check result at last probe.
- Window selector `24h · 7d · 30d` with printed UTC boundaries.
- Verdict share: `HEALTHY 412 / 430 · TOLERATED 11 · FAULT 4 · EXPECTED_GONE 3 · UNREACHABLE_POST_WINDOW 0 · NOT_PROBED 0` with the Wilson 95% lower bound for HEALTHY printed as `≥ 93.9% (95% lower bound, n=430)`.
- Probe log table: time (UTC), height, blob id (link), row index, result, latency ms, error string. Sorted newest first. Gaps in the collector's coverage are drawn as a grey row "collector gap 12:04–12:19 UTC (no probes)".

**Blob detail `/blobs/[id]`**
- Header: namespace (display `29c6 ••• 7a77` with copy of the full 56-hex; alias if known), commitment (hex, `shortHex` style, copy full), height (link to Celenium block), tx (link), size (`formatBytes` style, binary units), `ExpiresAt`, `pruneAt = max(ExpiresAt, created + ShardRetention)`.
- Reconstructability indicator (§6.5).
- Probe timeline component (§6.4).
- Assigned-validator table: validator, rows assigned, rows served at last probe, last verdict, last probe time.

**Methodology `/methodology`** — one anchor per term: `#verdicts`, `#windows`, `#reconstructable`, `#probe`, `#sampling`, `#wilson`, `#vantage`. Quote the spec for rows/ratio and `pruneAt`; quote the forum thread for "no slashing" and "1/3 honest suffices"; state what the tool does not do (no availability proof; no slashing evidence; no legal claim).

**About `/about`** — operator identity, vantage (city, hosting provider, ASN), probe cadence, retention of raw probes, data licence, "Not affiliated with the Celestia Foundation", Celenium attribution line, source repository link, `celestia-app` pinned commit.

**API `/api`** — list of JSON files the static export publishes (`/api/v1/overview.json`, `/api/v1/validators/<valoper>.json`, `/api/v1/blobs/<id>.json`), their schema, and the same `schema_version`/`collector_height`/`generated_at` envelope on each.

### 5.4 Status footer (every page)

`collector height 4,123,456 · last probe 2026-09-07 16:42:10 UTC · vantage fra1 (AS16509) · celestia-app v6.x @ 1a2b3c4 · data generated 16:45:00 UTC · Data provided by Celenium API` — the last item satisfies Celenium's free-tier terms ([api-docs](https://api-docs.celenium.io/)). If `generated_at` is older than 2× the probe cadence, the footer turns into a full-width notice "Data is stale (last generated 3 h ago)" — an honest equivalent of Statuspage's grey "no data" days ([Statuspage](https://support.atlassian.com/statuspage/docs/display-historical-uptime-of-components/)).

---

## 6. Design spec for the front-end implementer

Plain, implementable, no marketing. Sources are the ones cited in §1–§4; each subsection names the ones that motivate it.

### 6.1 Colour tokens

Surfaces and text come from the Celestia brand page (Frost/Void/Neutral/Indigo/Amethyst) so the site reads as part of the ecosystem without using the marks ([celestia.org/brand](https://celestia.org/brand/)). Status colours come from Okabe-Ito ([Wilke](https://clauswilke.com/dataviz/color-pitfalls.html)) because the Celestia palette has no red/orange and because the status set must be colour-blind safe. Contrast values are from §4.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg` | #FDFCFF (Frost) | #0E1014 (Void) | page |
| `--surface` | #FFFFFF | #242830 (Neutral 800) | cards, table body |
| `--surface-2` | #EAECEE (Neutral 50) | #1A1D22 (between Void and Neutral 800; not a brand value — stated as ours) | table header, code blocks |
| `--border` | #C4C8CE (Neutral 200) | #4A5058 (Neutral 600) | 1px rules |
| `--text` | #0E1014 | #FDFCFF | body (18.6:1) |
| `--text-2` | #4A5058 (Neutral 600, 7.96:1) | #C4C8CE (Neutral 200, 11.3:1) | secondary |
| `--text-3` | #808890 (Neutral 400, 3.5:1 — large text/labels ≥14px bold only) | #808890 (5.3:1) | tertiary, placeholders |
| `--link` | #5640D1 (Indigo, 6.7:1) | #7C68F2 (Amethyst, 4.6:1) | links, focus ring |
| `--status-healthy` | #009E73 | #009E73 | fills/icons only |
| `--status-tolerated` | #E69F00 | #E69F00 | fills/icons only; **2.2:1 vs Frost — never a boundary on its own** |
| `--status-fault` | #D55E00 | #D55E00 | fills/icons only |
| `--status-expected-gone` | #808890 (Neutral 400) | #808890 | fills/icons |
| `--status-post-window` | #0072B2 | #56B4E9 | fills/icons; informational |
| `--status-not-probed` | transparent + `--border` dashed | same | never a fill |
| `--gap` | repeating 45° hatch of `--border` on `--surface-2` | same | collector gaps |

Theme default: follow the system (`prefers-color-scheme`), matching docs.celestia.org (`defaultTheme: "system"`, seen 7 Sep 2026); provide a toggle. Both palettes must be fully defined; do not rely on Celenium's dark-first `:root` trick.

### 6.2 Typography and density

- Body: system sans stack (`ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Inter, sans-serif`). UntitledSans and Youth are licensed, self-hosted fonts on celestia.org/docs ([curl of docs.celestia.org](https://docs.celestia.org/)); we have no licence for them, so do not copy the files. Inter is an acceptable free substitute and is what Celenium uses ([nuxt.config.ts](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/nuxt.config.ts)); load it self-hosted, not from Google Fonts, to avoid a third-party request on a trust page.
- Mono for every identifier and number column: `ui-monospace, "JetBrains Mono", "Roboto Mono", Menlo, monospace` with `font-variant-numeric: tabular-nums` (Celenium's `.tabular` and `.mono` classes; celestia.org uses Roboto Mono).
- Scale: 13px table text, 14px body, 16px lead, 20/24/32px headings; line-height 1.4 tables / 1.6 prose (Celenium's `fz--13/14` and `lh--140/160` utilities are the precedent).
- Content width 992–1200px; tables may overflow inside a horizontally scrolling container; never hide columns on mobile — show fewer rows instead (GOV.UK table guidance on small screens: reduce text size only when the table "has a lot of data" and consider splitting; [GOV.UK Table](https://design-system.service.gov.uk/components/table/)).
- Numeric columns right-aligned; identifiers left-aligned; captions on every table ([GOV.UK Table](https://design-system.service.gov.uk/components/table/)).

### 6.3 Verdict badge set

Rules: text always present; icon glyph always present; colour third (WCAG 1.4.1; GOV.UK Tag). Badge = 1px `--border`, 4px radius, `--text` label in 12px semibold mono, 12px glyph on the left drawn in the status colour and also in `--text` stroke so it survives greyscale. Light mode fill = status colour at 12% over `--surface`; dark mode fill = solid status colour with Void text (measured ≥4.9:1).

| Class | Label text | Glyph | Colour token | Severity rank (for sorting and for the overview's summary colour, following Statuspage/incident.io ordering) | One-line definition shown in tooltip and on `/methodology#verdicts` |
|---|---|---|---|---|---|
| `HEALTHY` | `healthy` | filled circle ● | `--status-healthy` | 0 (nominal, quiet) | Assigned row(s) served correctly within the must-serve window. |
| `TOLERATED` | `tolerated` | half-filled circle ◐ | `--status-tolerated` | 1 | Probe failed or was slow, but within the tolerance stated in methodology (e.g. one failure with a later success in the same window). |
| `FAULT` | `fault` | filled triangle ▲ | `--status-fault` | 2 (highest) | Assigned row not served during the must-serve window after retries. |
| `EXPECTED_GONE` | `expected gone` | hollow circle ○ | `--status-expected-gone` | — (not a failure; excluded from rates) | Probe after `pruneAt`; absence is correct behaviour. |
| `UNREACHABLE_POST_WINDOW` | `unreachable after window` | hollow diamond ◇ | `--status-post-window` | — (informational; excluded from rates) | Endpoint unreachable after `ExpiresAt`+grace; no promise was broken. |
| `NOT_PROBED` | `not probed` | dash — | `--status-not-probed` | — (excluded from rates; counted separately) | The collector did not probe this validator/blob pair (gap, quota, or not yet scheduled). |

Rates: `HEALTHY / (HEALTHY + TOLERATED + FAULT)`; the excluded classes are listed with counts next to the rate, never folded in (Spark's `nonZero` precedent of making exclusions explicit; [spark-stats](https://github.com/CheckerNetwork/spark-stats)).

### 6.4 Validator list table

| Column | Content | Align | Sort |
|---|---|---|---|
| Validator | moniker (link to detail) + `celestiavaloper ••• xxxx` in mono under it (Celenium `splitAddress` pattern), copy button for the full address | left | A–Z |
| Fibre endpoint | registered `host:port` or `— not registered` | left | — |
| Last verdict | badge + `12 min ago` + absolute UTC on hover | left | severity |
| Healthy rate (window) | `97.1%` then `(34/35)` in `--text-2`; Wilson 95% lower bound as `≥93.9%` on hover and in detail | right | numeric on lower bound |
| Fault | count | right | numeric |
| Tolerated | count | right | numeric |
| Not probed | count | right | numeric |
| Last probe | UTC `2026-09-07 16:42:10Z`, relative on hover | right | time |
| Links | Celenium ↗ | — | — |

Defaults: sort by severity of last verdict, then by Wilson lower bound ascending (worst first — the FDP reviewer's question), then moniker. Page size 100 (the active set is ~100 validators; Celenium paginates its list five pages deep with "1 of 5" and no reader benefits from paging a 100-row monitor). Sticky header; a search box that matches moniker, valoper, consensus address, and Fibre host. Row permalink `/validators/<valoper>` (operators find their row by search or by appending their own address).

Time convention: absolute UTC everywhere in tables (`YYYY-MM-DD HH:MM:SSZ`), relative time as the secondary hover/tooltip text. This inverts Celenium's `relative + local absolute` ([celenium.io/txs](https://celenium.io/txs)) because our readers compare across sites and the spec's `ExpiresAt` is an absolute instant.

### 6.5 Probe timeline component (blob detail)

Modelled on Grafana's state timeline (one row per series, discrete state regions, nulls drawn as gaps; [Grafana](https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/state-timeline/)) and on tenderduty/ping.pub cell grids ([tenderduty](https://github.com/blockpane/tenderduty/blob/main/docs/README.md), [ping.pub](https://raw.githubusercontent.com/ping-pub/explorer/master/src/modules/%5Bchain%5D/uptime/index.vue)).

- x axis: wall-clock time from settlement (`MsgPayForFibre` inclusion) through `ExpiresAt`, then `pruneAt` (= `max(ExpiresAt, created + ShardRetention)`), then the grace boundary, then the post-window tail. Draw vertical rules at each boundary with labels `settled`, `ExpiresAt`, `pruneAt`, `grace end`. Print the four UTC instants in a caption.
- y axis: one row per assigned validator, ordered by row-index range; label = moniker (link) + row range `rows 0–163`.
- Cells: one cell per probe, width proportional to probe interval; fill by verdict token; glyph inside the cell when the cell is ≥16px wide, otherwise the glyph appears in the tooltip. `NOT_PROBED` intervals are empty cells with a dashed border; collector gaps are the hatched `--gap` pattern across all rows.
- Region shading behind the cells: must-serve window in `--surface-2`; post-window tail unshaded; nothing after the grace boundary is counted in any rate.
- Tooltip per cell: verdict word, UTC time, height, row index, latency, error string, TLS identity check result.
- Legend below the chart lists all six badges with their glyphs (ping.pub renders a legend row; we always do).
- Reduced-motion: no animation. Keyboard: cells are focusable in row order.

### 6.6 Reconstructability indicator (blob detail)

- Text first: `Reconstructable: yes — 15,872 distinct rows held of 16,384; 4,096 needed` or `Reconstructable: no — 3,900 distinct rows held; 4,096 needed`. The parameter values come from the spec ("total rows: 16384", "original rows: 4096"; [fibre_encoding.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_encoding.md)) and are printed, not assumed.
- Bar: a single horizontal bar of 16,384 units with a tick at 4,096; fill = distinct rows we observed served at the latest probe round; fill colour `--status-healthy` when ≥4,096, `--status-fault` when below, `--status-expected-gone` after `pruneAt`. The word "yes/no" and the counts carry the meaning; the bar is secondary.
- Caveat line directly under: `Based on rows observed from one location in the last probe round at 16:40:12Z. Rows we did not probe are not counted.`
- Do not extrapolate: if fewer than 4,096 rows were probed at all, print `Unknown — only 2,048 rows probed; cannot determine` in `--text-2`, with the `NOT_PROBED` glyph.

### 6.7 "Observed from one location" banner

- Position: below the header on every page; not dismissable; `--surface-2` background, 1px `--border`, an "i" glyph in `--text-2`.
- Text (exact): `Observed from one location: <city>, <provider>, AS<n>. A failed probe means this vantage could not fetch the row at that time; it is not proof the validator is down. A successful probe is not proof of availability from elsewhere.`
- Link: `How probing works →` to `/methodology#vantage`.
- Motivation: Spark trusts committee consensus, not one checker ([docs.filspark.com committee-building](https://docs.filspark.com/measurement/committee-building), search snippet); L2BEAT states what it does not do ([FAQ](https://l2beat.com/faq)).

### 6.8 Empty and degraded states

| State | Rendering |
|---|---|
| Before Fibre activation | Overview shows one card: `0 Fibre publications since height <activation height>. Collector at height <N>, following the chain. Last check <UTC>.` Validator table still lists validators with `Fibre endpoint` column populated from `query valaddr providers` and every verdict cell showing `not probed`. |
| Validator has no registered endpoint | Endpoint cell `— not registered`; verdict `not probed`; tooltip "No `MsgSetFibreProviderInfo` host on chain" ([fibre_registry_module.md](https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_registry_module.md)). |
| No probes in the selected window | Rate cell prints `— (0 probes)`, never `0%` or `100%` (Grafana's "No value" default is a hyphen; [Grafana](https://grafana.com/docs/grafana/latest/panels-visualizations/configure-standard-options/)). |
| Collector gap | Hatched band in timelines; grey row in probe logs with start/end UTC; overview footer notice if the gap is ongoing. |
| Stale export | Footer becomes a full-width notice (§5.4). |
| Blob after `pruneAt` | Header badge `expected gone`; reconstructability shows the last known value with its timestamp and the note "shards may be pruned as specified". |
| Search miss | `No validator matches "<query>". Try the moniker, celestiavaloper…, or consensus address.` |

### 6.9 Identifier rendering rules (aligned with Celenium so operators recognise them)

- Bech32: `celestiavaloper ••• z3tl` (prefix + last 4) in lists; full string with copy button in detail headers ([general.js](https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js)).
- Hex hashes and consensus addresses: first 8 ` ••• ` last 8, upper-case as stored; copy full.
- Namespaces: strip leading `00` bytes for display (`29c6 ••• 7a77`), keep full 56-hex in URLs and copy; show alias when Celenium provides one, marked `(alias from Celenium)`.
- Commitments: hex, `shortHex` pattern; offer base64 in the copy menu because SDK users see base64.
- Heights: plain integers with thousands separators, link to `https://celenium.io/block/<height>` (or `mocha.celenium.io`).
- Bytes: binary units (`KiB`, `MiB`) to match Celenium.

### 6.10 Mobile

- Overview: stat tiles stack; the validator table becomes a card list with the same fields in the same order; no field is dropped (colour and glyph both present, text always present).
- Timeline: the y-axis labels collapse to a fixed 96px column with truncated monikers; the chart scrolls horizontally inside its container; legend remains visible above the chart.
- Tap targets ≥44px; tooltips become tap-to-open panels.

---

## 7. What could not be established (say so in the UI and docs)

- Fonts of celestia.org/docs are UntitledSans, Youth, NuberNext (self-hosted, licensed); no public licence for reuse was found. We do not use them.
- No Celestia trademark policy page exists; only the ToS trademark clause (16 Jan 2023). We avoid the marks.
- forum.celestia.org theme colours/fonts and status.celestia.org fonts were not readable from static HTML.
- Spark's live dashboard (filspark.com hosts) could not be reached from this environment on 7 Sep 2026; Spark facts are from its GitHub README, a roadmap issue, and search snippets of docs.filspark.com.
- L2BEAT chart range selectors and tooltip text could not be read (page body truncated); the rosette semantics come from the forum framework post.
- No reference dashboard in this set displays Wilson intervals; that element is our own choice, justified by the small-n literature cited in §4.
- Celenium's `identity` → avatar mapping is not documented by Celenium; the Keybase lookup pattern is cited from ping.pub's source, not from Celenium.

---

## Sources (all seen 7 September 2026)

Celestia properties and repos
- https://celestia.org/brand/
- https://celestia.org/press/
- https://celestia.org/tos/
- https://celestia.org/ (HTML/CSS via curl)
- https://raw.githubusercontent.com/celestiaorg/celestia.org/main/tailwind.config.js
- https://github.com/celestiaorg/celestia.org
- https://docs.celestia.org/ (HTML via curl: fonts, Nextra hue tokens, `defaultTheme: "system"`)
- https://github.com/celestiaorg/docs
- https://raw.githubusercontent.com/celestiaorg/docs/main/app/globals.css
- https://docs.celestia.org/operate/networks/mainnet-beta/
- https://docs.celestia.org/learn/celestia-101/resources/
- https://docs.celestia.org/community/foundation-delegation-program
- https://blog.celestia.org/
- https://forum.celestia.org/
- https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288
- https://status.celestia.org/
- https://cips.celestia.org/cip-051.html
- https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_encoding.md
- https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_server.md
- https://raw.githubusercontent.com/celestiaorg/celestia-app/main/specs/src/fibre_registry_module.md
- https://github.com/celestiaorg/celestia-app/tree/main/specs/src
- https://github.com/celestiaorg/fibre-da-spec (archived 2 Mar 2026; points to celestia-app specs)

Celenium
- https://github.com/celenium-io/celenium-interface
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/base.scss
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/assets/styles/text.scss
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/nuxt.config.ts
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/services/utils/general.js
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/validator/ValidatorOverview.vue
- https://raw.githubusercontent.com/celenium-io/celenium-interface/master/components/modules/block/BlockOverview.vue
- https://celenium.io/validators · https://celenium.io/validator/2 · https://celenium.io/block/1 · https://celenium.io/txs · https://celenium.io/namespaces · https://mocha.celenium.io/
- https://api-docs.celenium.io/ · https://api-docs.celenium.io/api-8204946 · https://api-plans.celenium.io/
- https://api-mainnet.celenium.io/v1/validators?limit=1&offset=0 · https://api-mainnet.celenium.io/v1/validators/2 · https://api-mainnet.celenium.io/v1/search?query=celestiavaloper14ntfv0qkg8522xe0pvrfgqxcmnj5x466v8z3tl · https://api-mocha.celenium.io/v1/head

Reference dashboards
- https://l2beat.com/scaling/summary · https://l2beat.com/faq · https://l2beat.com/glossary · https://l2beat.com/data-availability/risk · https://l2beat.com/data-availability/projects/celestia/blobstream
- https://forum.l2beat.com/t/the-risk-rosette-framework/292
- https://github.com/CheckerNetwork/spark-stats · https://github.com/CheckerNetwork/roadmap/issues/192 · https://docs.filspark.com/measurement/committee-building (search snippet only) · https://dashboard.filspark.com/provider/f0101020 (search snippet only)
- https://github.com/blockpane/tenderduty/blob/main/docs/README.md
- https://raw.githubusercontent.com/ping-pub/explorer/master/src/modules/%5Bchain%5D/uptime/index.vue · https://raw.githubusercontent.com/ping-pub/explorer/master/src/stores/useStakingStore.ts
- https://grafana.com/docs/grafana/latest/panels-visualizations/configure-thresholds/ · https://grafana.com/docs/grafana/latest/panels-visualizations/configure-standard-options/ · https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/state-timeline/ · https://grafana.com/docs/grafana/latest/panels-visualizations/configure-value-mappings/
- https://support.atlassian.com/statuspage/docs/top-level-status-and-incident-impact-calculations/ · https://support.atlassian.com/statuspage/docs/display-historical-uptime-of-components/
- https://docs.incident.io/status-pages/severity

Accessibility and statistics
- https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html
- https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html
- https://clauswilke.com/dataviz/color-pitfalls.html
- https://jfly.uni-koeln.de/color/
- https://sronpersonalpages.nl/~pault/
- https://design-system.service.gov.uk/components/tag/ · https://design-system.service.gov.uk/components/table/
- https://en.wikipedia.org/wiki/Binomial_proportion_confidence_interval
- https://www.evanmiller.org/how-not-to-sort-by-average-rating.html
