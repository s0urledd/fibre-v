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
manifest_tool="${FIBRE_BACKUP_MANIFEST:-/usr/local/bin/fibre-backup-manifest}"
[ -x "$manifest_tool" ] || manifest_tool="$(dirname "$0")/backup-manifest.py"

if [ -z "$remote" ]; then
  echo "fibre-backup[$instance]: BACKUP_REMOTE is not set; nothing copied (litestream still covers the database if enabled)"
  exit 0
fi
command -v rclone >/dev/null || { echo "fibre-backup: rclone is not installed" >&2; exit 1; }

dest="$remote/$instance"
echo "fibre-backup[$instance]: $data -> $dest"
# One consistent cut of the record before anything is copied: the byte
# length of every file (dependents before what they refer to), the SHA-256
# and record count of exactly those bytes, and the scanner's checkpoint.
# The files keep growing while rclone reads them, so the copy is at least
# the cut; deploy/test/restore.sh trims a restored copy back to the cut and
# checks every hash. Missing, short or different is a failed restore.
if [ -x "$manifest_tool" ]; then
  "$manifest_tool" write "$data" "$data/backup-manifest.json" | head -1
else
  echo "fibre-backup[$instance]: backup-manifest tool not found; copying without a manifest (restore.sh will refuse to verify this copy)" >&2
fi
# copy, not sync. The record is append-only, so copy is the correct verb, and
# sync would mirror a deletion: deploy/README.md tells the operator that the
# answer to a full disk is to move the oldest JSONL files off the box, and the
# next nightly run would then delete exactly those files from the remote —
# which is the only copy, since litestream replicates the derived database and
# not the record. --max-delete 0 is belt and braces for the same reason.
# --local-no-check-updated: the JSONL files are being appended while they
# are read, and rclone would otherwise abort with "source file is being
# updated". The copy is whatever length the file had when the transfer
# began, which is at least the manifest's cut.
rclone copy "$data" "$dest" \
  --include '*.jsonl' --include 'state.json' --include 'backup-manifest.json' --include 'status/**' --include 'exports/**' \
  --exclude 'sampling-master.key' --exclude 'observer.db*' --exclude 'snapshots/**' \
  --local-no-check-updated \
  --transfers 4 --checkers 8 --stats-one-line --stats 0 --log-level NOTICE
echo "fibre-backup[$instance]: done"
