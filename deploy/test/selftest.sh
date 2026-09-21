#!/usr/bin/env bash
#
# selftest: the acceptance tests' own regression tests. Runs anywhere with
# bash, python3 and curl — CI included — against fake servers on loopback,
# and needs no root, no systemd, no rclone. Each case is a bug that was
# shipped once:
#
#   http_code       a closed port is 000, not 000000; 200 is 200; 503 is 503
#   health reasons  503 with the chain source named passes; 503 for the disk
#                   alone does not; "ok" does not; garbage does not
#   env parsing     values with spaces and quotes survive, comments and bad
#                   lines are skipped, every pair reaches the command as its
#                   own argument (the xargs bug)
#   manifest        a copy that grew after the cut verifies (and is trimmed);
#                   a truncated, altered or missing file fails; the master
#                   key in the copy fails; a state.json the copy took later
#                   (records at 100, checkpoint at 101) is replaced by the
#                   cut's own, whole, not just its height; a manifest that
#                   carries no state cannot accept one past its checkpoint;
#                   the cut ends on a line boundary while a writer is
#                   mid-line; a cut that ends inside a line fails verify; a
#                   line that is not a JSON record is refused at the cut
#   rpc-check       app version 9 + fibre code 6 passes; 10 + 6 fails; 10 + 0
#                   passes; no block_results fails; a second RPC that does
#                   not answer fails; two nodes disagreeing on a hash fails;
#                   two agreeing pass
#
# Usage: deploy/test/selftest.sh     Exit 0 when every case passes.
set -o errexit -o nounset -o pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
MANIFEST="$HERE/../backup-manifest.py"
T=$(mktemp -d)
PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; rm -rf "$T"; }
trap cleanup EXIT
ok=0; bad=0
check() { # the command's own output goes to the log; only a failure is printed
  if "$@" >> "$T/check.log" 2>&1; then ok=$((ok+1)); else bad=$((bad+1)); echo "  FAIL: $*"; tail -5 "$T/check.log" | sed 's/^/        /'; fi
}
not() { ! "$@"; }
eq() { [ "$1" = "$2" ] || { echo "  expected [$2], got [$1]"; return 1; }; }
serve() { # serve <script> <args...>: starts a fake server, sets PORT
  # Not $(serve ...): a background child inherits the substitution's pipe and
  # the substitution never returns, and PIDS+= in a subshell is lost.
  PORT=$(free_port)
  python3 "$HERE/$1" --port "$PORT" "${@:2}" > "$T/$1.$PORT.log" 2>&1 &
  PIDS+=("$!")
  wait_http "http://127.0.0.1:$PORT/status" 10 "200 404 503" >/dev/null || true
}

echo "== http_code"
closed=$(free_port)
check eq "$(http_code "http://127.0.0.1:$closed/v1/health")" 000
printf '{"status":"ok","checks":[]}' > "$T/ok.json"
serve fake-http.py --code 200 --body "$T/ok.json"; p200=$PORT
check eq "$(http_code "http://127.0.0.1:$p200/v1/health")" 200
cat > "$T/degraded.json" <<'J'
{"status":"degraded","checks":[{"name":"scanner","ok":true,"detail":"alive"},{"name":"chain_liveness","ok":false,"detail":"newest block 11m3s old (2026-09-21T10:00:00Z)"}]}
J
serve fake-http.py --code 503 --body "$T/degraded.json"; p503=$PORT
check eq "$(http_code "http://127.0.0.1:$p503/v1/health" "$T/body.json")" 503
check eq "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["status"])' "$T/body.json")" degraded

echo "== health reasons"
check health_has_reason "$T/degraded.json" "chain_liveness,scanner,collector"
printf '{"status":"degraded","checks":[{"name":"disk","ok":false,"detail":"3%% free"}]}' > "$T/disk.json"
check not health_has_reason "$T/disk.json" "chain_liveness,scanner,collector"
printf '{"status":"degraded","checks":[{"name":"chain_liveness","ok":false,"detail":""}]}' > "$T/nodetail.json"
check not health_has_reason "$T/nodetail.json" "chain_liveness"
check not health_has_reason "$T/ok.json" "chain_liveness"
printf 'not json' > "$T/garbage.json"
check not health_has_reason "$T/garbage.json" "chain_liveness"
check eq "$(health_bad_checks "$T/degraded.json")" "chain_liveness: newest block 11m3s old (2026-09-21T10:00:00Z)"

echo "== env parsing"
cat > "$T/test.env" <<'E'
# a comment
NETWORK=mocha
VANTAGE_LOCATION="Helsinki, Finland"
VANTAGE_PROVIDER='Hetzner Online'
ALERT_WEBHOOK=https://hooks.example.org/x/y?a=1&b=2
export START_HEIGHT=0
EMPTY=
not an assignment
E
mapfile -t pairs < <(parse_envfile "$T/test.env")
check eq "${#pairs[@]}" 6
check eq "$(envval "$T/test.env" VANTAGE_LOCATION)" "Helsinki, Finland"
check eq "$(envval "$T/test.env" VANTAGE_PROVIDER)" "Hetzner Online"
check eq "$(envval "$T/test.env" ALERT_WEBHOOK)" "https://hooks.example.org/x/y?a=1&b=2"
check eq "$(envval "$T/test.env" MISSING)" ""
# every pair is one argument, and the command sees the exact values
check eq "$(env "${pairs[@]}" sh -c 'printf "%s|%s|%s|%s" "$VANTAGE_LOCATION" "$VANTAGE_PROVIDER" "$START_HEIGHT" "$ALERT_WEBHOOK"')" "Helsinki, Finland|Hetzner Online|0|https://hooks.example.org/x/y?a=1&b=2"
check eq "$(run_as_service "$T/test.env" "$(id -un)" sh -c 'printf "%s" "$VANTAGE_LOCATION"')" "Helsinki, Finland"

echo "== backup manifest"
D="$T/data"; mkdir -p "$D"
for i in 1 2 3; do printf '{"promise_hash":"p%d","x":%d}\n' "$i" "$i" >> "$D/publications.jsonl"; done
for i in 1 2 3 4 5; do printf '{"vantage":"t","promise_hash":"p1","validator_address":"v%d","scheduled_at":"2026-09-21T00:00:0%dZ"}\n' "$i" "$i" >> "$D/measurements.jsonl"; done
printf '{"last_scanned_height":500,"last_scanned_time":"2026-09-21T00:00:00Z"}\n' > "$D/state.json"
printf 'secret' > "$D/sampling-master.key"
check python3 "$MANIFEST" write "$D" "$T/manifest.json" >/dev/null
check eq "$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); print(m["files"]["publications.jsonl"]["records"], m["files"]["measurements.jsonl"]["records"], m["checkpoint"]["last_scanned_height"])' "$T/manifest.json")" "3 5 500"
# a copy taken after the cut: longer, verifies, is trimmed
copy() { rm -rf "$T/copy"; mkdir -p "$T/copy"; cp "$D/publications.jsonl" "$D/measurements.jsonl" "$D/state.json" "$T/copy/"; }
copy; printf '{"promise_hash":"p4","x":4}\n' >> "$T/copy/publications.jsonl"
check python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json" >/dev/null
check eq "$(wc -l < "$T/copy/publications.jsonl")" 3
# truncated
copy; head -c 20 "$D/measurements.jsonl" > "$T/copy/measurements.jsonl"
check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json"
# altered in place, same length
copy; printf '{"promise_hash":"pX","x":1}\n' > "$T/copy/publications.jsonl"; tail -n +2 "$D/publications.jsonl" >> "$T/copy/publications.jsonl"
check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json"
# missing file
copy; rm "$T/copy/measurements.jsonl"
check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json"
# the key came along
copy; cp "$D/sampling-master.key" "$T/copy/"
check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json"
ckpt() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("last_scanned_height", 0))' "$1"; }
# records at 100, checkpoint at 101: after the cut the scanner appended a
# record and moved its checkpoint past it, and the copy carried both. The
# record is trimmed back to the cut and state.json is the cut's own — the
# whole file, gaps and all — not the copy's: a checkpoint past the records
# it sits beside would resume the scanner past blocks this cut never held.
copy; printf '{"promise_hash":"p101","x":101}\n' >> "$T/copy/publications.jsonl"
printf '{"last_scanned_height":501,"last_scanned_time":"2026-09-21T00:00:03Z","gaps":[{"from":501,"to":501}]}\n' > "$T/copy/state.json"
check python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json" >/dev/null
check eq "$(wc -l < "$T/copy/publications.jsonl")" 3
check eq "$(ckpt "$T/copy/state.json")" 500
check cmp -s "$T/copy/state.json" "$D/state.json"
# a checkpoint behind the cut is replaced the same way, and a copy with no
# state.json gets the cut's
copy; printf '{"last_scanned_height":400}\n' > "$T/copy/state.json"
check python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json" >/dev/null
check eq "$(ckpt "$T/copy/state.json")" 500
copy; rm "$T/copy/state.json"
check python3 "$MANIFEST" verify "$T/copy" "$T/manifest.json" >/dev/null
check cmp -s "$T/copy/state.json" "$D/state.json"
# a manifest from before the state was carried cannot repair one: a copy
# whose state.json is past (or behind) its checkpoint fails, not passes
python3 - "$T/manifest.json" "$T/manifest-v1.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1])); m.pop("state_raw"); m.pop("state"); m["version"] = 1
json.dump(m, open(sys.argv[2], "w"))
PY
copy; printf '{"last_scanned_height":501}\n' > "$T/copy/state.json"
check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest-v1.json"
copy; check python3 "$MANIFEST" verify "$T/copy" "$T/manifest-v1.json" >/dev/null
# the manifest's own state must match its hash: an altered manifest fails
python3 - "$T/manifest.json" "$T/manifest-forged.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1])); m["state_raw"] = '{"last_scanned_height":900}\n'
json.dump(m, open(sys.argv[2], "w"))
PY
copy; check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest-forged.json"
# a writer mid-line at the cut: the cut ends at the last complete line
printf '{"promise_hash":"p4","x":4}\n{"promise_hash":"p5","x":' >> "$D/publications.jsonl"
check python3 "$MANIFEST" write "$D" "$T/manifest2.json" >/dev/null
check eq "$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); print(m["files"]["publications.jsonl"]["records"])' "$T/manifest2.json")" 4
check eq "$(python3 -c 'import json,sys; m=json.load(open(sys.argv[1])); print(m["files"]["publications.jsonl"]["bytes"])' "$T/manifest2.json")" \
         "$(python3 -c 'import sys; b=open(sys.argv[1],"rb").read(); print(b.rfind(b"\n")+1)' "$D/publications.jsonl")"
copy; check python3 "$MANIFEST" verify "$T/copy" "$T/manifest2.json" >/dev/null
check eq "$(tail -c 1 "$T/copy/publications.jsonl" | od -An -c | tr -d ' ')" '\n'
# a cut that ends inside a line — a manifest that hashes half a record —
# fails verify even though the hash over those bytes matches: every line
# is parsed, and the last one is not a record
python3 - "$T/manifest2.json" "$D/publications.jsonl" "$T/manifest-torn.json" <<'PY'
import hashlib, json, sys
m = json.load(open(sys.argv[1])); n = m["files"]["publications.jsonl"]["bytes"] - 3
b = open(sys.argv[2], "rb").read()[:n]
m["files"]["publications.jsonl"] = {"bytes": n, "sha256": hashlib.sha256(b).hexdigest(), "records": b.count(b"\n")}
json.dump(m, open(sys.argv[3], "w"))
PY
copy; check not python3 "$MANIFEST" verify "$T/copy" "$T/manifest-torn.json"
# snapshot copies exactly the cut (no torn tail), never the key, and its
# state.json is the cut's
rm -rf "$T/snap"; check python3 "$MANIFEST" snapshot "$D" "$T/snap" >/dev/null
check test ! -e "$T/snap/sampling-master.key"
check eq "$(wc -l < "$T/snap/publications.jsonl")" 4
check cmp -s "$T/snap/state.json" "$D/state.json"
check python3 "$MANIFEST" verify "$T/snap" >/dev/null
# a line that is not a JSON record is refused at the cut, whatever its length
printf 'not a record\n' >> "$D/measurements.jsonl"
check not python3 "$MANIFEST" write "$D" "$T/manifest3.json"
check test ! -e "$T/manifest3.json"

echo "== rpc-check"
RC="$HERE/rpc-check.sh"
serve fake-rpc.py --app-version 9 --fibre-code 6; r9=$PORT
check "$RC" "http://127.0.0.1:$r9"
serve fake-rpc.py --app-version 10 --fibre-code 6; r10bad=$PORT
check not "$RC" "http://127.0.0.1:$r10bad"
serve fake-rpc.py --app-version 10 --fibre-code 0; r10=$PORT
check "$RC" "http://127.0.0.1:$r10"
serve fake-rpc.py --no-block-results; rnores=$PORT
check not "$RC" "http://127.0.0.1:$rnores"
check not "$RC" "http://127.0.0.1:$r9" "http://127.0.0.1:$closed"
serve fake-rpc.py --hash-salt other; rsalt=$PORT
check not "$RC" "http://127.0.0.1:$r9" "http://127.0.0.1:$rsalt"
serve fake-rpc.py --app-version 9 --fibre-code 6; r9b=$PORT
check "$RC" "http://127.0.0.1:$r9" "http://127.0.0.1:$r9b"

echo
echo "selftest: $ok passed, $bad failed"
[ "$bad" = 0 ]
