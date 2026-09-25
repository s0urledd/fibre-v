#!/bin/sh
# vantage-pull.sh <network> — exchange records with every second vantage.
#
# A second vantage is observer-heartbeat run on another host under its own
# -vantage name (deploy/README.md, "Second vantage"), and, once it confirms
# faults, sentinel-probe -confirm-requests beside it. Once a minute, from the
# timer, for each vantage:
#
#   push  <DATA_DIR>/vantage-requests.jsonl, the prober's confirmation
#         requests (one per FAULT), to vantage/<name>/inbox/requests.jsonl,
#         sending only the bytes added since the last push (sftp reput);
#   pull  vantage/<name>/reachability.jsonl and vantage/<name>/measurements.jsonl
#         into <DATA_DIR>/vantages/<name>/, fetching only the bytes added
#         since the last pull (sftp reget), where the collector ingests them.
#
# Every file on both sides is append-only. A fresh full copy is always a
# correct state too: the collector and the confirmer read by key, so a line
# they have seen twice is a no-op.
#
# Environment (the network's env file):
#   VANTAGE_PULL_HOST   user@host of an sftp-only account on that server
#   VANTAGE_PULL_NAMES  space-separated vantage names; each is read from
#                       vantage/<name>/ under that account
#   VANTAGE_PULL_KEY    ssh key (default /etc/fibre-observer/backup_ed25519)
#   VANTAGE_PULL_KNOWN  known_hosts (default /etc/fibre-observer/backup_known_hosts)
#   VANTAGE_PUSH        0 turns the request push off (default on)
#   SFTP                the sftp binary (default sftp; the tests put a fake here)
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
push=${VANTAGE_PUSH:-1}
sftp_bin=${SFTP:-sftp}

session() {
	"$sftp_bin" -q -b - -i "$key" -o UserKnownHostsFile="$known" -o BatchMode=yes -o ConnectTimeout=20 "$host" >/dev/null
}

# size <file>: bytes, 0 when missing
size() {
	if [ -f "$1" ]; then wc -c <"$1" | tr -d ' '; else echo 0; fi
}

rc=0
reqs="$data/vantage-requests.jsonl"
for n in $names; do
	case $n in *[!a-z0-9-]*|"") echo "vantage-pull[$net]: bad vantage name '$n'" >&2; rc=1; continue ;; esac
	dir="$data/vantages/$n"
	mkdir -p "$dir"

	# ---- push the confirmation requests ----
	# pushed holds how many bytes of the requests file the vantage has. Only
	# a file that grew is sent: reput appends what the remote copy lacks, and
	# refuses when the remote file does not exist yet, or is not shorter than
	# the local one, in which case a whole put replaces it (a fresh copy is a
	# correct state). The mode is set on every push so the confirm service,
	# a member of the inbox's group, can read what the sftp account wrote.
	if [ "$push" != 0 ] && [ -f "$reqs" ]; then
		have=$(size "$reqs")
		pushed=$(cat "$dir/requests.pushed" 2>/dev/null || echo 0)
		case $pushed in ''|*[!0-9]*) pushed=0 ;; esac
		if [ "$have" -lt "$pushed" ]; then
			pushed=0 # the local file was reset: send it whole
		fi
		if [ "$have" -gt "$pushed" ]; then
			remote="vantage/$n/inbox/requests.jsonl"
			if { [ "$pushed" -gt 0 ] && printf 'reput %s %s\nchmod 640 %s\n' "$reqs" "$remote" "$remote" | session; } ||
				printf 'put %s %s\nchmod 640 %s\n' "$reqs" "$remote" "$remote" | session; then
				echo "$have" >"$dir/requests.pushed.tmp" && mv "$dir/requests.pushed.tmp" "$dir/requests.pushed"
			else
				echo "vantage-pull[$net]: $n: request push failed" >&2
				rc=1
			fi
		fi
	fi

	# ---- pull its records ----
	# reget appends what the remote file gained since the local copy's size.
	# The remote files are append-only; if one is ever reset, move the local
	# copy aside by hand so the next pull starts a fresh one. measurements.jsonl
	# exists only once the vantage has answered a request, so its fetch may
	# fail (the leading '-' lets the batch go on); reachability.jsonl must be
	# there.
	if ! printf 'reget vantage/%s/reachability.jsonl %s\n-reget vantage/%s/measurements.jsonl %s\n' \
		"$n" "$dir/reachability.jsonl" "$n" "$dir/measurements.jsonl" | session; then
		echo "vantage-pull[$net]: $n: fetch failed" >&2
		rc=1
	fi
done
exit $rc
