# sample — outputs from real devnet runs

Committed as evidence. Local paths and usernames are scrubbed; the numbers,
timestamps, and JSON are verbatim.

## from `./devtest.sh 4 4` (scanner only)

| file | what it is |
|---|---|
| `publications.jsonl` | 4 publication records the scanner wrote — full `PaymentPromise`, `must_serve_until`, and the `fibre-assign` assignment table (per-validator row lists included) |
| `state.json` | the scan cursor + full param history + protocol-params fingerprint |
| `scan.log` | the scanner's own log for that run |
| `pub.log` | `sentinel-pub` publishing the 4 blobs |
| `devtest.log` | the whole `devtest.sh` run, ending in `sentinel-verify` PASS |

Each record shows `assign[with_rows=4 sigma=12291 distinct=12291 overlaps=0]`
and `must_serve_until = creation + 10m` (the devnet's `shard_retention`).

## from `./probe-devtest.sh 4 3` (scanner + prober + fault injection)

In `probe/`:

| file | what it is |
|---|---|
| `measurements.jsonl` | **60 raw measurements** — 3 blobs × 5 schedule points × 4 validators. Each has the L1–L4 per-layer breakdown (separate durations), the identity verdict, rows returned + both verification results, and the classification |
| `probe.log` | the prober's log — you can watch the schedule fire (`w1`/`w2`/`w3` in-window, then `grace`, then `post`) and the killed validator flip to `FAULT` |
| `measure-check.log` | `sentinel-measure-check` confirming the taxonomy: 0 checks failed |
| `probe-devtest.log` | the whole run |

One assigned validator's fibre server was killed ~3 min into the window. Result:
27 `HEALTHY`, 9 `FAULT` (that validator, in-window), 12 `TOLERATED` (grace,
post-prune), 9 `EXPECTED_GONE`, 3 `UNREACHABLE_POST_WINDOW` — zero
misclassifications.
