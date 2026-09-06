# fibre-v

Research and design notes for the **Fibre observer**: an independent, public
dashboard that checks whether Celestia validators keep the serving promise they
make when they sign a Fibre blob.

The code lives in [plsgiveup/fibre](https://github.com/plsgiveup/fibre)
(modules `fibre-tlsverify`, `fibre-assign`, `fibre-sentinel`, `fibre-devnet`,
Apache-2.0). This repository holds the documents that precede the product
work. Everything under `docs/` is written to be moved into that repository's
`docs/` directory unchanged once the owners settle the repo-identity question
(see `docs/research/R0-open-decisions.md`).

| document | what it answers |
|---|---|
| `docs/research/R1-fibre-protocol-surface.md` | what Fibre looks like on the wire and on chain at the pinned celestia-app commit, and what changed on main |
| `docs/research/R2-activation-and-rollout.md` | where Fibre activation on mocha-5 stands, how the server is packaged, what a validator's minimal setup is |
| `docs/research/R3-prior-art.md` | who else monitors Fibre, and how existing Celestia dashboards present validator data |
| `docs/research/R4-probe-etiquette.md` | how often and how much the observer may download without looking like an attack |
| `docs/research/R5-fdp-signals.md` | what the Foundation Delegation Program says it rewards, mapped to our deliverables |
| `docs/research/R6-hosting-and-cost.md` | what running the observer costs, with the arithmetic |
| `docs/research/R0-open-decisions.md` | decisions only the owners can make |
| `docs/adr/0001-observer-architecture.md` | the architecture decision for collector, prober, store, API and web |
| `docs/verdicts.md` | the verdict taxonomy the dashboard is allowed to use, one sentence per class |

Every factual claim in these documents links to its source. Where a source
could not be found the text says "unknown" and what would resolve it.
