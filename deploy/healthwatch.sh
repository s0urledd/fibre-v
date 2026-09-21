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

# --test: post one message to the webhook and exit with curl's verdict,
# touching no state. A webhook that was pasted wrong, or a channel that
# dropped the integration, looks exactly like a healthy observer until the
# day it is not; this is how an operator proves delivery before that day.
# deploy/test/exposure.sh runs it.
if [ "${2:-}" = "--test" ]; then
  [ -n "$webhook" ] || { echo "healthwatch[$name]: ALERT_WEBHOOK is empty; nothing to test" >&2; exit 1; }
  msg="Fibre observer [$name] test: alert delivery check from $(hostname) at $(date -u +%Y-%m-%dT%H:%M:%SZ); no action needed"
  payload=$(printf '%s' "$msg" | python3 -c 'import json,sys; m=sys.stdin.read()[:1900]; print(json.dumps({"content": m, "text": m}))')
  # Only the status code is printed: curl's own error text can carry the
  # URL, and the URL is the secret.
  code=$(curl -sS -m 20 -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -d "$payload" "$webhook" 2>/dev/null) || code=000
  case "$code" in
    2*) echo "healthwatch[$name]: test message delivered (HTTP $code)"; exit 0 ;;
    *)  echo "healthwatch[$name]: webhook post failed (HTTP ${code:-000})" >&2; exit 1 ;;
  esac
fi

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
    # Read stdin once. Reading it twice in one dict literal left "text"
    # empty, because Python evaluates the values in order and the first read
    # exhausts it: Discord reads "content" and worked, Slack reads "text",
    # rejected the empty payload with a 400, and curl without --fail exited 0
    # — so the alert was never delivered and the log said nothing.
    payload=$(printf '%s' "$msg" | python3 -c 'import json,sys; m=sys.stdin.read()[:1900]; print(json.dumps({"content": m, "text": m}))' 2>/dev/null \
      || printf '{"content":"%s"}' "$msg")
    # --fail-with-body so a rejected post is an error here rather than a
    # silence. The point of this script is that somebody hears about it.
    if ! out=$(curl -sS --fail-with-body -m 20 -X POST -H 'content-type: application/json' -d "$payload" "$webhook" 2>&1); then
      echo "healthwatch: webhook post failed: $out" >&2
    fi
  fi
fi
[ "$now" = "ok" ]
