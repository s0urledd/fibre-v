#!/bin/sh
# vantage-pull.sh <network> — fetch the reachability record of every second
# vantage into <DATA_DIR>/vantages/<name>/reachability.jsonl, where the
# collector ingests it beside the observer's own.
#
# A second vantage is observer-heartbeat run on another host under its own
# -vantage name (deploy/README.md, "Second vantage"). It writes an
# append-only reachability.jsonl there; this script pulls only the bytes
# added since the last pull (sftp reget), once a minute from the timer.
#
# Environment (the network's env file):
#   VANTAGE_PULL_HOST   user@host of an sftp-only account on that server
#   VANTAGE_PULL_NAMES  space-separated vantage names; each is read from
#                       vantage/<name>/reachability.jsonl under that account
#   VANTAGE_PULL_KEY    ssh key (default /etc/fibre-observer/backup_ed25519)
#   VANTAGE_PULL_KNOWN  known_hosts (default /etc/fibre-observer/backup_known_hosts)
set -eu
net=${1:?usage: vantage-pull.sh <network>}
host=${VANTAGE_PULL_HOST:-}
names=${VANTAGE_PULL_NAMES:-}
if [ -z "$host" ] || [ -z "$names" ]; then
	echo "vantage-pull[$net]: VANTAGE_PULL_HOST / VANTAGE_PULL_NAMES not set; nothing to fetch"
	exit 0
fi
key=${VANTAGE_PULL_KEY:-/etc/fibre-observer/backup_ed25519}
known=${VANTAGE_PULL_KNOWN:-/etc/fibre-observer/backup_known_hosts}
data=${DATA_DIR:?DATA_DIR not set}
rc=0
for n in $names; do
	case $n in *[!a-z0-9-]*|"") echo "vantage-pull[$net]: bad vantage name '$n'" >&2; rc=1; continue ;; esac
	dir="$data/vantages/$n"
	mkdir -p "$dir"
	# reget appends what the remote file gained since the local copy's size.
	# The remote file is append-only; if it is ever reset, move the local
	# copy aside by hand so the next pull starts a fresh one.
	if ! printf 'reget vantage/%s/reachability.jsonl %s\n' "$n" "$dir/reachability.jsonl" |
		sftp -q -b - -i "$key" -o UserKnownHostsFile="$known" -o BatchMode=yes -o ConnectTimeout=20 "$host" >/dev/null; then
		echo "vantage-pull[$net]: $n: fetch failed" >&2
		rc=1
	fi
done
exit $rc
