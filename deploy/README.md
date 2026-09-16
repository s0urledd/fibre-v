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
- If the same host also runs your own validator and Fibre server: celestia-app
  main (#7848, 15 Sep 2026) recommends separate disks for Fibre shards and
  the node's data; the observer's data directory should not share the Fibre
  shard disk either.

## 2. Build

```bash
git clone https://github.com/plsgiveup/fibre && cd fibre
make build            # fibre-sentinel/bin/* and web/out/
```

## 3. Configure

```bash
sudo install -d -m 0755 /etc/fibre-observer
sudo cp deploy/observer.env.example /etc/fibre-observer/observer.env
sudo cp fibre-sentinel/observer/policy/policy.mocha.yaml /etc/fibre-observer/policy.yaml   # mocha-5; policy.example.yaml for mainnet
```

Edit `observer.env`: set `RPC`, `VANTAGE`, `DATA_DIR`. In `policy.yaml`
set `sampling.master_secret_file: /var/lib/fibre-observer/master.secret`
so the sampling secret survives restarts; the prober creates it with mode
0600 on first run. The file must live under the data directory: the units
mount `/etc/fibre-observer` read-only, and the service user cannot write
there.

Leave `-probe-unassigned` off on a public vantage. The read-path rate
limiting Celestia is designing (forum topic 2295) treats requests for
shards a validator was never assigned as illegitimate; probing only real,
in-window, correctly assigned commitments is what keeps the observer's
traffic on the right side of it.

## 4. systemd

```bash
sudo useradd --system --home /var/lib/fibre-observer --create-home fibre-observer
sudo install -d -o fibre-observer -m 0750 /var/lib/fibre-observer/data
sudo install -m 0755 fibre-sentinel/bin/* /usr/local/bin/
sudo cp deploy/systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now fibre-scan fibre-probe fibre-heartbeat fibre-collector fibre-api
```

The units are hardened (`ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`,
an empty `CapabilityBoundingSet`, `SystemCallFilter=@system-service`) and can
write only `/var/lib/fibre-observer/data`. The sampling secret must therefore
sit under the data directory, not under `/etc`.

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

Budget for disk: one measurement is about 1.5 KB in `measurements.jsonl`
and about twice that again in the database. At the R4 stress scenario
(60 publications an hour, 100 validators, 6 points) that is about 1.3 GB a
day of JSONL plus the database; at a realistic mocha rate it is a few GB a
month. The JSONL files are the record and are never rotated by the tools;
move them off the box when the disk fills and rebuild the database from
them if ever needed. Two options for copies:

- **litestream** (recommended for SQLite): copy `deploy/litestream.yml` to
  `/etc/fibre-observer/litestream.yml`, put the bucket keys in
  `/etc/fibre-observer/litestream.env` (mode 0600), and enable
  `deploy/systemd/fibre-litestream.service`. It replicates the **derived**
  database only, not the JSONL files it is rebuilt from. To restore, stop
  both `fibre-collector` and `fibre-api` first (each holds the WAL), then
  `litestream restore -config /etc/fibre-observer/litestream.yml /var/lib/fibre-observer/data/observer.db`.
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
