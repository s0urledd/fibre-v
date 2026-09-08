# fibre

Independent, outside-the-validator tooling for **Celestia Fibre** — the
low-latency data-availability path where validators sign for a blob and then owe
a retention window of serving it.

Nothing here needs to be run by a validator, and nothing trusts a validator's
self-report. Each module reads what it needs from the chain and recomputes the
rest.

| module | what it is | dependencies |
|---|---|---|
| [**fibre-tlsverify**](fibre-tlsverify/) | verifies the validator-endorsed TLS identity a Fibre server presents (consensus-key signed extension, no CA) | none — stdlib only |
| [**fibre-assign**](fibre-assign/) | recomputes which validator must serve which blob rows; `ShardMap.Verify` classifies what one actually returned | none — stdlib only (the differential test in `fibre-assign/reftest/` pulls celestia-app) |
| [**fibre-sentinel**](fibre-sentinel/) | the observer: `sentinel-scan` records every publication from the chain; `sentinel-probe` probes the assigned validators across each blob's window and classifies the result | celestia-app (pinned), + the two above |
| [**fibre-devnet**](fibre-devnet/) | `multi-node-fibre.sh` — a multi-validator local devnet with retention lowered to the 10-minute protocol floor, so assignment and pruning are observable in minutes | shell + a celestia-app build |

## Repository layout

A monorepo of three Go modules plus a script directory:

```
fibre-tlsverify/         module github.com/plsgiveup/fibre/fibre-tlsverify   (go 1.23)
fibre-assign/            module github.com/plsgiveup/fibre/fibre-assign      (go 1.23)
fibre-assign/reftest/    module github.com/plsgiveup/fibre/fibre-assign/reftest (go 1.26.5, differential test)
fibre-sentinel/          module github.com/plsgiveup/fibre/fibre-sentinel   (go 1.26.5)
fibre-devnet/            shell scripts, no Go module
```

`fibre-sentinel` and `fibre-assign/reftest` depend on their siblings through
in-repo relative `replace` directives — build them from a full checkout of this
repo, not from the subdirectory alone. `fibre-tlsverify` and `fibre-assign` are
independently `go get`-able as libraries.

For cross-module local development, drop a `go.work` at the repo root (it is
git-ignored):

```
go work init ./fibre-tlsverify ./fibre-assign ./fibre-assign/reftest ./fibre-sentinel
```

## Verify everything

Each claim is one command, reproducible from a clean checkout. CI
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs all of them on
every push.

```bash
# fibre-tlsverify — 21 golden vectors (1 valid + 20 typed failures), byte-exact envelope
cd fibre-tlsverify && go test -race ./...

# fibre-assign — 21 unit tests, then ~890 scenarios bit-identical to celestia-app
cd fibre-assign         && go test -race ./...
cd fibre-assign/reftest && go test ./...

# fibre-sentinel — 14 unit tests
cd fibre-sentinel && go test ./...

# fibre-sentinel — full devnet run with fault injection: 60 measurements, 0 misclassifications
#   (needs a celestia-app + fibre build on PATH; ~14 min)
cd fibre-sentinel && ./probe-devtest.sh 4 3
```

## Pinned upstream

celestia-app commit `0b69316466c3ba02f708c0e2a101f834d5d1827f`
(`v10.0.0-20260901162319-0b69316466c3`), celestia-core `v0.40.8`, cosmos-sdk
fork `v0.52.11`. TLS golden vectors from celestia-app commit
`dba155084505a8f6c5d37260a94f70f939fb96de`. `fibre-assign/reftest/go.mod` and
`fibre-sentinel/go.mod` each carry a verbatim copy of celestia-app's `replace`
block (Go does not apply a dependency's replaces); refresh the pin and the block
together.

## Research and design documents

The observer product (store, collector, prober, API, dashboard) is being built
on top of the modules above. The research that precedes it lives in `docs/`:

| document | what it answers |
|---|---|
| `docs/research/R1-fibre-protocol-surface.md` | what Fibre looks like on the wire and on chain at the pinned celestia-app commit, and what changed on main |
| `docs/research/R2-activation-and-rollout.md` | where Fibre activation on mocha-5 stands, how the server is packaged, what a validator's minimal setup is |
| `docs/research/R3-prior-art.md` | who else monitors Fibre, and how existing Celestia dashboards present validator data |
| `docs/research/R4-probe-etiquette.md` | how often and how much the observer may download without looking like an attack |
| `docs/research/R5-fdp-signals.md` | what the Foundation Delegation Program says it rewards, mapped to our deliverables |
| `docs/research/R6-hosting-and-cost.md` | what running the observer costs, with the arithmetic |
| `docs/research/R8-comparable-products-and-durability.md` | how Filecoin Spark, Xatu, Storj, Celenium, ProbeLab and L2BEAT measure and present, and the data-loss prevention patterns the observer adopts |
| `docs/research/R9-presentation-and-celestia-design.md` | Celestia's visual identity, Celenium conventions, trusted-dashboard patterns, and the dashboard design spec |
| `docs/research/R7-devnet-reproduction.md` | reproducing the tests and the devnet fault-injection run on a fresh Linux machine |
| `docs/research/R0-open-decisions.md` | decisions only the owners can make |
| `docs/adr/0001-observer-architecture.md` | the architecture decision for collector, prober, store, API and web |
| `docs/verdicts.md` | the verdict taxonomy the dashboard is allowed to use |

This repository is a fork of [plsgiveup/fibre](https://github.com/plsgiveup/fibre)
(Huginn Tech). Code changes are meant to flow back upstream as pull requests.

## License

Apache-2.0. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
