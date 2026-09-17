#!/usr/bin/env bash
#
# fibre-healthwatch <instance>: ask this instance's API whether the observer
# is healthy, and if not, tell somebody. Runs from fibre-healthwatch@.timer
# every five minutes as the service user, with the instance's env file.
#
# /v1/health is 200 when every process is alive and the disk has room, 503
# with the failing checks otherwise. The check is deliberately outside the
# processes it judges: a dead prober cannot report itself, and this script
# has no state to lose. It posts to ALERT_WEBHOOK (a Discord, Slack, Matrix
# or Telegram-bridge URL that accepts a JSON body with "content"), once
# when the state changes and again every ALERT_REPEAT_MIN minutes while it
# stays bad, so a broken observer nags and a fixed one says so once.
#
# With ALERT_WEBHOOK empty it only logs, which journalctl -u
# fibre-healthwatch@<instance> shows; point any external uptime monitor at
# /api/v1/health for the same signal without this script.
set -o errexit -o nounset -o pipefail

instance="${1:?instance}"
listen="${API_LISTEN:-127.0.0.1:8080}"
url="http://${listen}/v1/health"
webhook="${ALERT_WEBHOOK:-}"
repeat="${ALERT_REPEAT_MIN:-60}"
state="${DATA_DIR:-/var/lib/fibre-observer/$instance}/status/healthwatch.state"
name="${NETWORK:-$instance}"

body=$(curl -sS -m 20 -o /dev/stdout -w '\n%{http_code}' "$url" 2>/dev/null || echo -e '\n000')
code="${body##*$'\n'}"
json="${body%$'\n'*}"

if [ "$code" = "200" ]; then
  now="ok"
  summary="every observer process is alive"
elif [ "$code" = "000" ]; then
  now="down"
  summary="observer-api at $listen does not answer"
else
  now="degraded"
  summary=$(printf '%s' "$json" | python3 -c '
import json, sys
try:
    h = json.load(sys.stdin)
except Exception:
    print("unreadable health body"); sys.exit()
bad = [c for c in h.get("checks", []) if not c.get("ok")]
print(h.get("status", "?") + ": " + "; ".join(f"{c[\"name\"]}: {c[\"detail\"]}" for c in bad))
' 2>/dev/null || echo "health $code")
fi

prev_state=""; prev_at=0
if [ -r "$state" ]; then
  prev_state=$(sed -n 1p "$state"); prev_at=$(sed -n 2p "$state")
fi
epoch=$(date +%s)
echo "healthwatch[$name]: $now ($summary)"

notify=0
if [ "$now" != "$prev_state" ]; then notify=1
elif [ "$now" != "ok" ] && [ $((epoch - ${prev_at:-0})) -ge $((repeat * 60)) ]; then notify=1
fi
if [ "$notify" = 1 ]; then
  mkdir -p "$(dirname "$state")"
  printf '%s\n%s\n' "$now" "$epoch" > "$state"
  if [ -n "$webhook" ]; then
    msg="Fibre observer [$name] $now: $summary"
    if [ "$now" = "ok" ] && [ -n "$prev_state" ]; then msg="Fibre observer [$name] recovered: $summary"; fi
    payload=$(printf '%s' "$msg" | python3 -c 'import json,sys; print(json.dumps({"content": sys.stdin.read()[:1900], "text": sys.stdin.read()[:1900]}))' 2>/dev/null \
      || printf '{"content":"%s"}' "$msg")
    curl -sS -m 20 -X POST -H 'content-type: application/json' -d "$payload" "$webhook" >/dev/null || echo "healthwatch: webhook post failed" >&2
  fi
fi
[ "$now" = "ok" ]
