# R10 — An explorer-shaped Fibre dashboard

Date of research: 16 September 2026. This note does **not** repeat R9. R9's
palette, typography, badge mechanics, Celenium identifier conventions,
accessibility rules and empty-state rules still stand and should be read
first. This note records what changed after the September audit, what R9's
spec can no longer absorb, and the information architecture that follows from
one instruction: the dashboard should read like an explorer, elegant and
plain, weighted toward Fibre's own metrics, and specific to Celestia.

Every claim below was fetched on 16 September 2026 unless marked otherwise.
Where a page could not be read, that is stated rather than guessed.

---

## 1. Why R9's spec no longer fits

Four things moved under it.

**The taxonomy went from six classes to eleven.** R9 section 6.3 specifies
badges for `HEALTHY / TOLERATED / FAULT / EXPECTED_GONE /
UNREACHABLE_POST_WINDOW / NOT_PROBED`. The audit added `UNATTESTED`,
`UNREACHABLE`, `NOT_REGISTERED`, `SHADOWED_SHARD` and `IDENTITY_EXPIRED`,
each because the old taxonomy was making a claim the evidence did not
support. Eleven badges is past the point where a legend is readable.

**R9's rate formula is now wrong.** It specifies
`HEALTHY / (HEALTHY + TOLERATED + FAULT)`. The rate is now
`HEALTHY / (HEALTHY + FAULT)` over in-window probes only, because a grace
probe can only ever add `HEALTHY` and counting it rewarded over-retention
instead of measuring retention.

**The numbers multiplied.** Verdict coverage, held-out counts per class, a
per-obligation rate, a per-schedule-point breakdown, attestation coverage,
vantage health, and reconstructability split into fully-served and
recoverable. R9's section 5 page contents have nowhere to put these.

**The table broke.** R9 section 6.4 specifies nine columns. The shipped table
has thirteen and overflows a 1440px viewport, with voting power and the last
probe time behind a horizontal scroll. Observed directly on 16 September.

---

## 2. What the field actually does, and the gap it leaves

Four surveys were run: Celestia's own ecosystem, blob and DA dashboards
elsewhere, independent measurement products, and visual grammar.

### 2.1 Nobody else asks our question

Of every blob and DA dashboard found, **none measures whether data is still
retrievable**. Blobscan, blobs.money, blobsy.xyz, blobspace.fun and the Dune
boards are supply-and-price dashboards: blob counts, gas price, saturation,
fee spend, rollup market share. Ethereum's 18-day pruning window is treated
as a fact of the protocol rather than something to verify. Blobscan's nearest
thing is a "Storages" column naming a mirror, with no timestamp saying when
that mirror was last checked.

L2BEAT prints **"Duration of storage: 14 days"** for EigenDA and **"Storage
Duration 7.04 days"** for Celestia with nothing beside either checking
whether the promise held. That is the clearest statement of the gap this
product fills: the ecosystem publishes the promise and nobody measures it.

EigenDA's own operator tooling is a Grafana board bundled in
`Layr-Labs/eigenda-operator-setup` showing batches processed by your own
node. Self-reported telemetry about yourself is the opposite of an
independent observer.

### 2.2 Two products publish a denominator; none publishes an interval

Across every measurement product surveyed, exactly two show the sample beside
the rate:

- **Spark** (Filecoin retrieval checking) publishes daily retrieval request
  counts next to the retrieval success rate, and allowed its headline to read
  12.8 percent rather than tuning it to look good. Its mistake is instructive:
  it excludes miners with zero successful retrievals from the aggregate,
  which is exactly the wrong exclusion for an accountability tool, since a
  provider serving nothing is the case you most want counted.
- **ar.io** stores `passedEpochCount`, `failedEpochCount`, `totalEpochCount`,
  `observedEpochCount` and `prescribedEpochCount` per gateway on chain.
  `prescribedEpochCount` records how often the gateway was actually selected
  for checking, which is a coverage disclosure. It also **rejects a report if
  more than 80 percent of gateways fail**, on the grounds that this indicates
  an observer-side network problem. That is the same reasoning as our
  correlated-failure guard, arrived at independently.

**Not one dashboard in the survey displays a confidence interval on a
measured rate, and not one says "we checked N of M".** Our verdict coverage
figure and per-obligation rate have no precedent to copy. They are either an
advantage or an over-engineering, and the design has to decide which by how
prominently it places them.

Avail is the only project treating confidence as a first-class quantity, and
it does the inverse of what we do: `calculate_confidence(count) = 100 * (1 -
1/2^count)` in `avail-light/core/src/utils.rs`, with the operator choosing a
target (default 99.9) and the tool deciding how many cells to sample. That is
sampling power, not a hit rate, so it answers a different question.

### 2.3 ProbeLab is the model for saying it honestly

ProbeLab publishes measured judgements about named infrastructure, including
Celestia's own bootstrappers, without overclaiming. Four techniques:

1. **The verdict has a stated cost of entry.** "Upon three failed attempts to
   connect to a bootstrap node with any of the transport options ... the node
   is considered offline", across four transports, every five minutes. A
   partial failure never turns the tile red; it becomes a red stripe in the
   timeseries. The headline stays generous, the detail stays complete.
2. **A third category that is neither pass nor fail.** `Unknown` for nodes
   whose agent version could not be retrieved, with the reason given. Our
   `UNREACHABLE` and `UNATTESTED` are the same move.
3. **The vantage is part of the number, not a footnote.** Seven named AWS
   regions for Tiros, four named DHT locations, sample size published as the
   arithmetic that produces it ("2 * 5 * 2 * 14 = 280 requests ... 280 * 7 =
   1,960 data points every six hours"), and a region picker on the chart so a
   regional result cannot be read as a global one.
4. **Measurement and accusation are separated by policy.** "we do not provide
   commentary on the results presented on the website itself", with
   discussion pushed to a forum where the operator can answer. A right of
   reply built by omission.

Two techniques worth taking from elsewhere:

- **cosmos.directory** publishes the raw error string and its timestamp beside
  the last success timestamp, with a separate `rateLimited` flag so "the
  endpoint throttled our prober" never reads as "the operator is down". Live
  example from `status.cosmos.directory/celestia`: QUAD carrying
  `"lastError":"Request failed with status code 429 (Too Many Requests)"`,
  `rateLimited: true`, yet still `available: true`.
- **Rated** excludes groups operating five or fewer validators from percentile
  ranking, "to remove the noise from relatively small operations". A minimum
  sample rule for ranking, which is what our twenty-observation floor already
  does.

**SSL Labs** is the only product in the survey that gives the subject an
opt-out: results are publicly cached by default with a "Do not show the
results on the boards" checkbox. It also dropped its 0-100 score for a letter
grade because the number was falsely precise. Both are relevant to us.

### 2.4 Storj is the cautionary case

Storj's online score does **not** distinguish "could not reach" from
"failed": an unanswered audit is an offline audit and counts against the
operator. The saving grace is that the verdict is delivered privately to the
operator's own dashboard rather than published against a named party. We
publish, so we cannot borrow that conflation.

---

## 3. What Celestia users already recognise

From the Celenium survey, fetched from server-rendered HTML rather than
inferred:

| Convention | What Celenium does |
|---|---|
| Theme | Dark by default, `--app-background:#17191b`, cards `#202225`. Three themes ship; light is `#ebebeb` |
| Accent | Mint `#18d2a5`, **not** Celestia's own purple `#7C68F2` |
| Validator state | Mint active, blue inactive, **orange** jailed. Jailed is not red, because it is a state and not an alarm |
| Truncation | `7265 ••• 7631` with a three-bullet ellipsis, not `0x72…31` |
| Type | Inter for UI, **JetBrains Mono for every hash and address**, `.tabular` for figures |
| Units | Binary throughout: MiB, GiB, TiB. Never MB |
| Time | Relative in lists, "3 sec. ago" |
| Validator sort | Voting power descending, **no logos or avatars anywhere** |
| Aggregates | Every number carries a share-of-total percentage beneath it |
| Content width | `--base-width: 992px`. A laptop column, not a 1440 stretch |
| Table density | `tbody tr { height: 28px }`, cells `padding:0`, `white-space:nowrap` |
| Many columns | Horizontal scroll in a `_table_scroller`, unapologetically. Nobody hides columns automatically |
| Structure | Card row on top, dense unstriped table below |

There is **no first-party Celestia dashboard**. `docs.celestia.org` links out
to Celenium, celestiadata.com, L2BEAT, probelab.io/celestia and three Google
Looker Studio boards. `celestia.observer` does not resolve;
`status.celestia.observer` returns Cloudflare Error 1000. Celenium's
`/blobstream` route now 404s. The space for a Fibre dashboard is genuinely
empty.

Note the divergence to decide deliberately: **Celenium is mint on charcoal;
celestia.org is purple `#7C68F2` on near-black `#040207`.** Following the
explorer means mint; following the brand means purple. They cannot both be
the accent.

---

## 4. The design direction

### 4.1 One sentence

An explorer for one question: **is each validator still serving the Fibre
shards it signed for?** Everything on the first screen answers that; every
qualification the answer needs is one click away, not in the reader's face.

### 4.2 What goes above the fold

Ordered by what a reader wants, not by what we were clever about:

1. **Network serve rate**, one number, with its sample beside it at smaller
   size and dimmer contrast, never as a second tile. Grafana's fixed 2.5:1
   ratio between a value and its qualifier is the mechanic.
2. **Reconstructable now**, the Fibre-specific number nobody else has: how
   many recently settled blobs could still be rebuilt from what came back.
3. **Validators serving**, as a proportional bar in Celenium's grammar:
   serving, unreachable, unproven, with a 10x10px swatch and the raw integer
   beside each. The bar is the rate, the integers are the sample, one object.
4. **Bytes under obligation right now**, the Fibre equivalent of Celenium's
   distinctive "Bytes In Blocks" counter.

Verdict coverage, attestation coverage, per-point breakdown, vantage health
and the held-out table move **off** the overview and onto a single
`/methodology#what-this-excludes` section plus the validator detail page. One
line of summary stays on the overview, linking down.

### 4.3 The validator table

Eight columns, pinned first column, horizontal scroll for the rest,
everything past the eighth on the detail page. Celenium ships eight and
scrolls; that is the precedent.

| # | Column | Notes |
|---|---|---|
| 1 | Validator | **moniker**, with `celestiavalcons ••• xxxx` beneath. Pinned |
| 2 | Voting power | descending default, share percentage beneath, like Celenium |
| 3 | Serve rate | value plus `(26 / 26)` smaller and dimmer; counts only below the floor |
| 4 | Obligation | proven / unproven / no host |
| 5 | Reachable | yes / no / not probed |
| 6 | Fault | count |
| 7 | Unreachable | count, in the tolerated colour, never the fault colour |
| 8 | Last probe | relative, absolute on hover |

Default sort stays worst-first, with the floor already implemented: a
validator below twenty rated observations is not ranked among the worst.

### 4.4 The single biggest change is not a number

**Show monikers.** The dashboard currently renders `b997869dd0cc…`. Every
explorer a Celestia operator uses renders `P-OPS Team`. This is the one
change that makes the product feel like an explorer rather than a research
tool, and it is now implemented by querying the chain's own staking module
rather than an explorer's API, so it works on any network including a devnet
and does not inherit anyone's rate limits, attribution terms or coverage
gaps.

Celenium renders **no avatars at all** in its validator rows, so text-only
monikers are the familiar choice, not a shortcut. The Keybase key is stored
for later, not used now.

### 4.5 Colour

Follow Celenium, not celestia.org: mint accent on charcoal, dark by default.
Three reasons. Operators spend their time in Celenium and will read a mint
accent as native. Celestia's purple as a UI accent would put the brand colour
in the good/bad channel, which Blobscan deliberately avoids by keeping its
purple accent separate from its success/warning/error ramps. And a jailed or
unreachable validator should be **orange**, not red, following Celenium's
`--validator-jailed:#f8774a`, because those are states rather than alarms.

Red stays reserved for `FAULT`, the one class that is an accusation.

### 4.6 Techniques to copy, with their source

- Sample size as a smaller dimmer sibling inside the same unit, never a
  separate tile. Grafana, Celenium.
- Never let colour carry meaning alone; write the word beside the swatch.
  GitHub Status writes "Partial Outage" next to every colour.
- Cap the type scale. Stripe's shipped API reference tops out at 15px;
  Grafana caps stat titles at 30px and never goes above weight 500.
- Cap content width near a laptop column. Celenium 992px, Stripe 1000px.
- Build neutrals as an opacity ladder on one ink rather than a grey palette,
  so dark mode inverts for free. Celenium's `--txt-primary` .9 down to
  `--txt-support` .2.
- Publish the raw error and its timestamp beside the last success.
  cosmos.directory.
- Fill empty states rather than leaving gaps. GitHub Status renders quiet
  days as neutral bars reading "No incidents reported".
- Skeleton-load at the final size so nothing reflows. Celenium's placeholder
  pill is exactly the width and height of the number that replaces it.

### 4.7 Two things to decide, not assume

**Do the epistemic numbers belong on a public dashboard at all?** No product
in the survey publishes a confidence interval on a measured rate. That is
either our advantage or our over-engineering. The recommendation is to keep
them in the API and on the detail pages, where a reviewer or an accused
operator will look, and to keep the overview to one line about them. The FDP
reviewer and the accused operator are the two readers who need them; the
casual reader is not.

**Do we offer an opt-out?** SSL Labs is the only precedent, and it works:
publicly cached by default with a checkbox to stay off the boards. We
currently have no register, and the About page says so. If we add one, SSL
Labs' shape is the one to copy, including its honest note that hiding does
not make the configuration better.

---

## 5. Sources

All fetched 16 September 2026 unless noted.

Celestia: celenium.io and its `/_nuxt/entry.Cu2tIufR.css`, celenium.io/validators,
celenium.io/networks, celenium.io/faucet, mocha.celenium.io, arabica.celenium.io,
status.celestia.org, docs.celestia.org/learn/celestia-101/resources,
docs.celestia.org/operate/data-availability/metrics, docs.celestia.org/learn/blobstream,
celestiadata.com, l2beat.com/data-availability/projects/celestia/no-bridge,
grafana.com/grafana/dashboards/21116-celestia-consensus-validator-node.
Not reachable: celestia.observer (DNS), status.celestia.observer (Cloudflare 1000),
celenium.io/blobstream (404), mintscan.io/celestia/validators (client-rendered),
ping.pub/celestia (404).

Blob and DA: blobscan.com, api.blobscan.com, Blobscan frontend and Tailwind
sources on GitHub, docs.blobscan.com/docs/features, eigencloud.xyz/da,
l2beat.com DA summary and EigenDA pages, viewblock.io/arweave, docs.ar.io,
availproject/avail-light `core/src/utils.rs`, CheckerNetwork/spark-stats,
ethereumdashboards.com. Not reachable: ethseer.io, dora instances,
beaconcha.in (403), blobs.eigenda.xyz (403), filspark dashboards (proxy).

Measurement: probelab.io and its /ipfs/dht, /celestia/bootstrappers, /tools/tiros,
/tools/nebula pages, blog.ipfs.tech/2023-ipfs-observatory,
docs.rated.network methodologies, storj.dev/node/faq/how-the-online-score-is-calculated,
forum.sia.tech/t/hostscore-a-host-benchmarking-tool/415,
status.cosmos.directory/celestia, ssllabs research wiki SSL Server Rating Guide,
developers.cloudflare.com/radar, filecoin.io/blog/posts/reputation-systems-in-filecoin.
Not reachable: filrep.io and hostscore.info (client-rendered shells).

Visual grammar: eth.blockscout.com, www.githubstatus.com, docs.stripe.com/api,
grafana/grafana typography and BigValue sources, Datadog query-value widget docs,
www.cloudflarestatus.com. Not reachable: status.stripe.com (JS shell),
rated.network (Vercel checkpoint), linear.app/docs.
