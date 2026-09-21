#!/usr/bin/env bash
#
# resource-watch: sample the observer every few minutes for a day and say
# what grew, what lagged and what failed.
#
#   run        appends one CSV row per interval: memory of each unit, size of
#              the data directory, free disk, the health verdict and the two
#              lags it reports (scanner behind the chain, age of the newest
#              block), row counts, the 24h snapshot's compute time, how far
#              the collector's cursor is behind measurements.jsonl, the number
#              of RPC-shaped errors in the four readers' journals over the
#              interval, and the scan-gap count.
#   summarize  reads that CSV and prints first/last/max per column, growth
#              per hour for memory and per day for data, and a verdict
#              against plain thresholds. The thresholds are a first day's
#              expectations, not a spec; the numbers beside them are the
#              spec.
#
# Usage:
#   sudo nohup deploy/test/resource-watch.sh run [instance] [interval_s] [duration_s] &
#        defaults: mocha 300 86400; CSV at /var/log/fibre-resource-watch-<instance>.csv
#   deploy/test/resource-watch.sh summarize /var/log/fibre-resource-watch-mocha.csv
set -o errexit -o nounset -o pipefail

MODE="${1:-run}"

summarize() {
  python3 - "$1" <<'PY'
import csv, sys, statistics
rows = list(csv.DictReader(open(sys.argv[1])))
if len(rows) < 2:
    print("fewer than two samples"); sys.exit(1)
def num(r, k):
    try: return float(r[k])
    except Exception: return None
def col(k): return [v for v in (num(r, k) for r in rows) if v is not None]
t0, t1 = int(rows[0]["ts"]), int(rows[-1]["ts"]); hours = max((t1 - t0) / 3600, 1e-9)
print(f"{len(rows)} samples over {hours:.1f} h")
warn = []
for u in ("scan", "probe", "heartbeat", "collector", "api"):
    c = col(f"rss_{u}")
    if not c: continue
    growth = (c[-1] - c[0]) / hours
    print(f"  rss_{u:<10} first {c[0]/2**20:8.1f} MiB  last {c[-1]/2**20:8.1f} MiB  max {max(c)/2**20:8.1f} MiB  {growth/2**20:+.2f} MiB/h")
    if c[0] > 0 and c[-1] > c[0] * 1.5 and hours >= 6: warn.append(f"rss_{u} grew {c[-1]/c[0]:.1f}x over {hours:.0f} h: watch it another day before calling it a leak")
d = col("data_bytes")
if d: print(f"  data dir     first {d[0]/2**20:8.1f} MiB  last {d[-1]/2**20:8.1f} MiB  {((d[-1]-d[0])/hours*24)/2**20:+.1f} MiB/day")
f = col("disk_avail_bytes")
if f:
    print(f"  disk free    first {f[0]/2**30:8.2f} GiB  last {f[-1]/2**30:8.2f} GiB")
    if d and (d[-1]-d[0]) > 0:
        days_left = f[-1] / ((d[-1]-d[0]) / hours * 24)
        print(f"  at this growth the disk fills in {days_left:.0f} days")
        if days_left < 60: warn.append(f"disk full in {days_left:.0f} days at the observed growth")
for k, thr, unit in (("scanner_lag_blocks", 200, "blocks"), ("chain_age_s", 600, "s"), ("ingest_lag_bytes", 1 << 20, "bytes"), ("compute_ms", 5000, "ms")):
    c = col(k)
    if not c: continue
    p95 = sorted(c)[int(len(c) * 0.95) - 1] if len(c) >= 20 else max(c)
    print(f"  {k:<18} max {max(c):10.0f} {unit:<6} p95 {p95:10.0f}  last {c[-1]:10.0f}")
    if max(c) > thr: warn.append(f"{k} reached {max(c):.0f} {unit} (threshold {thr})")
e = col("rpc_errors")
if e: print(f"  rpc errors   total {sum(e):.0f} over {len(e)} intervals, max in one interval {max(e):.0f}")
g = col("scan_gaps")
if g and g[-1] > g[0]: warn.append(f"scan gaps went {g[0]:.0f} -> {g[-1]:.0f}: the node could not serve those heights")
h = [r["health"] for r in rows]
bad = sum(1 for x in h if x != "ok")
print(f"  health       ok in {len(h)-bad} of {len(h)} samples" + (f"; not ok: {sorted(set(x for x in h if x != 'ok'))}" if bad else ""))
if bad > len(h) * 0.05: warn.append(f"health not ok in {bad} of {len(h)} samples")
p = col("publications"); q = col("probes")
if p and q: print(f"  rows         publications {p[0]:.0f} -> {p[-1]:.0f}, probes {q[0]:.0f} -> {q[-1]:.0f}")
print()
if warn:
    for w in warn: print("  WARN", w)
    sys.exit(1)
print("resource-watch: nothing outside the first-day thresholds")
PY
}

if [ "$MODE" = "summarize" ]; then summarize "${2:?csv path}"; exit $?; fi
[ "$MODE" = "run" ] || { echo "usage: $0 run [instance] [interval_s] [duration_s] | summarize <csv>" >&2; exit 2; }

INSTANCE="${2:-mocha}"; INTERVAL="${3:-300}"; DURATION="${4:-86400}"
ENVFILE=/etc/fibre-observer/$INSTANCE.env
OUT="${RESOURCE_WATCH_OUT:-/var/log/fibre-resource-watch-$INSTANCE.csv}"
[ -r "$ENVFILE" ] || { echo "no $ENVFILE" >&2; exit 2; }
envval() { sed -n "s/^$1=//p" "$ENVFILE" | head -1; }
DATA_DIR=$(envval DATA_DIR); API_LISTEN=$(envval API_LISTEN)
DB="$DATA_DIR/observer.db"

rss() { v=$(systemctl show -p MemoryCurrent --value "$1@$INSTANCE" 2>/dev/null || true); case "$v" in ''|'[not set]'|18446744073709551615) echo 0 ;; *) echo "$v" ;; esac; }
health_fields() { # -> "status scanner_lag chain_age gaps"
  curl -sS -m 10 "http://$API_LISTEN/v1/health" 2>/dev/null | python3 -c '
import json, re, sys
try: h = json.load(sys.stdin)
except Exception: print("down 0 0 0"); sys.exit()
lag = age = 0
def secs(s):
    t = 0
    for n, u in re.findall(r"(\d+(?:\.\d+)?)([hms])", s):
        t += float(n) * {"h": 3600, "m": 60, "s": 1}[u]
    return int(t)
for c in h.get("checks", []):
    if c["name"] == "scanner_lag":
        m = re.search(r"\((\d+) blocks behind\)", c["detail"]); lag = int(m.group(1)) if m else 0
    if c["name"] == "chain_liveness":
        m = re.search(r"newest block (\S+) old", c["detail"]); age = secs(m.group(1)) if m else 0
print(h.get("status", "?"), lag, age, len(h.get("scan_gaps") or []))
' 2>/dev/null || echo "down 0 0 0"
}
meta_counts() { curl -sS -m 10 "http://$API_LISTEN/v1/meta" 2>/dev/null | python3 -c 'import json,sys; c=json.load(sys.stdin)["counts"]; print(c["Publications"], c["Probes"])' 2>/dev/null || echo "0 0"; }
compute_ms() { curl -sS -m 30 "http://$API_LISTEN/v1/network?window=24h" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("compute_ms", 0))' 2>/dev/null || echo 0; }
ingest_lag() {
  python3 - "$DB" "$DATA_DIR/measurements.jsonl" <<'PY' 2>/dev/null || echo 0
import os, sqlite3, sys
try:
    size = os.path.getsize(sys.argv[2])
except OSError:
    print(0); sys.exit()
con = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
row = con.execute("SELECT byte_offset FROM ingest_cursors WHERE file LIKE '%measurements.jsonl'").fetchone()
print(max(size - (row[0] if row else 0), 0))
PY
}
rpc_errors() {
  journalctl -u "fibre-scan@$INSTANCE" -u "fibre-collector@$INSTANCE" -u "fibre-probe@$INSTANCE" -u "fibre-heartbeat@$INSTANCE" \
    --since "-${INTERVAL} seconds" --no-pager 2>/dev/null | grep -c -i -E 'rpc|timeout|timed out|connection refused|reset by peer|EOF|503|429' || true
}

if [ ! -f "$OUT" ]; then
  echo "ts,health,rss_scan,rss_probe,rss_heartbeat,rss_collector,rss_api,data_bytes,disk_avail_bytes,scanner_lag_blocks,chain_age_s,scan_gaps,publications,probes,compute_ms,ingest_lag_bytes,rpc_errors" > "$OUT"
fi
echo "resource-watch: sampling every ${INTERVAL}s for ${DURATION}s into $OUT"
end=$(( $(date +%s) + DURATION ))
while [ "$(date +%s)" -lt "$end" ]; do
  read -r status lag age gaps <<<"$(health_fields)"
  read -r pubs probes <<<"$(meta_counts)"
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$(date +%s)" "$status" "$(rss fibre-scan)" "$(rss fibre-probe)" "$(rss fibre-heartbeat)" "$(rss fibre-collector)" "$(rss fibre-api)" \
    "$(du -sb "$DATA_DIR" 2>/dev/null | cut -f1)" "$(df -B1 --output=avail "$DATA_DIR" 2>/dev/null | tail -1 | tr -d ' ')" \
    "$lag" "$age" "$gaps" "$pubs" "$probes" "$(compute_ms)" "$(ingest_lag)" "$(rpc_errors)" >> "$OUT"
  sleep "$INTERVAL"
done
echo "resource-watch: done; summarize with: $0 summarize $OUT"
