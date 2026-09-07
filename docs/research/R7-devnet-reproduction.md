# R7. Reproducing the test suite and the devnet run on a fresh machine

Date: 6 September 2026. Machine: fresh Linux container (kernel 6.18, x86_64,
4 vCPU, 15 GB RAM), Go 1.24.7 installed, `GOTOOLCHAIN=auto` so the 1.26.5
toolchain celestia-app pins was downloaded automatically. Repo:
`plsgiveup/fibre` at `ac4bae4` (the only commit).

The brief's acceptance criterion is "devnet run green on a fresh machine
following only the README". This note records what happened when that was
attempted literally.

## 1. Unit and differential tests: pass

Commands exactly as the top-level README lists them, from a full checkout:

| command | result |
|---|---|
| `cd fibre-tlsverify && go vet ./... && go test -race -shuffle=on ./...` | ok, 1.1 s |
| `cd fibre-assign && go vet ./... && go test -race -shuffle=on ./...` | ok, 1.0 s |
| `cd fibre-assign/reftest && go test -shuffle=on ./...` | ok, 1.0 s after module download (pulls celestia-app and the 1.26.5 toolchain from the proxy; several minutes on first run) |
| `cd fibre-sentinel && go vet ./... && go build ./... && go test -shuffle=on ./...` | ok: `internal/probe` 0.25 s, `internal/scan` 0.18 s |
| `gofmt -l .` | clean |

The CI workflow in the repo runs the same commands, so CI on the upstream repo
and this reproduction agree.

## 2. Building the pinned celestia-app: pass

From a checkout of celestia-app at `0b69316466c3ba02f708c0e2a101f834d5d1827f`,
the two commands the devnet README gives:

| command | wall time |
|---|---|
| `go build -tags ledger -o build/celestia-appd ./cmd/celestia-appd` | 52 s (190 MB binary) |
| `go build -o build/fibre ./fibre/cmd` | 8 s (80 MB binary) |

Both binaries start and print help. The `fibre` binary has exactly the
subcommands and flags R1 describes.

## 3. Devnet plus fault injection: two portability defects, then pass

`cd fibre-sentinel && go build -o bin/ ./cmd/...` then, with `bin/`,
`fibre-devnet/` and the celestia-app `build/` directory on `PATH`,
`./probe-devtest.sh 4 3` as the README says.

### Defect 1: scripts are not executable in git

`./probe-devtest.sh` fails with "Permission denied". All three shell scripts
are committed with mode `100644`:

```
100644 fibre-devnet/multi-node-fibre.sh
100644 fibre-sentinel/devtest.sh
100644 fibre-sentinel/probe-devtest.sh
```

The README invokes them as `./script.sh` and `probe-devtest.sh` invokes
`multi-node-fibre.sh` from `PATH`, so both need the executable bit. Fix:
`git update-index --chmod=+x` on the three files. Workaround used here:
`chmod +x` after cloning.

### Defect 2: fault injection uses Windows-only tools

With the executable bit set, the devnet comes up (4 validators, 4 fibre
servers registered on chain, uploader escrow funded, READY in about 50 s),
the scanner records all 3 publications with the expected assignment
(`sigma == distinct == 12291`, 4 validators with rows), and the prober starts.
The script then exits at the fault-injection step with exit code 1 and no
message. Cause: `pid_on_port` runs `netstat -ano -p tcp ... LISTENING`, the
Windows netstat syntax; `netstat` is absent on this machine, and with
`set -o errexit -o pipefail` the failing command substitution aborts the
script before `fail` can print. The same scripts also call
`taskkill //F //IM *.exe`, guarded with `|| true`, so those are harmless on
Linux but show the scripts were developed under Git Bash on Windows.

The devnet script already writes a pid table
(`<devnet home>/logs/fibre-pids`, lines of `<index> <pid> <port>`) for exactly
this purpose. The patch in
`patches/0001-probe-devtest-portable-pid-lookup.patch` makes `pid_on_port`
read that table first, fall back to `lsof`, then to Windows `netstat`, and
never abort on a missing tool. `devtest.sh` does not look up ports and needs
only the executable bit.

### Result with the patch applied

`./probe-devtest.sh 4 3` completed with exit code 0 in about 18 minutes:
devnet READY in ~50 s, 3 blobs of 256 KiB published (4 signatures each),
scanner recorded all three with `must_serve_until = creation + 10m`, prober
drained the whole schedule, node 1's fibre server killed 174 s after the
first publish, `sentinel-measure-check` reported `0 checks failed`.

| classification | count | matches upstream `sample/` |
|---|---:|---|
| `HEALTHY` | 27 | yes |
| `FAULT` | 9 | yes (the killed validator, in window, caught at TCP in 0 ms) |
| `TOLERATED` | 12 | yes |
| `EXPECTED_GONE` | 9 | yes |
| `UNREACHABLE_POST_WINDOW` | 3 | yes |
| total | 60 | yes |

By outcome: 27 `SERVED_OK`, 18 `NOT_FOUND`, 15 `TCP_REFUSED`. Assigned row
counts were 3186 for the proposer and 3035 for the other three, exactly the
numbers in `docs/fibre-operational-notes.md`. Probe cost on this machine,
localhost, per full probe (dial, TLS, identity, `DownloadShard`, verify
~3000 rows):

| outcome | n | p50 | p95 | max |
|---|---:|---:|---:|---:|
| `SERVED_OK` | 27 | 24 ms | 30 ms | 32 ms |
| `NOT_FOUND` | 18 | 3 ms | | 4 ms |
| `TCP_REFUSED` | 15 | 0 ms | | 0 ms |

These sit inside the ranges the upstream notes report (p50 14 to 24 ms, p95
24 to 40 ms), so the taxonomy and the cost figures reproduce on a fresh
Linux machine once the two defects above are fixed. The first `TOLERATED`
`NOT_FOUND` in this run was observed at `must_serve_until + 2m00s` (the
grace probe is scheduled at +120 s in this script), consistent with the
measured prune lag of about 1m45s.

## 4. What to change upstream

1. `git update-index --chmod=+x fibre-devnet/multi-node-fibre.sh fibre-sentinel/devtest.sh fibre-sentinel/probe-devtest.sh`
2. Apply `patches/0001-probe-devtest-portable-pid-lookup.patch`.
3. Add a CI job that runs `probe-devtest.sh` on Linux, or at least a
   `bash -n` plus a Linux-only `pid_on_port` unit check. The existing
   `scripts` CI job only checks syntax, which is why neither defect was
   caught.
4. Mention in the devnet README that the scripts were developed on Windows
   and list the Linux tools they need (`lsof` or the pid table).
