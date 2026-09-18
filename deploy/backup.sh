#!/usr/bin/env bash
#
# fibre-backup <instance>: copy the raw record (the JSONL files and
# state.json) off the host. Runs nightly from fibre-backup@.timer as the
# service user with the instance's env file.
#
# Litestream replicates only the derived database; the JSONL files are what
# everything is rebuilt from and they had no shipped copy until this. The
# copy goes to BACKUP_REMOTE, an rclone remote path ("r2:fibre-observer",
# "b2:bucket/path", "sftp-host:/backups"), configured once in
# /etc/fibre-observer/rclone.conf (RCLONE_CONFIG below) with any provider
# rclone supports. The files are append-only, so a nightly sync moves only
# what was added since yesterday.
#
# Never copied: the sampling master key. It is what makes the published
# sample commitments checkable and unpredictable, and a copy that leaves the
# host is a copy a publisher might read.
#
# With BACKUP_REMOTE empty the script says so and exits 0, so the timer can
# be enabled everywhere and armed by setting one variable.
set -o errexit -o nounset -o pipefail

instance="${1:?instance}"
data="${DATA_DIR:-/var/lib/fibre-observer/$instance}"
remote="${BACKUP_REMOTE:-}"
export RCLONE_CONFIG="${RCLONE_CONFIG:-/etc/fibre-observer/rclone.conf}"

if [ -z "$remote" ]; then
  echo "fibre-backup[$instance]: BACKUP_REMOTE is not set; nothing copied (litestream still covers the database if enabled)"
  exit 0
fi
command -v rclone >/dev/null || { echo "fibre-backup: rclone is not installed" >&2; exit 1; }

dest="$remote/$instance"
echo "fibre-backup[$instance]: $data -> $dest"
# copy, not sync. The record is append-only, so copy is the correct verb, and
# sync would mirror a deletion: deploy/README.md tells the operator that the
# answer to a full disk is to move the oldest JSONL files off the box, and the
# next nightly run would then delete exactly those files from the remote —
# which is the only copy, since litestream replicates the derived database and
# not the record. --max-delete 0 is belt and braces for the same reason.
rclone copy "$data" "$dest" \
  --include '*.jsonl' --include 'state.json' --include 'registry.jsonl' --include 'status/**' --include 'exports/**' \
  --exclude 'sampling-master.key' --exclude 'observer.db*' --exclude 'snapshots/**' \
  --transfers 4 --checkers 8 --stats-one-line --stats 0 --log-level NOTICE
echo "fibre-backup[$instance]: done"
