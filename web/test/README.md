# Looking at the dashboard with realistic data

The devnet has four validators and two publications. Most of what makes a
dashboard good or bad only appears at scale: whether a moniker column holds a
thirty-four character name, whether the worst-first sort surfaces the right
rows, whether a legend stays readable when nine verdict classes are actually
present, whether the validator table still fits. These three tools exist so
those questions get answered by measurement rather than by eye.

Nothing here runs in production and nothing here is served to anyone. The
fixture is synthetic and local.

## fixture.py — a store with sixty validators in it

```bash
make build                                   # needs observer-collector
python3 web/test/fixture.py /tmp/fx/observer.db
fibre-sentinel/bin/observer-api -db /tmp/fx/observer.db -listen 127.0.0.1:8099 \
  -vantage eu1 -vantage-location "Helsinki, Finland" \
  -vantage-provider Hetzner -vantage-asn AS24940 -vantage-egress 203.0.113.10
```

It creates the schema by running `observer-collector -once`, so it can never
drift from the shipped migrations, then fills it with sixty validators, 260
publications and about 85,000 probes. The draw is seeded, so the same fixture
comes out every run and two screenshots are comparable.

Every row is placed relative to **the moment the script runs**, because the
API reads its 24h / 7d / 30d windows off the real clock: a fixture pinned to a
fixed date goes empty in every short window a day later. Set
`FIXTURE_NOW=2026-09-16T10:30:00Z` to pin it when two runs have to match byte
for byte. Every insert names its columns, so a migration that adds a nullable
column does not break the script; a new NOT NULL column or a new table still
needs a line here. The escrow side includes the withdrawal queue (schema 21):
paid withdrawals attributed to their payout, and a few still queued.

Two routes do not answer 200 against a fixture, by design: `/v1/health` is
503, because no observer process is running to keep a status file fresh, and
`/v1/exports/pubkey` is 404, because the fixture's exports are not signed.
Every page carries an "Observer degraded" line for the first reason.

The population is deliberately mostly healthy, because a fixture that is half
broken teaches you to design for a network that does not exist. Eleven of the
sixty are impaired, each in a different way, so every class the taxonomy can
produce is present and findable: a repeated fault, two occasional ones, two
outages that begin at a known hour, a validator with no registered host, one
whose certificate has lapsed (identity reason `cert_expired`, the verifier's
own code, so the API calls it expired and not a mismatch), one that never signed, one that prunes before
the deadline, one that is reachable and answers every download with a server
error, and one that rate-limits most downloads. Two more are jailed: their
endpoint rows are closed with the reason the collector records and their
heartbeats stop at that moment, which is how the table's "jailed" word and
the validator page's "left the bonded list" line get exercised.

The reachability heartbeat is generated at its real cadence — every five
minutes for seven days, so 2,016 samples per registered validator — because
that is the one stability signal whose coverage does not depend on the chain
proving an obligation, and a dozen rows an hour deep put every validator under
the twenty-observation floor so the uptime column was never exercised.

Attestation follows the **two-thirds quorum**, not a flag. The publisher stops
collecting signatures once it holds two thirds of voting power, so on any given
blob about a third of the set has no signature on chain and is neither down nor
at fault. Arrival order is drawn per publication and is independent of stake, so
quorum membership is a race rather than a list. A fixture where everyone signs
hides the single class most likely to be misread on the dashboard; this one
produces it at the rate the real network will.

Probe durations are generated rather than constant: they scale with the
validator's assigned rows, sit on a per-validator floor, and have a long right
tail, so the median and the 95th percentile are different numbers and the
throughput column has something to show. One validator (`slow`) serves
everything at a ninth of everyone else's speed, and one large validator carries
enough rows that its raw duration is the worst on the network while its
throughput is the best — which is the case the column exists to get right.

Three modelling rules it follows, all taken from the code rather than invented:

- **Attestation is decided at upload time, serving at probe time.** A validator
  already dark when the publisher uploaded never received the shard, so it never
  signed: that is `UNATTESTED`. One that signed and then went dark inside the
  retention window is `UNREACHABLE`. Conflating them makes every blob
  permanently "degraded" and the fully-served figure a constant zero.
- **A validator with no registered host is absent from the signature set**, for
  the same reason, but still classifies as `NOT_REGISTERED`, because
  `classify.go` judges `OutcomeNoHost` before the attestation check.
- **The wire is recorded as it happened, and attestation decides the class on
  top of it.** `classify.go` judges identity, then no-host, then attestation,
  then the outcome, and the fixture's `classify()` is a branch-for-branch port
  of that order. An unattested validator with a lapsed certificate is
  `IDENTITY_EXPIRED`, not `UNATTESTED`; an unattested validator that was
  refusing connections still has `tcp_ok = 0` on its row. Writing a clean
  handshake for every unattested probe, which is what this did before, made the
  heartbeat and the probe history disagree about the same endpoint at the same
  minute.

Rows are inserted in start-time order, heartbeats and probes alike. Several of
the API's "latest row per validator" queries use `MAX(rowid)` rather than a
correlated `MAX(started_at)`, because the real collector ingests in write order
and write order is chronological. A fixture that inserts newest-first quietly
hands those queries the *oldest* row, which is how a validator that had been
down for thirty hours came out of the API as `reachable: yes`.

## serve.cjs — the static export with the API behind it

```bash
npm --prefix web run build
API_PORT=8099 PORT=3111 node web/test/serve.cjs
```

Serves `web/out` and proxies `/api/*`, which is what the Caddyfile does in
production, so the page under test is the page that ships.

## audit.cjs — the numbers behind "professional, plain, elegant"

```bash
NODE_PATH=web/node_modules PAGES='[["/","overview"],["/blobs","blobs"]]' \
  node web/test/audit.cjs
```

Reports, per page and viewport: document height, horizontal overflow, the widest
table, how many distinct font sizes, weights, families, radii and text colours
are actually in play, and every text-on-background pair that fails WCAG AA
against the background it is really painted on, worst first.

The point of the counts is that a design system has few values and an accretion
has many, so the number is a fact rather than an opinion. The point of the
contrast check is that it walks up the DOM for the first opaque background, so
it catches what a token audit misses: a palette can be perfectly consistent and
still paint twelve-pixel text at 2.2:1.

Requires Playwright: `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm --prefix web install --no-save playwright`.
