# Deploying the observer

One VM runs everything. Five processes, one SQLite file, Caddy in front.

```
sentinel-scan ──► publications.jsonl, payments.jsonl, state.json ─┐
sentinel-probe ─► measurements.jsonl ─────────────────────────────┼─► observer-collector ─► observer.db ─► observer-api ─► Caddy ─► web/out
observer-heartbeat ─► reachability.jsonl ─────────────────────────┘                         (also polls x/valaddr and escrow balances)
```

The JSONL files are the append-only raw record; the SQLite database is
derived from them and can be rebuilt by deleting it and restarting the
collector. The collector's own `registry.jsonl` carries the endpoint
history (which validator registered which host, when), the one derived
table that has no other source; the prober reads it too, so a validator
that left the bonded list keeps being probed at its last registered host
across prober restarts. Back up the JSONL files and the database
(see "Backups"). Every process keeps a status file under `status/`, and
`/v1/health` turns them into one answer (see "Health and alerting").

One host can run one instance per network: the systemd units are
templates that take the network name as their instance (`fibre-scan@mocha`,
`fibre-api@mainnet`), each with its own env file, data directory and API
port, behind one Caddy with a site per network (see "Two networks").

## 1. Prerequisites

- A Linux VM with Go 1.23+ (the celestia-app pin needs 1.26.5; `GOTOOLCHAIN=auto` downloads it), Node 22, Caddy 2.
- A CometBFT RPC endpoint for the chain you observe. Use your own full node
  (default pruning is fine; no archive node is needed, see
  `docs/research/R6-hosting-and-cost.md`) with
  `storage.discard_abci_responses = false` in `config.toml`: the scanner
  reads `block_results` for every block, and a node that discards ABCI
  responses answers "node is not persisting finalize block responses"
  (rpc-mocha.pops.one did on 8 September 2026). Public RPCs on
  celestia-core v0.41.0 also cap heavy requests at 20 in flight.
- Before the chain runs app version 10, `x/fibre` and `x/valaddr` do not
  exist. The scanner logs "x/fibre is not active on this chain yet" and
  keeps following blocks, retrying the params query every 100 heights;
  the collector and heartbeat log the missing registry and carry on. This
  is the expected state on mocha-5 until the v10 upgrade.
- Outbound TCP to validators' Fibre ports (default 7980). Nothing more is
  needed to observe them: `DownloadShard` performs no caller authorization
  (celestia-app `fibre/server_download.go`), and the Fibre server's TLS config
  sets no `ClientAuth`, so it never asks for a client certificate
  (`fibre/server.go`). This observer is an ordinary client and can watch every
  validator that registers a Fibre endpoint, not only its operator's own.
- If the same host also runs your own validator and Fibre server: celestia-app
  main (#7848, 15 Sep 2026) recommends separate disks for Fibre shards and
  the node's data; the observer's data directory should not share the Fibre
  shard disk either.

## 1a. Describe the vantage before you publish anything

Every reachability verdict on the site is a statement about a network path,
and half that path is yours. A reader cannot judge an `UNREACHABLE` without
knowing where it was measured from, and a validator operator cannot check
your traffic against their own logs without knowing which addresses to look
for. So the env file has four settings that a public vantage must fill in,
and the API logs a warning at startup if it does not.

```bash
curl -4 https://ifconfig.co    # every address probes can leave from
curl -6 https://ifconfig.co
whois -h whois.radb.net "$(curl -4 -s https://ifconfig.co)" | grep -i origin
```

`VANTAGE_EGRESS` is the anchor and the only one that has to be exactly right:
an operator who sees connections from those addresses on their Fibre port can
match them against `/v1/meta`, and one who sees connections from anywhere
else knows they are not you. Include every address the host can leave from,
including IPv6, or a validator will see traffic it cannot attribute.

`VANTAGE_ASN` is the one that earns its place. It names the network your
traffic is routed through, not a place: one provider can hold several
autonomous systems and one autonomous system can span countries. It matters
because the most likely cause of a systematic false `UNREACHABLE` is a
peering or rate-limiting problem between your network and a validator's. With
the ASN published, an operator can look at their own peering and recognise
it; without it, the same evidence reads as the validator's fault. It is also
the field a third party can confirm from your egress addresses through public
routing data, which is what makes the rest of the block more than a claim.

`VANTAGE_PROVIDER` usually follows from the ASN. `VANTAGE_LOCATION` is the
only one nothing proves, because geolocating an address is a guess, and
`/v1/meta` says so per field rather than presenting all four as equal.

## 2. Build

```bash
git clone https://github.com/plsgiveup/fibre && cd fibre
make build            # fibre-sentinel/bin/* and web/out/
```

## 3. Configure

Everything is per network. The examples below set up `mocha`; repeat with
`mainnet` (and its own RPC, data directory and API port) for the second.

```bash
sudo install -d -m 0755 /etc/fibre-observer
sudo cp deploy/observer.env.example /etc/fibre-observer/mocha.env
sudo cp fibre-sentinel/observer/policy/policy.mocha.yaml /etc/fibre-observer/policy-mocha.yaml   # policy.example.yaml for mainnet
sudo cp deploy/publishers.yaml.example /etc/fibre-observer/publishers-mocha.yaml                  # publisher labels; optional, the API runs without it
```

Edit `mocha.env`: set `NETWORK`, `RPC`, `VANTAGE`, `DATA_DIR`
(`/var/lib/fibre-observer/mocha`), `POLICY`
(`/etc/fibre-observer/policy-mocha.yaml`), `API_LISTEN` (a port of its
own), and, when you have them, `ALERT_WEBHOOK` and `BACKUP_REMOTE`. Point
`sampling.master_secret_file` in the policy at
`<DATA_DIR>/sampling-master.key`; the prober creates it with mode 0600 on
first run. Keep it there. The file must live under the data directory,
because the units mount `/etc/fibre-observer` read-only and the service
user cannot write there.

That file is what makes the sample auditable. The commitments published at
`/v1/sampling` are SHA-256 of a per-day secret derived from it, so if the
master is regenerated on every restart the commitments change with it and
nobody can ever check a day's draw against them. The prober publishes each
day's secret seven days after the day ends (`-reveal-after`), to
`<DATA_DIR>/sampling-secrets.jsonl`; the collector serves it beside the
day's commitment. Only the day secrets are ever revealed, never the master. It is also the reason the
sample is unpredictable: a publisher who learned the master in advance could
work out which of its blobs would be probed, so do not put it anywhere the
publishers can read, and do not include it in a backup that leaves the host.

`host_at_settlement` on every assignment comes from the chain's
`set_fibre_provider_info` events, read in the same `block_results` pass
as the promises, seeded once from the bonded registry when the scan
starts (`host_history.jsonl`). No state query at past heights is made, so
the node's state pruning does not matter to the observer; what must be
available is `block` and `block_results` over the scanner's lag behind
the tip, which the scan needs anyway (a block the node cannot serve is a
recorded gap, and a registration inside a gap makes the hosts of later
settlements unknown until the gap is re-scanned).

Leave `-probe-unassigned` off on a public vantage. The read-path rate
limiting Celestia is designing (forum topic 2295) treats requests for
shards a validator was never assigned as illegitimate; probing only real,
in-window, correctly assigned commitments is what keeps the observer's
traffic on the right side of it.

## 4. systemd

```bash
sudo useradd --system --home /var/lib/fibre-observer --create-home fibre-observer
sudo install -d -o fibre-observer -m 0750 /var/lib/fibre-observer/mocha
sudo install -m 0755 fibre-sentinel/bin/* /usr/local/bin/
sudo install -m 0755 deploy/healthwatch.sh /usr/local/bin/fibre-healthwatch
sudo install -m 0755 deploy/backup.sh /usr/local/bin/fibre-backup
sudo cp deploy/systemd/*.service deploy/systemd/*.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fibre-scan@mocha fibre-probe@mocha fibre-heartbeat@mocha fibre-collector@mocha fibre-api@mocha
sudo systemctl enable --now fibre-healthwatch@mocha.timer fibre-backup@mocha.timer
```

The units are templates: the part after `@` is the network, and it selects
`/etc/fibre-observer/<network>.env`, `/var/lib/fibre-observer/<network>`
and `publishers-<network>.yaml`. They are hardened (`ProtectSystem=strict`,
`ProtectHome`, `PrivateTmp`, an empty `CapabilityBoundingSet`,
`SystemCallFilter=@system-service`) and can write only their own data
directory. The sampling secret must therefore sit under the data directory,
not under `/etc`.

Each unit runs one binary with the flags from its env file. Order does not
matter: the collector tolerates missing files, the API waits for the
collector to create the database, and before Fibre is active on the chain
the prober and heartbeat have nothing to do and say so in their logs.

None of the four processes exits on an RPC outage. The scanner retries a
transient failure for as long as it takes, with a warning every five minutes,
and picks up where it was; the prober waits for the chain at startup instead
of failing; the collector and heartbeat log the failure and try again next
round. A height the node cannot serve at all (pruned, or ABCI responses
discarded) is retried for ten minutes and then recorded as a gap in
`state.json`, shown on the dashboard and reported by `/v1/health`, and the
scan moves on. A chain halt is warned about every five minutes and waited
out; `Restart=always` in the units is for crashes, not for outages.

`sudo systemctl status 'fibre-*@mocha'` and `journalctl -u fibre-probe@mocha -f`.

### Health and alerting

Every process rewrites `<DATA_DIR>/status/<component>.json` on each unit of
work and at least every fifteen seconds: whether the last cycle succeeded,
the last error and when, its progress height, and the free space of the
data disk. `/v1/health` reads those files straight from disk and answers
200 when every one of scanner, prober, heartbeat and collector is alive and
succeeding, the scanner is within 200 blocks of the chain, the disk has
over 5% free, no scan gap is recorded and the chain has not upgraded past
this build's pin, and 503 with the failing checks otherwise. `/v1/meta`
carries the same components and verdict, and the header chip on the site
reflects it: green, amber with the failing processes in its tooltip, red
when nothing is alive.

`fibre-healthwatch@<network>.timer` asks `/v1/health` every five minutes as
the service user and posts to `ALERT_WEBHOOK` when the verdict changes,
again every `ALERT_REPEAT_MIN` while it stays bad, and once on recovery. The
webhook is any URL that accepts a JSON body with a `content` field (Discord,
Slack incoming webhooks, a Matrix or Telegram bridge). With no webhook it
only logs; an external uptime monitor pointed at
`https://<site>/api/v1/health` is the same signal with somebody else's
timer.

### Upgrading a running observer

The collector owns the schema and the API opens the database read-only, so an
upgrade has an order: install the new binaries, restart **fibre-collector**
first, then the rest. The API refuses to start against a database older than
the binary expects and says so, which is the intended failure — it will not
serve numbers from a schema it does not understand.

```bash
sudo install -m 0755 fibre-sentinel/bin/* /usr/local/bin/
sudo systemctl restart fibre-collector@mocha       # applies migrations
sudo systemctl restart fibre-scan@mocha fibre-probe@mocha fibre-heartbeat@mocha fibre-api@mocha
```

The API also refuses a database **newer** than itself, so an API left on an
old build after the collector moved on says so rather than serving columns
it does not know.

Schema 5 adds a covering index over `probes`. On a store with 700,000 probes it
takes a few seconds and about 200 bytes a probe; the collector logs it and the
restart is not otherwise different.

The API computes the network summary, the validator list and the market
figures for every window at startup rather than on demand, and keeps the
last computed copy of each under `<DATA_DIR>/snapshots/`. A restarted API
serves those copies at once, with their real age shown on the page, while
the warm-up recomputes them behind; the first minute or two after a restart
is busier than the steady state, but nobody waits for it.

## 5. Caddy

Install the site: `sudo mkdir -p /var/www/fibre-observer && sudo cp -r web/out/. /var/www/fibre-observer/`.
Then `deploy/Caddyfile` with your domains in place of `observer.example.org`
and `mocha.observer.example.org`:

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile && sudo systemctl reload caddy
```

Caddy serves the same static export on every site, proxies each site's
`/api/*` to that network's `observer-api` port, and gets TLS certificates
from Let's Encrypt. Delete the second site block if you run one network.

Build the site once with the sibling networks listed, and the header shows
a switch between them (the current one is marked by origin):

```bash
cd web && NEXT_PUBLIC_NETWORKS="mainnet=https://observer.example.org,mocha=https://mocha.observer.example.org" npm run build
```

### Two networks

Mocha and mainnet are two instances of everything, side by side:

| | mocha | mainnet |
|---|---|---|
| env | `/etc/fibre-observer/mocha.env` | `/etc/fibre-observer/mainnet.env` |
| data | `/var/lib/fibre-observer/mocha` | `/var/lib/fibre-observer/mainnet` |
| policy | `policy-mocha.yaml` (from `policy.mocha.yaml`) | `policy-mainnet.yaml` (from `policy.example.yaml`) |
| API | `127.0.0.1:8081` | `127.0.0.1:8080` |
| units | `fibre-*@mocha`, `fibre-*@mocha.timer` | `fibre-*@mainnet`, `fibre-*@mainnet.timer` |
| site | `mocha.observer.example.org` | `observer.example.org` |

Each instance needs its own RPC node with `discard_abci_responses = false`.
Nothing is shared between them but the binaries and the static export; a
data directory belongs to one chain and the scanner refuses to resume it
against another. Disk: a mocha instance grows by a few GB a month, a
mainnet instance by what its publication rate makes it (see "Backups"). Two
instances double the probe bandwidth budget in
`docs/research/R6-hosting-and-cost.md`.

## 6. docker compose (alternative)

```bash
cp deploy/observer.env.example deploy/.env   # edit RPC, VANTAGE, DOMAIN
docker compose -f deploy/docker-compose.yml up -d --build
```

Same five processes plus Caddy, one image built from `deploy/Dockerfile`.
Data lives in the `observer-data` volume. Compose is one network per
project (it binds 80 and 443); for two networks on one host use systemd.

## 7. Backups, retention, rebuild

Budget for disk: one measurement is about 1.5 KB in `measurements.jsonl`
and about twice that again in the database. At the R4 stress scenario
(60 publications an hour, 100 validators, 6 points) that is about 1.3 GB a
day of JSONL plus the database; at a realistic mocha rate it is a few GB a
month. The JSONL files are the record and are never rotated by the tools;
`/v1/health` fails the `disk` check under 5% free so the alert arrives
before a write does. When a disk fills, move the oldest JSONL files off the
box (they are append-only; a copy is complete the moment it is taken) and
rebuild the database from the rest if you want it smaller. Nothing here
deletes a probe row yet: the "all" window is exactly that.

**Retention (decided 2026-09-18, implemented in the collector):** raw
probe and heartbeat rows are kept for **90 days** (`-retain-raw`);
`raw_json` (the bulk of a row) is dropped after **30 days**
(`-retain-raw-json`) while every typed column, including the evidence
columns from schema 9, stays; **14 days** after a UTC day ends
(`-rollup-after`) the collector computes the day's per-validator rollup
(obligation buckets by settlement day; classes, faults, gaps and
heartbeats by start day) with the API's own SQL, and only a rolled day is
ever pruned, whole days at a time. `-rollup-after` is a floor, not the
rule: a day rolls only once every promise settled on it has left its
window (`must_serve_until` plus an hour) and no probe row of theirs still
awaits the late shadow verdict, so a chain whose retention is longer than
the flag holds the rollup rather than rolling a pending obligation; the
log says which day is waiting and why. From the first prune on the "all"
figures are the rollup plus the raw rows and carry a `rolled_up` label
(`raw_from`, days folded in); the shorter windows never touch it. The
retention pass runs hourly (`-retention-every`); the status file shows
`rollup_through` and `raw_from`. A warning in the log that obligations
were still pending at roll means `-rollup-after` is shorter than a
retention window on this chain: raise it. The
JSONL files are never rotated by the tools and remain the record; the
daily export is what a verifier downloads. The collector builds it: one
tarball per UTC day under `<DATA_DIR>/exports` (`-exports-dir`), once the
grace hour has passed (`-export-hour`, default 03:00 UTC, so late rows
land in their own day), served at `/v1/exports` with a manifest of
digests. `sentinel-recompute` re-derives every verdict and every
obligation figure from an untarred export and compares them with the
API's `?as_of=` answer; see `docs/verdicts.md`, "Reproducing the
figures". At mainnet's 148 MB/s the
publication rate is many times mocha's, which is why the decision is
written down now: a rollup added later could not reconstruct the "all"
window it replaced.

Two copies, both shipped:

- **litestream** for the database: copy `deploy/litestream.yml` to
  `/etc/fibre-observer/litestream-mocha.yml` (fix the `path` to the
  instance's data directory), put the bucket keys in
  `/etc/fibre-observer/litestream-mocha.env` (mode 0600), and enable
  `fibre-litestream@mocha`. It replicates the **derived** database only,
  continuously, with 72 h of history.
- **fibre-backup** for the record: `fibre-backup@mocha.timer` runs
  `rclone sync` of every `.jsonl` (the record, `registry.jsonl`,
  `runs.jsonl`, `sampling-secrets.jsonl`, `amendments.jsonl`), `state.json`, the status files
  and the daily exports to `BACKUP_REMOTE/<network>` nightly (`deploy/backup.sh`),
  with the rclone remote configured once in `/etc/fibre-observer/rclone.conf`.
  It never copies `sampling-master.key`, which must not leave the host, nor
  the database, which litestream covers. With `BACKUP_REMOTE` empty the
  timer runs and does nothing, so enable it everywhere and arm it with one
  variable.

**Rebuild from the record.** Stop the instance's collector and API, move
`observer.db*` aside, start the collector: it recreates the schema, replays
`registry.jsonl` (endpoint history), then tails the JSONL files from zero.
Every record has a natural key and every insert is `ON CONFLICT DO
NOTHING`, so a replay never duplicates. The run record (`/v1/runs`) comes
back from `runs.jsonl`, which every component appends its starts, stops
and flags to, the revealed sampling secrets from
`sampling-secrets.jsonl`, and the late shadow verdicts from
`amendments.jsonl`, the collector's own log of them (replayed before
anything is re-judged, so a rebuild never draws a verdict twice). What a rebuild does **not** bring back, because
it has no JSONL source: the collector's own run row, the escrow balances
and validator identities (re-polled within minutes), and the chain-side
`meta` keys (re-polled at once). Litestream's copy is the backup for those.

**Restore the database** from litestream: stop `fibre-collector@mocha` and
`fibre-api@mocha` (each holds the WAL), then
`litestream restore -config /etc/fibre-observer/litestream-mocha.yml /var/lib/fibre-observer/mocha/observer.db`,
delete any `observer.db-wal` / `-shm` left beside it, start both.

Test a restore and a rebuild before you need one: stop the collector, move
the database aside, restore or delete it, start the collector, and check
`/v1/meta` counts match.

### Runbook

- **Health is 503.** Read the `checks` list: it names the process or
  condition. A dead process: `journalctl -u fibre-<name>@<network> -n 100`.
  A `scanner_lag`: the RPC node is behind or slow; the scanner catches up
  on its own. A `scan_gaps`: the node could not serve those heights; point
  the scanner at a node that keeps them and delete `gaps` from `state.json`
  after re-scanning from the lowest gap height with `-start-height`, or
  accept the gap (the dashboard says which blocks). A `pin`: see below.
- **The chain upgraded past the pin** (`pin_status: chain_ahead`). The row
  assignment this observer computes depends on constants pinned to a
  celestia-app commit (`fibre-assign/params.go`), and a new major may change
  them. Until the pin is bumped, verdicts about who holds which rows may be
  wrong, and the site shows a banner. To bump: diff `fibre`, `x/fibre`,
  `x/valaddr`, `proto` and `specs` between the pinned commit and the release
  tag, update `PinnedCelestiaAppCommit`, `PinnedCelestiaAppVersion` and the
  `celestia-app` line plus the copied `replace` block in
  `fibre-sentinel/go.mod`, re-run `fibre-assign/reftest`, rebuild, deploy.
  `docs/research/R1-fibre-protocol-surface.md` has the detail.
- **A publication with no assignment** (`unassignable_publications` > 0):
  a blob version this build does not know. Same bump; the scanner does not
  re-scan settled publications, so re-scan from that height afterwards.
- **A process crash-loops.** `journalctl` shows the reason at the top of
  each attempt; the crash dump is the last 300 log lines. A full disk, an
  unreadable data directory or a chain-id mismatch are the known causes;
  none of them is an RPC outage.
- **Moving to a new host.** Copy the data directory (or restore from the
  two backups), install the binaries and units, copy `/etc/fibre-observer`,
  keep the same `VANTAGE` name if the egress addresses stay the same and a
  new one if they do not: the vantage is what a validator matches its logs
  against.

## 8. Checks after deploy

Run the smoke test first. It installs nothing and changes nothing: it parses
every unit, runs each one's own `ExecStart` with its own `EnvironmentFile` as
its own `User`, and checks that each reaches the chain and writes where it
should. That catches a binary in the wrong place, a flag that no longer
exists, a variable the env file never sets, and a directory the service user
cannot write, which is most of what actually goes wrong on a first deploy.

```bash
sudo deploy/test/smoke.sh http://127.0.0.1:26657 mocha
```

It does not test the sandboxing directives, which is what the
`systemd-analyze verify` step inside it is for, and it does not replace
starting the units for real:

```bash
sudo systemctl start fibre-scan@mocha fibre-heartbeat@mocha fibre-collector@mocha fibre-probe@mocha fibre-api@mocha
systemctl --no-pager status 'fibre-*@mocha' | grep -E 'Active|Loaded'
curl -s https://mocha.observer.example.org/api/v1/health | jq .status   # "ok" once every process has run a cycle
curl -s https://mocha.observer.example.org/api/v1/network | jq .registered_endpoints
```

Then check the site says where it watches from. If `complete` is false the
dashboard is publishing reachability verdicts without telling a reader which
network they were measured on, and the API will have logged a warning at
startup:

```bash
curl -s https://observer.example.org/api/v1/meta | jq .vantage_info
```

Confirm the ASN you declared is the one your traffic actually carries, since
that is the claim a reader will check:

```bash
whois -h whois.radb.net "$(curl -4 -s https://ifconfig.co)" | grep -i origin
```

Before Fibre activates on the chain, `publications` stays 0 and the site
shows the "0 Fibre publications" notice; that is the expected state.

## 9. What has been exercised

The units, the environment file, the Caddyfile and the whole process chain
were run against a local four-validator devnet on 16 September 2026: six
units verified by `systemd-analyze`, scanner, heartbeat, collector, prober
and API each started from their own unit as `fibre-observer`, writing to
`/var/lib/fibre-observer/data`, with `/v1/meta` serving the vantage block.
Both Caddyfiles validate under Caddy 2.8.4.

What has **not** been exercised anywhere: a real domain, a real certificate,
systemd as PID 1 actually supervising and restarting these units, litestream
replicating to real object storage, the template units with two instances
side by side, and `fibre-backup` against a real rclone remote. Those need a
host. The health endpoint, the status files, the snapshot persistence and
the registry replay are covered by the Go tests.
