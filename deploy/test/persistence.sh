#!/usr/bin/env bash
#
# persistence: the record survives a restart, the index is sound, and the
# index can be thrown away and rebuilt from the record to the same answer.
#
#   1. restart the collector and the scanner; every checkpoint (state.json
#      last_scanned_height, ingest cursors) must be at or past where it was,
#      the scanner must log that it resumed, and the row counts must not
#      drop.
#   2. PRAGMA integrity_check and foreign_key_check on the live database.
#   3. the record has no duplicate publication or measurement: a restart
#      that re-appended a line would show here before it showed anywhere.
#   4. rebuild the database from the JSONL alone in a temporary directory
#      (observer-collector --once) and compare it with the record and the
#      live database.
#   5. sentinel-recompute re-derives every verdict and obligation figure from
#      that copy of the record and compares them with what the live API
#      answers. Exit 0 from it is the claim docs/verdicts.md makes.
#
# Usage: sudo deploy/test/persistence.sh [instance]   (default mocha)
# Needs python3, the binaries in /usr/local/bin, and about the size of the
# data directory free under $TMPDIR. Exit 0 when every check passes.
set -o errexit -o nounset -o pipefail

INSTANCE="${1:-mocha}"
ENVFILE=/etc/fibre-observer/$INSTANCE.env
FAILED=0
pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; FAILED=1; }
warn() { echo "  warn $*"; }

[ -r "$ENVFILE" ] || { echo "no $ENVFILE" >&2; exit 2; }
envval() { sed -n "s/^$1=//p" "$ENVFILE" | head -1; }
DATA_DIR=$(envval DATA_DIR); RPC=$(envval RPC); VANTAGE=$(envval VANTAGE); API_LISTEN=$(envval API_LISTEN)
DB="$DATA_DIR/observer.db"
[ -f "$DB" ] || { echo "no $DB" >&2; exit 2; }

# sql <db> <query> [sep]: rows on stdout, read-only, python so nothing else
# has to be installed on the box.
sql() {
  python3 - "$1" "$2" <<'PY'
import sqlite3, sys
con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
for row in con.execute(sys.argv[2]):
    print("\t".join("" if v is None else str(v) for v in row))
PY
}
scanned() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("last_scanned_height", 0))' "$DATA_DIR/state.json" 2>/dev/null || echo 0; }
counts() { sql "$1" "SELECT (SELECT COUNT(*) FROM publications), (SELECT COUNT(*) FROM probes), (SELECT COUNT(*) FROM reachability)"; }
cursors() { sql "$1" "SELECT file, byte_offset FROM ingest_cursors ORDER BY file"; }

echo "== 1. restart keeps every checkpoint"
before_h=$(scanned); before_c=$(counts "$DB"); before_cur=$(cursors "$DB")
echo "  before: scanned=$before_h counts(pub,probe,reach)=$before_c"
systemctl restart "fibre-collector@$INSTANCE" "fibre-scan@$INSTANCE"
sleep 25
for u in fibre-collector fibre-scan; do
  [ "$(systemctl is-active "$u@$INSTANCE")" = "active" ] && pass "$u@$INSTANCE active after restart" || fail "$u@$INSTANCE not active after restart"
done
after_h=$(scanned); after_c=$(counts "$DB"); after_cur=$(cursors "$DB")
[ "$after_h" -ge "$before_h" ] && pass "scanner checkpoint $before_h -> $after_h" || fail "scanner checkpoint went backwards: $before_h -> $after_h"
if journalctl -u "fibre-scan@$INSTANCE" --since '-2 min' --no-pager 2>/dev/null | grep -q -E 'resuming: last_scanned='; then
  pass "scanner logged that it resumed from its checkpoint"
else
  warn "no 'resuming' line in the scanner journal in the last 2 min"
fi
python3 - "$before_c" "$after_c" <<'PY' && pass "row counts did not drop ($before_c -> $after_c)" || fail "row counts dropped: $before_c -> $after_c"
import sys
b = [int(x) for x in sys.argv[1].split("\t")]; a = [int(x) for x in sys.argv[2].split("\t")]
sys.exit(0 if all(x >= y for x, y in zip(a, b)) else 1)
PY
python3 - "$before_cur" "$after_cur" <<'PY' && pass "ingest cursors did not go backwards" || fail "an ingest cursor went backwards"
import sys
def parse(s):
    return {l.split("\t")[0]: int(l.split("\t")[1]) for l in s.splitlines() if "\t" in l}
b, a = parse(sys.argv[1]), parse(sys.argv[2])
sys.exit(0 if all(a.get(f, 0) >= o for f, o in b.items()) else 1)
PY

echo "== 2. database integrity"
ic=$(sql "$DB" "PRAGMA integrity_check" | head -3)
[ "$ic" = "ok" ] && pass "integrity_check ok" || fail "integrity_check: $ic"
fk=$(sql "$DB" "PRAGMA foreign_key_check" | wc -l)
[ "$fk" = 0 ] && pass "foreign_key_check clean" || fail "foreign_key_check: $fk violation(s)"

echo "== 3. no duplicate in the record"
dups=$(python3 - "$DATA_DIR" <<'PY'
import json, os, sys
d = sys.argv[1]
def scan(name, keyf):
    path = os.path.join(d, name)
    if not os.path.exists(path):
        return (0, 0, 0)
    seen, dup, n = set(), 0, 0
    with open(path, "rb") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            n += 1
            try:
                k = keyf(json.loads(line))
            except Exception:
                continue
            if k is None:
                continue
            if k in seen:
                dup += 1
            seen.add(k)
    return (n, len(seen), dup)
p = scan("publications.jsonl", lambda r: r.get("promise_hash"))
m = scan("measurements.jsonl", lambda r: None if not all(k in r for k in ("vantage","promise_hash","validator_address","scheduled_at")) else (r["vantage"], r["promise_hash"], r["validator_address"], r["scheduled_at"]))
print(f"{p[0]}\t{p[1]}\t{p[2]}\t{m[0]}\t{m[1]}\t{m[2]}")
PY
)
IFS=$'\t' read -r plines pkeys pdup mlines mkeys mdup <<<"$dups"
[ "$pdup" = 0 ] && pass "publications.jsonl: $plines lines, $pkeys distinct promises, 0 duplicates" || fail "publications.jsonl: $pdup duplicate promise line(s)"
[ "$mdup" = 0 ] && pass "measurements.jsonl: $mlines lines, $mkeys distinct slots, 0 duplicates" || fail "measurements.jsonl: $mdup duplicate slot line(s)"

echo "== 4. rebuild the index from the record"
TMP=$(mktemp -d "${TMPDIR:-/tmp}/fibre-rebuild.XXXXXX")
trap 'rm -rf "$TMP"' EXIT
cp "$DATA_DIR"/*.jsonl "$TMP"/ 2>/dev/null || true
[ -f "$DATA_DIR/state.json" ] && cp "$DATA_DIR/state.json" "$TMP"/
[ -f "$TMP/sampling-master.key" ] && { fail "the master key was copied; the rebuild copies only the record"; rm -f "$TMP/sampling-master.key"; }
if timeout 1200 /usr/local/bin/observer-collector -rpc "$RPC" -data-dir "$TMP" -vantage "$VANTAGE" -once \
     -endpoints-every 0 -escrow-every 0 -avatars-every 0 -export-hour -1 > "$TMP/rebuild.log" 2>&1; then
  pass "observer-collector --once rebuilt $TMP/observer.db ($(tail -1 "$TMP/rebuild.log"))"
else
  fail "rebuild failed: $(tail -3 "$TMP/rebuild.log")"
fi
if [ -f "$TMP/observer.db" ]; then
  rc=$(counts "$TMP/observer.db")
  IFS=$'\t' read -r rpub rprobe _ <<<"$rc"
  [ "$rpub" = "$pkeys" ] && pass "rebuilt publications ($rpub) == distinct promises in the record ($pkeys)" || fail "rebuilt publications $rpub != record $pkeys"
  [ "$rprobe" = "$mkeys" ] && pass "rebuilt probes ($rprobe) == distinct slots in the record ($mkeys)" || fail "rebuilt probes $rprobe != record $mkeys"
  echo "  live counts now: $(counts "$DB") (live may be ahead of the copy, and behind the record only by rows the retention pass pruned)"
fi

echo "== 5. the API matches a recomputation from the record"
if [ -f "$TMP/publications.jsonl" ] && [ -x /usr/local/bin/sentinel-recompute ]; then
  if out=$(timeout 900 /usr/local/bin/sentinel-recompute -data-dir "$TMP" -window all -api "http://$API_LISTEN" 2>&1); then
    pass "sentinel-recompute agrees with http://$API_LISTEN"
    printf '%s\n' "$out" | tail -4 | sed 's/^/       /'
  else
    fail "sentinel-recompute differs from the API (exit $?):"
    printf '%s\n' "$out" | tail -12 | sed 's/^/       /'
  fi
else
  warn "no publications.jsonl yet or sentinel-recompute missing; recompute skipped"
fi

echo
if [ "$FAILED" = 0 ]; then echo "persistence: every check passed"; else echo "persistence: FAILED"; exit 1; fi
