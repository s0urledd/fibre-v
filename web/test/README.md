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
publications and about 84,000 probes. The draw is seeded, so the same fixture
comes out every run and two screenshots are comparable.

The population is deliberately mostly healthy, because a fixture that is half
broken teaches you to design for a network that does not exist. Nine of the
sixty are impaired, each in a different way, so every class the taxonomy can
produce is present and findable: a repeated fault, two occasional ones, two
outages that begin at a known hour, a validator with no registered host, one
whose certificate has lapsed, one that never signed, and one that prunes before
the deadline.

Two modelling rules it follows, both taken from the code rather than invented:

- **Attestation is decided at upload time, serving at probe time.** A validator
  already dark when the publisher uploaded never received the shard, so it never
  signed: that is `UNATTESTED`. One that signed and then went dark inside the
  retention window is `UNREACHABLE`. Conflating them makes every blob
  permanently "degraded" and the fully-served figure a constant zero.
- **A validator with no registered host is absent from the signature set**, for
  the same reason, but still classifies as `NOT_REGISTERED`, because
  `classify.go` judges `OutcomeNoHost` before the attestation check.

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
