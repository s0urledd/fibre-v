# Deploying the observer

One VM runs everything. Five processes, one SQLite file, Caddy in front.

```
sentinel-scan ──► publications.jsonl, state.json ─┐
sentinel-probe ─► measurements.jsonl ─────────────┼─► observer-collector ─► observer.db ─► observer-api ─► Caddy ─► web/out
observer-heartbeat ─► reachability.jsonl ─────────┘                         (also polls x/valaddr)
```

The JSONL files are the append-only raw record; the SQLite database is
derived from them and can be rebuilt by deleting it and restarting the
collector. Back up the JSONL files and the database (see "Backups").

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
- Outbound TCP to validators' Fibre ports (default 7980).

## 2. Build

```bash
git clone https://github.com/plsgiveup/fibre && cd fibre
make build            # fibre-sentinel/bin/* and web/out/
```

## 3. Configure

Copy `deploy/observer.env.example` to `/etc/fibre-observer/observer.env`
and set `RPC`, `VANTAGE`, `DATA_DIR`. Copy
`fibre-sentinel/observer/policy/policy.example.yaml` to
`/etc/fibre-observer/policy.yaml` and set `sampling.master_secret_file`
to a path under `/etc/fibre-observer/` so the sampling secret survives
restarts (the prober creates it with mode 0600 on first run).

## 4. systemd

```bash
sudo useradd --system --home /var/lib/fibre-observer --create-home fibre-observer
sudo install -d -o fibre-observer /var/lib/fibre-observer/data
sudo install -m 0755 fibre-sentinel/bin/* /usr/local/bin/
sudo cp deploy/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fibre-scan fibre-probe fibre-heartbeat fibre-collector fibre-api
```

Each unit runs one binary with the flags from `observer.env`. Order does
not matter: the collector tolerates missing files, and before Fibre is
active on the chain the prober and heartbeat have nothing to do and say
so in their logs.

`sudo systemctl status 'fibre-*'` and `journalctl -u fibre-probe -f`.

## 5. Caddy

Install the site: `sudo mkdir -p /var/www/fibre-observer && sudo cp -r web/out/. /var/www/fibre-observer/`.
Then `deploy/Caddyfile` with your domain in place of `observer.example.org`:

```bash
sudo cp deploy/Caddyfile /etc/caddy/Caddyfile && sudo systemctl reload caddy
```

Caddy serves the static export, proxies `/api/*` to `observer-api` on
127.0.0.1:8080, and gets a TLS certificate from Let's Encrypt.

## 6. docker compose (alternative)

```bash
cp deploy/observer.env.example deploy/.env   # edit RPC, VANTAGE, DOMAIN
docker compose -f deploy/docker-compose.yml up -d --build
```

Same five processes plus Caddy, one image built from `deploy/Dockerfile`.
Data lives in the `observer-data` volume.

## 7. Backups

The raw files and the database are small (a few GB per month at the
stress scenario in R6). Two options:

- **litestream** (recommended for SQLite): `deploy/litestream.yml`
  replicates `observer.db` continuously to an S3-compatible bucket
  (Cloudflare R2 works). Restore with
  `litestream restore -config deploy/litestream.yml /var/lib/fibre-observer/data/observer.db`.
- **rsync** the whole data directory nightly. The JSONL files are
  append-only, so incremental copies are cheap, and the database can be
  rebuilt from them.

Test a restore before you need one: stop the collector, move the database
aside, restore or delete it, start the collector, and check `/v1/meta`
counts match.

## 8. Checks after deploy

```bash
curl -s https://observer.example.org/api/v1/meta | jq .collector.alive   # true
curl -s https://observer.example.org/api/v1/network | jq .registered_endpoints
```

Before Fibre activates on the chain, `publications` stays 0 and the site
shows the "0 Fibre publications" notice; that is the expected state.
