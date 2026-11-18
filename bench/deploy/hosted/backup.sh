#!/usr/bin/env bash
# Back up a hosted Heimdall (docs/hosting.md, Backups):
#
#   deploy/hosted/backup.sh [--dir /var/backups/heimdall] [--db-only] [--keep-days 14]
#
#   --dir defaults to $HEIMDALL_BACKUP_DIR, else /var/backups/heimdall.
#
#   database   pg_dump in custom format (restore with pg_restore), from the
#              running Postgres container: heimdall-db-<UTC time>.dump
#   evidence   the sealed run folders. With the S3 mirror configured
#              (HEIMDALL_S3_BUCKET and its keys; the bucket must exist) this
#              runs `bifrost-bench evidence backup` in the server container:
#              a second database dump uploaded to the bucket under
#              _backups/db/, then a mirror pass over every sealed experiment
#              (incremental, every seal checked; docs/evidence-storage.md).
#              Without a mirror, the data volume is archived to
#              heimdall-evidence-<UTC time>.tar.gz
#   env file   a copy of deploy/hosted/.env (it holds the database password
#              and the gateway secrets; the backup folder is chmod 700)
#
# Older backups than --keep-days (default 14) are deleted. Copy the folder
# off the machine (rsync, restic, the provider's snapshots): a backup on the
# same disk does not survive the disk.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

dir=${HEIMDALL_BACKUP_DIR:-/var/backups/heimdall} db_only='' keep_days=14
while [ $# -gt 0 ]; do
	case $1 in
	--dir) dir=${2:?--dir needs a value}; shift 2 ;;
	--db-only) db_only=1; shift ;;
	--keep-days) keep_days=${2:?--keep-days needs a value}; shift 2 ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done
case $keep_days in '' | *[!0-9]*) die "--keep-days must be a whole number" ;; esac

need docker gzip
require_configured
umask 077
mkdir -p "$dir"
chmod 700 "$dir"
ts=$(date -u +%Y%m%dT%H%M%SZ)
# A failed step leaves no half-written file behind.
trap 'rm -f "$dir"/heimdall-*-"$ts"*.part' EXIT

# Database: a consistent snapshot while the server keeps running.
db_file=$dir/heimdall-db-$ts.dump
log "dumping the database to $db_file"
hc exec -T postgres pg_dump -U heimdall -d heimdall --format=custom --no-owner >"$db_file.part"
mv "$db_file.part" "$db_file"
cp "$ENV_FILE" "$dir/heimdall-env-$ts"

if [ -z "$db_only" ]; then
	if [ -n "$(env_get HEIMDALL_S3_BUCKET)" ] && bench_has evidence; then
		log "evidence backup to the S3 mirror: database dump upload, then a mirror pass"
		# The dump is written to the container's tmpfs and removed once
		# uploaded; exit 4 means the database part or an experiment failed.
		# shellcheck disable=SC2016 # expanded by the container's shell
		hc exec -T heimdall sh -c 'd=$(mktemp -d) && rc=0 && bifrost-bench evidence backup -out "$d" || rc=$?; rm -rf "$d"; exit $rc'
	else
		ev_file=$dir/heimdall-evidence-$ts.tar.gz
		log "archiving the data volume (runs, releases) to $ev_file"
		# tar reads the live volume; sealed run folders do not change after
		# sealing, and a run still being written is re-uploaded by its runner.
		hc exec -T heimdall tar -C /var/lib/heimdall -cf - runs releases | gzip -c >"$ev_file.part"
		mv "$ev_file.part" "$ev_file"
	fi
fi

find "$dir" -maxdepth 1 -type f -name 'heimdall-*' -mtime +"$keep_days" -print -delete | sed 's/^/[heimdall] removed old backup /' >&2
log "backup done: $(du -sh "$dir" | cut -f1) in $dir"
