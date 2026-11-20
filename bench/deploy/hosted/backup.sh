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
#              (HEIMDALL_S3_BUCKET from the shell, else the env file, as
#              compose reads it; and its keys; the bucket must exist) this
#              runs `bifrost-bench evidence backup` in the server container:
#              a second database dump uploaded to the bucket under
#              _backups/db/, then a mirror pass over every sealed experiment
#              (incremental, every seal checked; docs/evidence-storage.md).
#              The mirror holds sealed experiments only, so the organization
#              project folders (runs/projects: agent configs, suites,
#              experiment definitions, policies, release logs) and releases/
#              are archived to heimdall-projects-<UTC time>.tar.gz as well.
#              Without a mirror, the data volume (runs, releases) is archived
#              to heimdall-evidence-<UTC time>.tar.gz. A configured mirror
#              whose backup command is missing fails the backup instead of
#              switching to the local archive.
#   archives   are written with the server container paused (docker compose
#              pause), so no run, seal or release is half-written into them;
#              requests wait for the length of the tar and then continue.
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
paused=''
# A failed step leaves no half-written file behind and no paused server.
trap '[ -z "$paused" ] || hc unpause heimdall >/dev/null 2>&1 || true; rm -f "$dir"/heimdall-*-"$ts"*.part' EXIT

# archive_volume FILE PATH... writes a tar.gz of PATHs (relative to
# /var/lib/heimdall) from a one-off container on the same volume while the
# server is paused: a plain tar of the live volume can pair a seals.log with
# run folders from a different moment, which the seal check then rejects.
archive_volume() {
	local out=$1
	shift
	paused=1
	hc pause heimdall >/dev/null
	hc run --rm --no-deps -T --entrypoint tar heimdall -C /var/lib/heimdall -cf - "$@" | gzip -c >"$out.part"
	hc unpause heimdall >/dev/null
	paused=''
	mv "$out.part" "$out"
}

# Database: a consistent snapshot while the server keeps running.
db_file=$dir/heimdall-db-$ts.dump
log "dumping the database to $db_file"
hc exec -T postgres pg_dump -U heimdall -d heimdall --format=custom --no-owner >"$db_file.part"
mv "$db_file.part" "$db_file"
cp "$ENV_FILE" "$dir/heimdall-env-$ts"

if [ -z "$db_only" ]; then
	# Same precedence as compose: the shell's value, then the env file's.
	s3_bucket=${HEIMDALL_S3_BUCKET:-$(env_get HEIMDALL_S3_BUCKET)}
	if [ -n "$s3_bucket" ] && ! bench_has evidence; then
		# A configured mirror never falls back to a silent local-only backup.
		die "HEIMDALL_S3_BUCKET is set but this server build has no 'evidence' command; upgrade the server image or unset the bucket to archive the volume locally"
	fi
	if [ -n "$s3_bucket" ]; then
		log "evidence backup to the S3 mirror: database dump upload, then a mirror pass"
		# The dump is written to the container's tmpfs and removed once
		# uploaded; exit 4 means the database part or an experiment failed.
		# shellcheck disable=SC2016 # expanded by the container's shell
		hc exec -T heimdall sh -c 'd=$(mktemp -d) && rc=0 && bifrost-bench evidence backup -out "$d" || rc=$?; rm -rf "$d"; exit $rc'
		pr_file=$dir/heimdall-projects-$ts.tar.gz
		log "archiving the project folders (runs/projects, releases) to $pr_file, server paused"
		archive_volume "$pr_file" runs/projects releases
	else
		ev_file=$dir/heimdall-evidence-$ts.tar.gz
		log "archiving the data volume (runs, releases) to $ev_file, server paused"
		archive_volume "$ev_file" runs releases
	fi
fi

find "$dir" -maxdepth 1 -type f -name 'heimdall-*' -mtime +"$keep_days" -print -delete | sed 's/^/[heimdall] removed old backup /' >&2
log "backup done: $(du -sh "$dir" | cut -f1) in $dir"
