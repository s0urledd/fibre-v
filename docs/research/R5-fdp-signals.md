> Note: section 5.1 reports the state of the Huginn validators (mocha-5, mocha-4, mainnet) as read from public indexers and LCDs on 6 September 2026. Re-check every one of those rows on the day of submission; they are observations, not facts about intent, and the owners may already have acted on them.

# R5 — Celestia Foundation Delegation Program (FDP): signals for a Cohort 9 application

Prepared 6 September 2026 for Huginn Tech (mocha-5 validator moniker `Huginn`). All sources were read on 6 Sep 2026 unless stated. Quotes are verbatim; nothing in the eligibility sections is paraphrased. Where a page was only readable through a summarising fetcher (GitHub commit pages) this is marked "(fetch summary)".

Sources used most: FDP docs page [docs.celestia.org/operate/consensus-validators/foundation-delegation-program/](https://docs.celestia.org/operate/consensus-validators/foundation-delegation-program/) (page stamps `Last updated on September 3, 2026`; docs source file `app/operate/consensus-validators/foundation-delegation-program/page.mdx`, last content change merged 31 Aug 2026 as PR #2572 "Update FND delegation docs"), forum threads 2007, 2008, 2107, 2181, 2208, 2222, 2232, 2252, 2281, 2288, 2295, the Foundation's selection spreadsheet, the Feb-2024 launch blog post, CIP-51, celestia-app v10 release notes, and live chain data from public mocha-5 / mainnet LCDs and Celenium.

---

## 1. The FDP docs page, verbatim (read 6 Sep 2026)

Page: https://docs.celestia.org/operate/consensus-validators/foundation-delegation-program/ — footer: `Last updated on September 3, 2026` (HTML `<time>` value 2026-09-03). Note the 3 Sep stamp is from an automated release-tag commit; the substantive wording below landed in PR #2572, merged 31 Aug 2026 (see §2.4).

### 1.1 Objectives

> The primary objectives of the Celestia Foundation Delegation Program are:
> - To provide a fair opportunity for Celestia's users to join the validator set, while ensuring the validator set remains proficient, trustworthy, and dependable.
> - To maintain network stability by promoting a steady transition of validators and avoiding sudden and disruptive changes in participation.
> - To enable the Celestia Foundation to use its stake towards its mission of fostering a modular blockchain network that delivers exceptional performance.

### 1.2 Evaluation / selection language

> Prospective validators are welcome to apply to the program. The application is designed to assess a validator's uptime performance and contributions to the Celestia ecosystem. Of the 100 total slots in Celestia's active validator set, up to 35 will receive delegations within the program.
>
> Application submissions will be reviewed by the Celestia Foundation.

> During this period, so long as the validator maintains high uptime and does not violate the rules of the program, the validator will receive the delegation for the duration of the cohort they are currently in.

> Please note, the objective of the program is to contribute to Celestia's resilience and uptime. If you contribute a lot to the Celestia ecosystem, but your validator uptime is low, this will negatively impact your chance at selection for the program. Furthermore, merely receiving delegation from the Foundation under the program does not guarantee your placement in the active validator set.

> Applicants are also expected to have reviewed Celestia docs and recommended guides on devops and monitoring setups.

From the launch blog post (6 Feb 2024, [blog.celestia.org/validator-delegation-program/](https://blog.celestia.org/validator-delegation-program/), read 6 Sep 2026) — still the only published statement of selection weighting:

> The Foundation will assess applications to the program based on a comprehensive set of considerations. These primarily include the number of validators previously elected in earlier cohorts, overall network stability, and validator applicant quality. Other considerations include contributions to the Celestia ecosystem and geolocation.

### 1.3 Timeline rules (cohort length, seats, deadlines)

> Every 6 months, the Celestia Foundation will distribute a portion of the Foundation's total available stake to a cohort of validators who meet certain criteria, detailed below.

> ### Key Points
> - The program is split into Cohorts every 6 months.
> - Each Cohort has to renew in 12 months, except the bottom 7 validators.
> - The bottom 7 validators renew every 6 months.
> - Open slots may be filled by existing Cohort members up for renewal or new applicants.

| Cohort | Cohort Size | Delegation Duration | Renewal By Cohort |
|---|---|---|---|
| Cohort 9 | 35 | 12 months | Cohort 11 |
| Cohort 10 | 7 | 6 months | Cohort 11 |
| Cohort 11 | 35 | 12 months | Cohort 13 |
| Cohort 12 | 7 | 6 months | Cohort 13 |

> This structure allows for a steady flow of both existing applicants and new applicants to maintain a stable set of participants in the program.

> The program will be divided into cohorts with applications open for new applicants and renewal of existing applicants every 6 months. Validators will be delegated for up to a year. For each cohort, the deadline to apply/be evaluated (if you are reapplying) is exactly 1 month prior to the date of being delegated to.

> Selected validators are required to support both Mainnet Beta and Mocha networks as an active validator and Fibre support (once Fibre is live).

Cohort information list on the page:

> - Cohort 1 : 50 Validator Seats
> - Cohort 2 : 15 Validator Seats (Applications open June 1, 2024)
> - Cohort 3 : 15 Validator Seats (Applications open October 1, 2024)
> - Cohort 4 : 20 Validator Seats (Applications open February 1, 2025)
> - Cohort 5 : 15 Validator Seats (Applications open June 1, 2025)
> - Cohort 6 : 15 Validator Seats (Applications open October 1, 2025)
> - Cohort 7 : 20 Validator Seats (Applications open February 1, 2026)
> - Cohort 8 : 15 Validator Seats (Applications open June 1, 2026)

### 1.4 Mandatory eligibility criteria (verbatim, complete)

> The minimum requirements for participation in the program are as follows:
> - Run an active Mainnet Beta validator or an active Mocha testnet validator for at least 2 months before application deadline
> - The validator has to have significant stake to be able to propose blocks
> - Not jailed more than once in the 6 months before application deadline
> - If jailed more than once in the 6 months period before application deadline, then we require a public forum post with detailed post mortem and we consider case by case
> - Not associated with an exchange or custodian
> - Not in the top 10 validators by delegation power, unless it enters the top 10 as a result of the Foundation's delegation under this program
> - Have 25% or less commission
> - Not based within the US, within any country subject to economic sanctions, or within any other prohibited jurisdiction, and successfully complete a compliance screen
> - Dedicated email address so that the Foundation can reach you in the event of emergency upgrades and fixes
> - Not running your infrastructure in Hetzner or OVH
> - Run Fibre once it is live
>
> Not adhering to any of the criteria above will automatically disqualify your application, and violating any of the criteria after you have received delegation will result in withdrawal of the delegation. A participant who loses stake due to being jailed by the protocol may reapply to the program after 2 cohort periods.

### 1.5 Optional criteria (verbatim, complete)

> Other optional but important criteria:
> - Develop and maintain developer tooling, services, applications, and dashboards
> - Work on projects aligned with Celestia's values
> - Contribute to documentation and new guides and tutorials
> - Quality of infrastructure
> - Operated within a location that improves geolocation of the validator set

### 1.6 Undelegation criteria (verbatim, complete)

> - Getting slashed/tombstoned (cannot apply for 1 year afterwards)
> - Getting jailed more than once during the cohort's applicable delegation period
> - Violating the Celestia.org Community Code of Conduct or engaging in harmful activities towards the network
> - Failing to upgrade your node in a timely manner (24 hours or less)
> - If necessary to protect or secure Mainnet Beta or to comply with applicable law
> - For any other reason, in the Celestia Foundation's sole discretion

### 1.7 What the application asks (verbatim)

> Before applying, be ready to share the following:
> - General info
>   - Security Email
>   - Validator Entity Name
>   - Discord ID
>   - Mark if entity or individual
>   - Website if any
>   - Github page of your organization
>   - Team experience and roster (including Twitter + Github links)
>   - Which networks you validate on Mainnet + links to your validators
>   - A personal statement why you should receive delegation from the Foundation (max 1500 characters)
> - Infrastructure
>   - Validator address on Mainnet Beta
>   - Validator address on Mocha testnet
>   - Have you been slashed or jailed in the last 6 months on Celestia or other chains you validated on.
>   - Hosting provider and Data Center location (Mainnet Beta and testnet if applicable)
>   - Setup of the validator
>   - Hardware
>   - Security setup (servers, private keys)
>   - Monitoring and alerting
> - Contributions
>   - Please list all contributions for Celestia and its ecosystem

The Google Form linked from every cohort thread (https://forms.gle/9n6WCskCnytdLEyp9 → docs.google.com/forms/d/e/1FAIpQLSdlqBFtkNWcPYC1wcP98Y4AP06XeIVi3RSPPoTnxRKer9eljg) currently shows (6 Sep 2026): "The form Celestia Foundation Delegation Program Cohort 8 Application is no longer accepting responses." The same short link has been reused since Cohort 5, so expect it to reopen with a Cohort 9 title on 1 Oct 2026; the question list above is the only public copy of what it asks.

### 1.8 Keyword check on the docs page

| Term | Present on page? | Where |
|---|---|---|
| "contribution(s)" | yes | "contributions to the Celestia ecosystem"; "Please list all contributions for Celestia and its ecosystem"; "If you contribute a lot ... but your validator uptime is low ..." |
| "tooling" | yes | "Develop and maintain developer tooling, services, applications, and dashboards" |
| "dashboards" | yes | same bullet |
| "Fibre" | yes (twice) | "Run Fibre once it is live"; "Fibre support (once Fibre is live)" |
| "ecosystem" | yes | see contributions |
| "community" | yes | only in "Celestia.org Community Code of Conduct" |
| "education" | no | — (closest: "Contribute to documentation and new guides and tutorials") |
| "governance" | no | — |
| "infrastructure" / "public RPC" | "infrastructure" yes ("Quality of infrastructure"; "Not running your infrastructure in Hetzner or OVH"); "RPC" no | — |
| "uptime" | yes (4×) | "assess a validator's uptime performance"; "maintains high uptime"; "resilience and uptime"; "validator uptime is low" |
| "monitoring" | yes | "Monitoring and alerting"; "recommended guides on devops and monitoring setups" |

---

## 2. Cohorts 5–8 on the forum: dates, numbers, wording, changes

### 2.1 Timeline table (all dates UTC, from Discourse post timestamps; "selected" counted from the Foundation's spreadsheet tabs)

| Cohort | "is open" post | Seats announced | "is live" (results) post | Selected (spreadsheet) | Applicants | Docs "applications open" date |
|---|---|---|---|---|---|---|
| 5 | 2 Jun 2025 09:15 ([t/2008](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-5-is-open/2008)) | 15 | 1 Aug 2025 15:21 ([t/2107](https://forum.celestia.org/t/foundation-cohort-5-is-live/2107)) | 15 | not published | June 1, 2025 |
| 6 | 1 Oct 2025 11:52 ([t/2181](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-6-is-open/2181)) | 15 | 8 Dec 2025 12:52 ([t/2208](https://forum.celestia.org/t/foundation-delegation-cohort-6-is-live/2208)) | 15 | not published | October 1, 2025 |
| 7 | 2 Feb 2026 15:35 ([t/2222](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-7-is-open/2222)) | 20 | 1 Apr 2026 18:12 ([t/2232](https://forum.celestia.org/t/foundation-delegation-cohort-7-is-live/2232)) | 20 | not published | February 1, 2026 |
| 8 | 1 Jun 2026 09:01 ([t/2252](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-8-is-open/2252)) | 15 | 3 Aug 2026 10:45 ([t/2281](https://forum.celestia.org/t/foundation-delegation-cohort-8-is-live/2281)) | 15 | not published | June 1, 2026 |
| 9 | announced for 1 Oct 2026 ([t/2281](https://forum.celestia.org/t/foundation-delegation-cohort-8-is-live/2281)) | 35 (docs table) | — | — | — | — |

Selected-validator lists (spreadsheet `1Fxu9uYJ4wxfHChEiSg5bmXAMU8IZSq7J3GYDCFgk1HA`, CSV export read 6 Sep 2026):
- Cohort 6 (gid 1010270944, 15): VALIDEXIS, bitszn, TTT VN, NodeStake, ContributionDAO, Nodes.Guru, ChainTrails Team, [NODERS], CryptoCrew Validators, Easy 2 Stake, Cumulo, BlackNodes, Enigma, Krews, blanc.
- Cohort 7 (gid 224284124, 20): Smart Stake, CosmoWiz, NakoTurk, mammoth.guru, F5 Nodes, Cosmostation, Stakely.io, alphab.ai, DSRV, CHAIN DIGITAL, kjnodes, Validatus, B-Harvest, Upnode, Binary Builders, deNodes, Stakin by The Tie, BlockPro, pro-nodes75, Staking Facilities.
- Cohort 8 (gid 1370047249, 15): Dragonfly, CroutonDigital, Endorphine Stake, quantumcore, DTEAM, LuckyResearch Labs, 01node, ECAD Infra, STAKEME, node101, StakeUp, Metatarz, Astrosynx, PPBCN_Val, itrocket.

The Foundation has never published applicant counts or a ratio. Every "is live" post says only "You can find the list of validators receiving delegations in the spreadsheet". Seats announced were fully filled in every cohort 5–8.

### 2.2 Every phrase in cohort 6–8 threads about what the Foundation values or how it selects

Cohort 8 "is live", Bidon15, 3 Aug 2026 ([t/2281](https://forum.celestia.org/t/foundation-delegation-cohort-8-is-live/2281)):

> Cohort 9 applications will open up on October 1st, 2026. As a reminder, for Cohort 9, all validators that were selected in Cohort 6 will have to reapply, along with new validator applicants.
>
> For the next Cohort, the Foundation will reject any applicants who run their infrastructure on OVH or Hetzner to foster provider decentralization. European and North American locations are greatly discouraged. Please note that we also do not advise Contabo. Furthermore, any new validator who wishes to apply to Cohort 9 is advised to start running their validator node on Mocha testnet ahead of applications opening on October 1st. The community managers on the Celestia Discord are happy to help get you to the active set on Mocha testnet. This ensures uptime performance can be measured prior to applications opening.
>
> Thank you to everyone who applied, and we look forward to seeing new applicants for Cohort 9. We also wanted to thank the whole P-OPS team for doing a lot of the heavy lifting that made the delegation program possible.
>
> Thank you for keeping the Celestia network secure and resilient.

Cohort 8 "is open", ismail, 1 Jun 2026 ([t/2252](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-8-is-open/2252)):

> For all new validator applicants, you can proceed with the application form. Upon selection, new validator applicants must complete a compliance screen.
>
> Cohort 8 has 15 seats up for delegation. Members of Cohort 8 will receive delegation for up to 12 months as long as they follow the Delegation Program guidelines.

Cohort 7 "is live", ismail, 1 Apr 2026 ([t/2232](https://forum.celestia.org/t/foundation-delegation-cohort-7-is-live/2232)) — identical template to Cohort 8 except the sentence "European and North American locations are greatly discouraged." is absent, and it reads "start running their validator node on Mocha testnet".

Cohort 6 "is live", ismail, 8 Dec 2025 ([t/2208](https://forum.celestia.org/t/foundation-delegation-cohort-6-is-live/2208)) — same template; reads "start running their validator node and bridge node on Mocha testnet".

Cohort 6 and 7 "is open" posts (t/2181, t/2222) use the same template as Cohort 8 "is open" (15 and 20 seats respectively).

Cohort 5 "is open", ismail, 2 Jun 2025 ([t/2008](https://forum.celestia.org/t/celestia-foundation-delegation-program-cohort-5-is-open/2008)) — the one explicit statement about weighting:

> Please note that Cohort 5 features a few changes, including allowing to be jailed once and less weight on contributions as before due to the new program that we have launched. The new program will run as an addition to this one.

The "new program" was the Celestia Ecosystem Delegation Program (July–Dec 2025), ismail, 2 Jun 2025 ([t/2007](https://forum.celestia.org/t/celestia-ecosystem-delegation-program-july-dec-2025-is-now-open/2007)): "a six-month pilot to support ten validators who drive ecosystem growth" with tracks "Mamo-Users", "Bootcamp & Mammothon", and "R&D – create CIPs or public-goods tooling". Its docs handbook URL now returns 404 (6 Sep 2026); no renewal has been announced. Whether "less weight on contributions" still applies after the pilot ended is not stated anywhere.

Community feedback on the process, in the Foundation's own threads (useful because nothing else public describes selection):

- kingsuper, 1 Jul 2025 (t/2008): "We've applied in all four past cohorts and were not selected each time. While we understand it's competitive, what's been difficult is the lack of a feedback loop ... we believe that the review process should ideally be handled by the Foundation team rather than fellow validator teams."
- ertemann (Lavender.Five), 31 Jul 2025 (t/2008): "It seems there is an insanely large focus on the location of the hardware used by the validator. To such an extent that dedicated-server validators in more remote locations are favoured over validators that use co-located or self-hosted hardware in more packed jurisdictions."
- Brightlystake, 14 Aug 2025 (t/2107): "congratulations for those selected, we feel disappointed to miss out."
- validexis, 8 Dec 2025 (t/2208): "We are happy to join the delegation program, thank you very much for your trust."

### 2.3 Changes between cohorts (forum)

| Change | First seen | Source |
|---|---|---|
| OVH/Hetzner rejected; Contabo "not advised" | Cohort 5 "is live", 1 Aug 2025 (applies from Cohort 6) | t/2107 |
| "validator node and bridge node on Mocha" → "validator node on Mocha" (bridge node dropped) | Cohort 7 "is live", 1 Apr 2026 | t/2208 vs t/2232 |
| "European and North American locations are greatly discouraged" | Cohort 8 "is live", 3 Aug 2026 (applies to Cohort 9) | t/2281 |
| Seats: 15 → 15 → 20 → 15 → 35 | Cohort 9 = 35 per docs table | docs page |
| Announcer changed from `ismail` to `Bidon15` | 3 Aug 2026 | t/2281 |

### 2.4 Changes between cohorts (docs page source history, `page.mdx`; GitHub commit pages read 6 Sep 2026, fetch summaries)

| Date | Commit | What changed (verbatim where captured) |
|---|---|---|
| 13 Feb 2026 | cb6da78 "docs: remove BNs from DFP (#2416)" (Bidon15) | Removed "Run an archival bridge node on Mainnet Beta that is connected and reporting ..." and "IMPORTANT: Each validator selected for the program has to maintain a fully archival (non pruned) bridge node for Mainnet Beta." **Added "Run Fibre once it is live".** Changed to "Run an active Mainnet Beta validator **and** an active Mocha testnet validator". |
| 24 Feb 2026 | ed7019d (mindstyle85) | "and" → "or" ("Run an active Mainnet Beta validator **or** an active Mocha testnet validator for **at least 1 month before application deadline**"). Added "Selected validators are required to support both Mainnet Beta and Mocha networks as an active validator and Fibre support (once Fibre is live)." |
| 18 Jun 2026 | 104869a (mindstyle85) | Added "- The validator has to have significant stake to be able to propose blocks"; Cohort 8 seats TBD → 15. |
| 25 Aug 2026 | 7745edd, 95a198c (mindstyle85) | "Every 4 months" → "Every 6 months"; "up to 50" → "up to 35"; "at least 1 month" → "at least 2 months before application deadline"; "Each Cohort has to renew in 12 months, except the bottom 7 validators." + "The bottom 7 validators renew every 6 months."; Cohort 9–12 table added (35/7/35/7). |
| 31 Aug 2026 | PR #2572 "Update FND delegation docs" merged (mindstyle85) | Same content as above, merged to main. |
| 3 Sep 2026 | automated release-tag commit | Only the page's "Last updated" stamp. |

So, relative to Cohort 8 (June 2026), a Cohort 9 applicant faces: 2-month (not 1-month) activity lookback, a 6-month cadence, 35 seats, and the explicit stake-to-propose requirement. "Run Fibre once it is live" has been mandatory text since 13 Feb 2026 (Cohort 7 onwards).

### 2.5 Cohort 9 window — projection

Rules stated by the Foundation (only these were used):
1. "Cohort 9 applications will open up on October 1st, 2026." (t/2281)
2. "For each cohort, the deadline to apply/be evaluated (if you are reapplying) is exactly 1 month prior to the date of being delegated to." (docs)
3. "The application process will last a month, after which the Foundation will assess and select the most suitable candidates for delegation." (launch blog, Feb 2024)
4. "Run an active Mainnet Beta validator or an active Mocha testnet validator for at least 2 months before application deadline" (docs)

Observed pattern (cohorts 5–8): results post lands 61–68 days after the "is open" post (1 Aug, 8 Dec, 1 Apr, 3 Aug).

| Milestone | PROJECTION (not announced) | Basis |
|---|---|---|
| Applications open | 1 Oct 2026 (stated) | rule 1 |
| Application deadline | ≈ 1 Nov 2026 | rule 3 (window "will last a month") |
| Delegation date | ≈ 1 Dec 2026 | rule 2 (deadline = delegation − 1 month) |
| "Cohort 9 is live" forum post | early Dec 2026 | observed pattern |
| Validator must have been active since | ≈ 1 Sep 2026 | rule 4 applied to projected deadline |
| Compliance screen | after selection, before delegation | "Upon selection, new validator applicants must complete a compliance screen." |

The Foundation has never published the exact deadline in a forum post; treat every date except 1 Oct as a projection.

---

## 3. What "good" looked like in recent cohorts

Searches run (6 Sep 2026): forum.celestia.org, X/Twitter, Medium, Substack, Mirror for "Foundation Delegation" + "cohort 6/7/8" + selected/contributions/writeup. Result: **no public "why we were selected" writeups, application texts, or contribution lists from any Cohort 6–8 selectee were found.** The Foundation does not publish per-validator rationale, scores, or applicant counts; selectees are only listed by name and valoper in the spreadsheet.

What does exist publicly:

1. **Atlas Staking's open application page** for Cohort 7 ([atlasstaking.com/celestia-delegation/](https://atlasstaking.com/celestia-delegation/), undated, read 6 Sep 2026). Atlas is **not** in the Cohort 7 or 8 selected lists, so this shows what an unsuccessful-but-serious application emphasised:
   > "We published 'What is Celestia?' to explain modular DA in plain language"
   > "we've launched our Cosmos delegator dashboard and Celestia is one of the first networks on it."
   > "multi‑datacenter bare metal, sentry‑only connectivity to the signer, encrypted keys that never touch internet facing hosts"
   > "The Foundation cares about resilience, decentralization, and reliable block production."
   > "What we're missing is simply the voting power to match that level of commitment."
   Takeaway: generic staking guides and a multi-chain delegator dashboard were not enough on their own.

2. **Profiles of the selectees** (names only from the spreadsheet; the following is general public knowledge about these operators, not a Foundation statement, and not individually verified for this note): Cohort 6–8 lists are dominated by established multi-chain operators with public infrastructure or analytics (Cosmostation, Smart Stake, Nodes.Guru, kjnodes, itrocket, B-Harvest, DSRV, Staking Facilities, 01node, NodeStake, Stakin, Stakely) plus a tail of smaller operators. Cumulo (Cohort 6) publishes a Celestia "Resources of the Validator Community" series and a "Live Peers" tool; Krews (Cohort 6) publishes Celestia ecosystem write-ups on Medium.

3. **The Foundation's own emphasis, in order of how often it is repeated**: measurable uptime on Mocha before applications open (every "is live" post since Cohort 5), hosting-provider and geographic decentralisation (every post since Cohort 5, tightened for Cohort 9), then "developer tooling, services, applications, and dashboards" and "documentation and new guides and tutorials" (docs, optional criteria).

4. **Nothing in any Foundation text mentions governance participation, education programmes, or public RPC** as criteria. Governance appears nowhere; "education" appears nowhere; RPC appears nowhere on the FDP page.

Net: "good" = provably high uptime on mocha (and ideally mainnet), non-Hetzner/OVH/Contabo hosting outside Europe/North America, plus a concrete, Celestia-specific tool or dashboard that other operators use. The Fibre observer fits the tooling bullet more precisely than anything the Foundation has cited so far, because the Foundation's own engineer said the community is expected to build exactly this (see §4).

---

## 4. Mapping Huginn's deliverables onto the Foundation's phrases

Context the Foundation has already put in writing about this kind of tool — chamirachid1 (Celestia core), 3 Sep 2026, [t/2288](https://forum.celestia.org/t/fibre-shardretention-and-what-happens-when-an-assigned-validator-doesnt-serve/2288):

> Additionaly, the community will generally build dashboards that will:
> * Periodically check whether the provided fibre server addresses, in the `x/valaddr` module are reachable
> * Whether validators are serving the data
> * etc

and on the team's follow-up: "cool stuff 👍 ... definitely, feel free to write something and we can review it" (4 Sep 2026).

| Foundation phrase (verbatim) | Our deliverable | Evidence link type |
|---|---|---|
| "Develop and maintain developer tooling, services, applications, and dashboards" | Public Fibre observer dashboard ([PRODUCT NAME] at [DOMAIN]) probing every registered Fibre endpoint from outside; open-source monorepo `fibre-tlsverify`, `fibre-assign`, `fibre-sentinel`, `fibre-devnet` (Apache-2.0) | URL (dashboard); repo (github.com/plsgiveup/fibre — see attribution risk §5) |
| "Periodically check whether the provided fibre server addresses, in the `x/valaddr` module are reachable" (core team, t/2288) | `fibre-sentinel` reachability probe (DNS/TCP/TLS/download judged separately, p50 ~14 ms, p95 ~24 ms per probe on devnet) | repo + forum post t/2288 #3 |
| "Whether validators are serving the data" (core team, t/2288) | `fibre-sentinel` shard-serving checks across the retention window (4–5 scheduled points per assigned validator per publication), row verification against the commitment, prune-lag tolerance (`pruneAt + ~1m45s` measured) | repo + forum post t/2288 #3 |
| "Validator-endorsed TLS identity for Fibre gRPC connections" (CIP-51) | `fibre-tlsverify`: verifies the server certificate's consensus-key endorsement against the validator's on-chain consensus key | repo |
| "Deterministic assignment of encoded blob shards to validators" (CIP-51) | `fibre-assign`: off-chain reimplementation, "verified ... bit-for-bit against the reference implementation across ~890 scenarios" (t/2295) | repo (differential test suite) + forum post t/2295 |
| "Contribute to documentation and new guides and tutorials" | Forum research posts t/2288 (ShardRetention / non-serving validators) and t/2295 (read-path rate limiting without IP allowlists); spec fix accepted upstream: celestiaorg/CIPs PR #405 / commit 6b9c74f "docs: correct CIP-51 x/fibre parameter table", whose description says it was "flagged on the forum" (thread title quoted) | forum post; GitHub PR/commit |
| "Work on projects aligned with Celestia's values" | Observer is independent of validators and deliberately refuses monitor-IP allowlisting ("Good external observability is a reason not to special-case the observer's traffic", t/2295); periodic public "Fibre health reports" on the forum | forum post (reports, once published) |
| "Quality of infrastructure" / "Monitoring and alerting" | Sentry architecture, HSM/KMS signing, 24/7 on-call ([FILL: provider, DC city/country, hardware, monitoring stack]) | application form text; optionally a public status page URL |
| "Run an active ... Mocha testnet validator for at least 2 months before application deadline" | `Huginn` on mocha-5: `celestiavaloper1d2ktc37cme7ydk30ylzhamutcynhdvyet7nt3x`, created 2 Sep 2026 15:31 UTC, BONDED, not jailed, commission 0.20 (max 0.25), security_contact security@huginn.tech, 100 % of last 100 blocks signed (Celenium mocha-5, 6 Sep 2026) | explorer URL (celenium.io mocha-5 validator page) |
| "Dedicated email address so that the Foundation can reach you" | security@huginn.tech (already set on-chain as `security_contact`) | on-chain description |
| "Have 25% or less commission" | 0.20 on mocha-5; max_rate 0.25 | on-chain |
| **"Run Fibre once it is live"** (mandatory) and "Fibre support (once Fibre is live)" | Run the reference `fibre` server for `Huginn` and register the host via `celestia-appd tx valaddr set-host <host:port>`; then our own observer verifies us like everyone else | see proof list below |

**What proves "Run Fibre" compliance** (from the v10 fibre server guide, [celestia-app/fibre/cmd/README.md](https://github.com/celestiaorg/celestia-app/blob/main/fibre/cmd/README.md), and v10.0.0-corto release notes, read 6 Sep 2026):
1. On-chain registration visible to anyone: `celestia-appd query valaddr provider <celestiavalcons-address>` returns our host; `celestia-appd query valaddr providers` lists all bonded validators' hosts. Our mocha-5 consensus address is `celestiavalcons1legae8ax6nd62h3f2n42qzxfutgs3ajcdrngcf` (hex E4401AEA8B1F8359FE58216D70D78A402689A2A4).
2. The fibre listen port (default 7980) reachable from outside — exactly what `fibre-sentinel` measures; publish our own row in the health report.
3. TLS certificate endorsed by our consensus key ("On startup the server generates its own certificate and has it endorsed once by your validator's consensus key") — verifiable with `fibre-tlsverify`.
4. Prerequisites we must meet: "The chain is on app version 10 or later", "The node's privval gRPC endpoint is enabled" (default moved to `127.0.0.1:26669`), KMS "must support the privval `SignRawBytes` message" with "median signing latency ... at or below 10ms", "Your validator is bonded".

Fibre status on 6 Sep 2026: **not live on any public network.** Mainnet (`celestia`) and `mocha-5` both report app version 9.0.6 via public LCDs; `x/valaddr` and `x/fibre` REST routes return `"Not Implemented"`; celestia-app v10.0.0-corto (2 Sep) and v10.0.1-corto (3 Sep) are pre-releases only ("The fibre and valaddr modules are enabled by default"; "Validators are recommended to run a fibre server and register their host on-chain"); status.celestia.org lists no scheduled v10 upgrade. So for the Cohort 9 application the criterion is a commitment, and the strongest evidence is (a) the devnet work already done and (b) being ready to register on mocha-5 within 24 h of v10 activation (the undelegation rule is "Failing to upgrade your node in a timely manner (24 hours or less)").

---

## 5. Risks and ambiguities

### 5.1 Hard disqualifiers to check now

| Criterion (verbatim) | Huginn status (6 Sep 2026) | Risk |
|---|---|---|
| "Run an active Mainnet Beta validator or an active Mocha testnet validator for at least 2 months before application deadline" | mocha-5 `Huginn` created **2 Sep 2026 15:31 UTC**. With a projected deadline of ~1 Nov 2026 the cutoff is ~1 Sep 2026. | **HIGH.** One to two days short if the deadline is 1 Nov. Mitigations: the mocha-4 `Huginn` (same valoper, created 5 Aug 2026) predates the mocha-5 hardspoon, but it shows `jailed: true` and 0 % recent uptime on the mocha-4 indexer, so it hurts more than it helps; do not cite it unless asked. Ask the Foundation/CMs in Discord (they invite this) whether mocha-5 genesis timing is taken into account; stay continuously bonded from now. |
| "The validator has to have significant stake to be able to propose blocks" | mocha-5 stake **1,000,000 utia = 1 TIA**, voting power 1 (active set 76 of 100). | **HIGH.** VP 1 will essentially never propose. Get testnet TIA delegated (faucet, community, CMs) before 1 Oct so proposals appear on the explorer. |
| "Not jailed more than once in the 6 months before application deadline" and form question "Have you been slashed or jailed in the last 6 months on Celestia or other chains you validated on." | mainnet `Huginn` (`celestiavaloper12lh3gp4mw7jzt3gymd7dukfvwra4h5jazke59f`, created 31 Oct 2023) is **currently jailed**, signing-info `jailed_until 2025-12-26T14:45:25Z`, `tombstoned: false`, stake 6.3 TIA. The jail event (~26 Dec 2025) is outside a 6-month window ending Nov 2026, so it is not a disqualifier by the letter. | **MEDIUM.** A visibly jailed, dust-stake mainnet validator under the same moniker/identity (`D27EE330254D4F6A`) is the first thing a reviewer will see on Celenium. Either unjail and keep it signing, or state plainly in the form that mainnet was decommissioned on [DATE] and Mocha is the active validator. Also disclose any jail on other chains in the last 6 months. |
| "Have 25% or less commission" | 0.20 (mocha-5, mainnet) | OK. Mainnet `max_rate` is 0.60; irrelevant unless they read it as intent — consider lowering if you reactivate mainnet. |
| "Not running your infrastructure in Hetzner or OVH" + "we also do not advise Contabo" + "European and North American locations are greatly discouraged" | [FILL: provider + location] | **MEDIUM/AMBIGUOUS.** Istanbul/Turkey sits on the Europe boundary; whether the Foundation counts it as "European" is not defined. If any node is in Frankfurt/Helsinki, move it before applying. A location outside EU/NA (or at minimum clearly Asia-side) removes the question. |
| "Not based within the US, within any country subject to economic sanctions, or within any other prohibited jurisdiction, and successfully complete a compliance screen" | Turkey is not a sanctioned jurisdiction. Compliance screen (KYC/KYB) is performed "Upon selection". | LOW, but the screen is mandatory and "prohibited jurisdiction" is undefined. Have entity documents ready. |
| "Not associated with an exchange or custodian" / "Not in the top 10 validators by delegation power" | Independent operator; nowhere near top 10. | None. |
| "Dedicated email address" | security@huginn.tech on-chain. | None; keep it monitored. |
| "Run Fibre once it is live" | Fibre not live; v10 pre-release only. | Commitment now; **execution risk later**: privval gRPC + `SignRawBytes` support in your KMS with ≤10 ms median latency (v10 release notes). Check your signer (tmkms/horcrux) supports it before v10 hits mocha-5. |
| Undelegation: "Failing to upgrade your node in a timely manner (24 hours or less)" | — | Operational: v10 rollout on mocha-5 is probably imminent (docs already say mocha-5 chain ID in v10 notes). Prepare. |

### 5.2 Ambiguities in the text

- "significant stake to be able to propose blocks" — no number given. On mocha-5 the set is 76/100, so bonding is trivial; proposing is what counts.
- "at least 2 months before application deadline" — the deadline itself is never announced; it is inferred from "exactly 1 month prior to the date of being delegated to", and the delegation date is not announced either.
- "less weight on contributions" (Cohort 5, June 2025) — tied to an Ecosystem Delegation pilot that ended Dec 2025 and whose handbook page is now 404. Whether contribution weight was restored is unknown. Do not build the application on contributions alone; the docs are explicit that low uptime outweighs contributions.
- "Which networks you validate on Mainnet + links to your validators" — this question exists to see multi-chain track record; a jailed Celestia mainnet validator is a poor first link.
- Selection is described as done by "the Celestia Foundation" (docs) but forum feedback (kingsuper, July 2025) says reviews were handled by "fellow validator teams", and every results post thanks "the whole P-OPS team". Assume operator peers read the application; write for them.
- The docs say "Each Cohort has to renew in 12 months, except the bottom 7 validators" — "bottom" is undefined (by stake? by uptime?). Irrelevant for entry, relevant after selection.

### 5.3 Attribution risk (specific to this team)

- The forum threads t/2288 and t/2295 are posted by user `utku`; the code is at `github.com/plsgiveup/fibre` (personal account, 0 stars, 1 commit on main as of 6 Sep 2026); the CIP fix commit 6b9c74f is authored by `rootulp` and credits no external reporter by name — PR #405 says only that it was "flagged on the forum". The validator's website links to `github.com/Huginntech` / `github.com/Huginn-Tech`. **None of these identities are linked to each other publicly.** The application asks for "Github page of your organization" and "Team experience and roster (including Twitter + Github links)". Fix before 1 Oct: transfer or fork the repo into the Huginn-Tech org (keep the history), add the Huginn name and forum handle to the README, put the valoper address in the README, and add "Huginn Tech" to the `utku` forum profile or reply in t/2288 from the Huginn account.
- The forum post t/2288 #3 says "I've been building an external verifier" (singular). Decide the [ATTRIBUTION SPLIT] wording (individual vs team) and use it consistently in the form's roster section.

---

## 6. Draft application bullets (plain English; each tied to a verifiable artefact)

Placeholders in [BRACKETS] are for the owners. Keep the personal statement under 1,500 characters; these bullets are raw material for both the statement and the "Contributions" field.

1. We run `Huginn` on mocha-5 (`celestiavaloper1d2ktc37cme7ydk30ylzhamutcynhdvyet7nt3x`), bonded since 2 Sep 2026, 20 % commission, security contact security@huginn.tech set on-chain. [FILL: uptime % since bonding, from Celenium, on the day of submission.]
2. Infrastructure: [PROVIDER], [CITY, COUNTRY] (not Hetzner/OVH/Contabo); signing key in [KMS/HSM], validator behind [N] sentries, [MONITORING STACK] with 24/7 on-call. [FILL: hardware spec.]
3. We are building [PRODUCT NAME] ([DOMAIN]), an independent public observer for Fibre (CIP-51): it watches `MsgPayForFibre`, recomputes each blob's shard assignment, probes only the assigned validators' registered endpoints from outside, verifies returned rows against the commitment, and repeats across the retention window. Source: [REPO URL], Apache-2.0.
4. The observer is split into reusable modules: `fibre-tlsverify` (checks the Fibre TLS certificate's endorsement against the validator's consensus key), `fibre-assign` (assignment recomputed off-chain and tested bit-for-bit against celestia-app across ~890 scenarios), `fibre-sentinel` (probe scheduler with a fault taxonomy), `fibre-devnet` (multi-validator devnet with fault injection). [REPO URL]/[module].
5. Measured on devnet: a full probe (dial + `DownloadShard` + row verification) costs p50 ~14 ms / p95 ~24 ms; actual pruning lands at `pruneAt + ~1m45s`, which any monitor must tolerate to avoid false faults. Published in forum thread 2288 (4 Sep 2026).
6. Our question on `ShardRetention` led the core team to correct the CIP-51 parameter table (celestiaorg/CIPs PR #405, commit 6b9c74f, 2 Sep 2026): "Thanks for flagging this, we fixed it in ..." (forum thread 2288, 3 Sep 2026).
7. We proposed a rate-limiting design for Fibre's read path that does not rely on IP allowlists, at the core team's invitation (forum thread 2295, 4 Sep 2026; reply from chamirachid1: "definitely, feel free to write something and we can review it").
8. Once Fibre is live we will run the reference fibre server for `Huginn`, register the host with `celestia-appd tx valaddr set-host`, and our own observer will verify our endpoint on the same terms as every other validator; the result will be visible in the public dashboard and in `celestia-appd query valaddr provider celestiavalcons1legae8ax6nd62h3f2n42qzxfutgs3ajcdrngcf`.
9. We will publish [CADENCE: weekly/fortnightly] "Fibre health reports" on the forum (reachability, TLS identity, shard-serving over the retention window, prune timing) from the day Fibre activates on mocha-5. [FILL: link to first report if available before submission.]
10. We commit to upgrade within 24 hours of any release, and have [FILL: KMS name] confirmed to support privval `SignRawBytes` for Fibre endorsements [FILL: or the date by which it will].
11. Team: [NAMES/HANDLES], [YEARS] operating validators on [NETWORKS], GitHub [github.com/Huginn-Tech or org URL], X [@handle]. Attribution for the Fibre work: [ATTRIBUTION SPLIT — e.g. "built by X (Huginn Tech) with Y"].
12. Mainnet: [CHOOSE ONE — "`Huginn` mainnet validator reactivated on [DATE], unjailed and signing" / "our 2023 mainnet validator was decommissioned on [DATE]; Mocha is our active Celestia validator until selected"]. No slashing or jailing on any chain in the last 6 months [confirm; otherwise disclose].

Avoid in the text: the word "novel", claims about 1 Tb/s, and any implication the Foundation asked for the tool. Quote the core team's forum reply instead.

---

## Appendix — verification data captured 6 Sep 2026

- mocha-5 LCDs (api-mocha.pops.one, celestia-testnet-api.itrocket.net, api-1/2.testnet.celestia.nodes.guru): 76 validators; `Huginn` `BOND_STATUS_BONDED`, `jailed:false`, `tokens:1000000`, commission `0.20/0.25/0.01`, commission `update_time 2026-09-02T15:31:01Z`; node app version 9.0.6.
- Celenium mocha-5 (api-mocha-5.celenium.io): validator id 17957054, `creation_time 2026-09-02T15:31:01Z`, uptime 1.0000 over the last 100 blocks, jails [], blocks proposed [].
- Celenium mocha-4 (api-mocha.celenium.io, still indexing mocha-4 at height 14,555,283): `Huginn` id 1211912260, created 2026-08-05, `jailed:true`, 0 of last 100 blocks signed.
- Mainnet LCDs (api.celestia.pops.one, celestia-mainnet-api.itrocket.net): app version 9.0.6; `Huginn` signing info `jailed_until 2025-12-26T14:45:25Z`, `tombstoned:false`. Celenium mainnet: id 35204, created 2023-10-31, stake 6,310,594 utia, `jailed:true`. A second validator "Celestine Sloth Society" lists `lazy@huginn.tech` and "Lazified by Huginn.Tech" with 0 stake.
- celestia-app releases (github.com/celestiaorg/celestia-app/releases): v10.0.1-corto (3 Sep, pre-release), v10.0.0-corto (2 Sep, pre-release), v9.0.6 (18 Aug, latest stable). status.celestia.org: "We're fully operational", no scheduled v10 maintenance.
- CIP-51 ([cips.celestia.org/cip-051.html](https://cips.celestia.org/cip-051.html)) parameter table now includes `fibre.ShardRetention | 4h | Minimum local duration validators keep an uploaded shard before pruning it ... Bounded to [10m, 168h]` and `fibre.FullStakeStorageBudget | 2 TiB`.
- Docs page `Cohort information` list and cohort table as quoted in §1.3; docs source commits as listed in §2.4 (GitHub pages, fetch summaries).
