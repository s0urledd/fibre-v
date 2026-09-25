#!/usr/bin/env bash
#
# vantage-sync: deploy/vantage-pull.sh against a fake sftp on local files.
# Needs bash and coreutils; no network, no root. The fake speaks the four
# batch commands the script sends (reget, reput, put, chmod) with OpenSSH's
# semantics where they matter here: a command fails the batch unless it is
# prefixed with '-'; reget appends what the remote file has beyond the local
# copy; reput appends what the local file has beyond the remote one and
# refuses when the remote is missing or not shorter; put replaces.
#
#   push     no requests file: nothing sent; the first push creates the
#            inbox copy whole, group-readable; a push with nothing new sends
#            nothing; new lines are appended, not re-sent; a reset local file
#            is sent whole
#   pull     reachability and measurements are appended, not re-fetched; a
#            vantage with no measurements.jsonl yet is not a failure; a
#            vantage whose reachability.jsonl is missing is
#   failure  a push that fails is retried whole on the next run and the
#            script says so
set -u
cd "$(dirname "$0")"
. ./lib.sh

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
remote="$tmp/remote"
data="$tmp/data"
log="$tmp/sftp.log"
mkdir -p "$remote/vantage/de-1/inbox" "$data"

cat >"$tmp/sftp" <<'EOF'
#!/usr/bin/env bash
# fake sftp: batch on stdin, files under $FAKE_REMOTE
[ -n "${FAKE_SFTP_DOWN:-}" ] && exit 255
sz() { if [ -f "$1" ]; then wc -c <"$1" | tr -d ' '; else echo -1; fi; }
while read -r cmd a b c; do
  soft=0
  case $cmd in -*) soft=1; cmd=${cmd#-} ;; esac
  ok=1
  case $cmd in
    reget) r="$FAKE_REMOTE/$a"; l=$b; rs=$(sz "$r"); ls=$(sz "$l"); [ "$ls" -lt 0 ] && ls=0
      if [ "$rs" -lt 0 ] || [ "$ls" -gt "$rs" ]; then ok=0
      else tail -c +$((ls + 1)) "$r" >>"$l"; echo "reget $a $((rs - ls))" >>"$FAKE_LOG"; fi ;;
    reput) l=$a; r="$FAKE_REMOTE/$b"; rs=$(sz "$r"); ls=$(sz "$l")
      if [ "$rs" -lt 0 ] || [ "$rs" -ge "$ls" ]; then ok=0
      else tail -c +$((rs + 1)) "$l" >>"$r"; echo "reput $b $((ls - rs))" >>"$FAKE_LOG"; fi ;;
    put) r="$FAKE_REMOTE/$b"
      if [ -d "$(dirname "$r")" ]; then cp "$a" "$r"; echo "put $b $(sz "$a")" >>"$FAKE_LOG"; else ok=0; fi ;;
    chmod) chmod "$a" "$FAKE_REMOTE/$b" || ok=0 ;;
    *) ok=0 ;;
  esac
  if [ "$ok" = 0 ] && [ "$soft" = 0 ]; then exit 1; fi
done
exit 0
EOF
chmod +x "$tmp/sftp"

run() {
  FAKE_REMOTE="$remote" FAKE_LOG="$log" SFTP="$tmp/sftp" DATA_DIR="$data" \
    VANTAGE_PULL_HOST=tensile-backup@example VANTAGE_PULL_NAMES=de-1 \
    VANTAGE_PULL_KEY=/dev/null VANTAGE_PULL_KNOWN=/dev/null \
    sh ../vantage-pull.sh mocha "$@" 2>"$tmp/stderr"
}
logged() { grep -c "$1" "$log" 2>/dev/null || true; }
same() { cmp -s "$1" "$2"; }

req="$data/vantage-requests.jsonl"
inbox="$remote/vantage/de-1/inbox/requests.jsonl"
rreach="$remote/vantage/de-1/reachability.jsonl"
rmeas="$remote/vantage/de-1/measurements.jsonl"
lreach="$data/vantages/de-1/reachability.jsonl"
lmeas="$data/vantages/de-1/measurements.jsonl"

echo "vantage-sync"
echo '{"beat":1}' >"$rreach"

# no requests yet, no measurements yet: the pull alone, and it succeeds
if run; then pass "no requests file and no measurements.jsonl yet: exit 0"; else fail "exit $? with nothing to push and no measurements yet ($(cat "$tmp/stderr"))"; fi
[ ! -e "$inbox" ] && pass "nothing pushed without a requests file" || fail "an inbox file appeared without requests"
same "$rreach" "$lreach" && pass "reachability pulled" || fail "reachability not pulled"
[ ! -e "$lmeas" ] && pass "no local measurements.jsonl while the vantage has none" || fail "an empty measurements.jsonl was created"

# first request: the whole file goes up with put, group-readable
echo '{"promise_hash":"a"}' >"$req"
: >"$log"
run || fail "first push: exit $? ($(cat "$tmp/stderr"))"
same "$req" "$inbox" && pass "first push copies the requests" || fail "first push: inbox differs"
[ "$(logged '^put ')" = 1 ] && pass "first push is a put (the inbox file did not exist)" || fail "first push: $(cat "$log")"
mode=$(stat -c %a "$inbox" 2>/dev/null || stat -f %Lp "$inbox")
[ "$mode" = 640 ] && pass "inbox file is 0640 (group-readable for the confirm service)" || fail "inbox file mode $mode"

# nothing new: nothing sent
: >"$log"
run || fail "idle push: exit $?"
[ "$(logged 'reput\|^put ')" = 0 ] && pass "nothing new, nothing sent" || fail "idle run sent: $(cat "$log")"

# a second request: only its bytes go up
echo '{"promise_hash":"b"}' >>"$req"
: >"$log"
run || fail "append push: exit $?"
same "$req" "$inbox" && pass "second request reaches the inbox" || fail "second push: inbox differs"
n=$(wc -c <<<'{"promise_hash":"b"}' | tr -d ' ')
grep -q "^reput vantage/de-1/inbox/requests.jsonl $n\$" "$log" && pass "only the new line is sent (reput, $n bytes)" || fail "append push: $(cat "$log")"

# the vantage answers, and more heartbeats arrive: only new bytes come back
echo '{"m":1}' >"$rmeas"
echo '{"beat":2}' >>"$rreach"
: >"$log"
run || fail "pull: exit $?"
same "$rmeas" "$lmeas" && pass "measurements pulled" || fail "measurements not pulled"
same "$rreach" "$lreach" && pass "reachability appended" || fail "reachability differs"
grep -q "^reget vantage/de-1/reachability.jsonl 11\$" "$log" && pass "only the new heartbeat is fetched" || fail "pull: $(cat "$log")"
echo '{"m":2}' >>"$rmeas"
run || fail "second pull: exit $?"
same "$rmeas" "$lmeas" && [ "$(wc -l <"$lmeas")" = 2 ] && pass "measurements appended, no line twice" || fail "measurements: $(cat "$lmeas")"

# the local requests file is reset (a restore): sent whole, replacing the inbox copy
echo '{"promise_hash":"z"}' >"$req"
: >"$log"
run || fail "reset push: exit $?"
same "$req" "$inbox" && pass "a reset requests file is sent whole" || fail "reset push: inbox differs"

# the server is down: the push fails, is reported, and is retried next run
echo '{"promise_hash":"y"}' >>"$req"
if FAKE_SFTP_DOWN=1 run; then fail "a failed push exited 0"; else pass "a failed exchange exits non-zero"; fi
grep -q "request push failed" "$tmp/stderr" && pass "the failed push is named" || fail "stderr: $(cat "$tmp/stderr")"
run || fail "retry: exit $?"
same "$req" "$inbox" && pass "the push is retried on the next run" || fail "retry: inbox differs"

# reachability.jsonl missing on the vantage is a failure
rm "$rreach"
if run; then fail "a missing reachability.jsonl passed"; else pass "a missing reachability.jsonl fails the fetch"; fi

# the push can be turned off
echo '{"promise_hash":"x"}' >>"$req"
: >"$log"
echo '{"beat":3}' >"$rreach"; rm -f "$lreach"
VANTAGE_PUSH=0 run || true
[ "$(logged 'reput\|^put ')" = 0 ] && pass "VANTAGE_PUSH=0 sends nothing" || fail "VANTAGE_PUSH=0 sent: $(cat "$log")"

[ "$FAILED" = 0 ] && echo "vantage-sync: all passed" || { echo "vantage-sync: FAILED"; exit 1; }
