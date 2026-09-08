# Observer test fixture

One `probe-devtest.sh 4 3` run (celestia-app `0b69316`, 4 validators, 3 blobs
of 256 KiB, node 1's fibre server killed 174 s after the first publish),
recorded on a fresh Linux machine on 6 September 2026; see
`docs/research/R7-devnet-reproduction.md`.

| file | rows | notes |
|---|---|---|
| `publications.jsonl` | 3 | full row lists (`-rows=true`) |
| `measurements.jsonl` | 60 | 27 HEALTHY, 9 FAULT, 12 TOLERATED, 9 EXPECTED_GONE, 3 UNREACHABLE_POST_WINDOW |
| `state.json` | | chain id `fibre-devnet`, params seeded at 10 m retention |

Unlike `fibre-sentinel/sample/`, whose `publications.jsonl` and
`probe/measurements.jsonl` come from two different devnet runs, these three
files are from the same run, so validator addresses line up across tables.
