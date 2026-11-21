#!/usr/bin/env bash
# Back up a hosted Heimdall (docs/hosting.md, Backups):
#
#   deploy/hosted/backup.sh [--dir /var/backups/heimdall] [--db-only] [--keep-days 14] [--env-dir DIR]
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
#   env file   deploy/hosted/.env holds HEIMDALL_MASTER_KEY (it decrypts every
#              stored provider key) and the database password, so it is never
#              copied next to the database dump in plain text. With
#              HEIMDALL_BACKUP_PASSPHRASE_FILE set (a file holding a
#              passphrase, kept elsewhere) an encrypted copy is written:
#              heimdall-env-<UTC time>.enc (openssl enc -aes-256-cbc -pbkdf2;
#              decrypt with openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000
#              -pass file:<passphrase file> -in <file>). With
#              --env-dir DIR a plain copy goes to DIR, which must be another
#              folder (another disk or a secrets vault mount). With neither,
#              the env file is left out with a warning: keep it, or at least
#              HEIMDALL_MASTER_KEY, in your secrets manager.
#
# Older backups than --keep-days (default 14) are deleted. Copy the folder
# off the machine (rsync, restic, the provider's snapshots): a backup on the
# same disk does not survive the disk.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

dir=${HEIMDALL_BACKUP_DIR:-/var/backups/heimdall} db_only='' keep_days=14 env_dir=''
passphrase_file=${HEIMDALL_BACKUP_PASSPHRASE_FILE:-$(env_get HEIMDALL_BACKUP_PASSPHRASE_FILE)} # the environment, else deploy/hosted/.env
while [ $# -gt 0 ]; do
	case $1 in
	--dir) dir=${2:?--dir needs a value}; shift 2 ;;
	--db-only) db_only=1; shift ;;
	--keep-days) keep_days=${2:?--keep-days needs a value}; shift 2 ;;
	--env-dir) env_dir=${2:?--env-dir needs a value}; shift 2 ;;
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
if [ -n "$env_dir" ]; then
	mkdir -p "$env_dir"
	chmod 700 "$env_dir"
	[ "$(cd "$env_dir" && pwd -P)" != "$(cd "$dir" && pwd -P)" ] || die "--env-dir must be another folder than the backup folder: the env file holds the master key"
fi
if [ -n "$passphrase_file" ]; then
	[ -s "$passphrase_file" ] || die "HEIMDALL_BACKUP_PASSPHRASE_FILE $passphrase_file is missing or empty"
	need openssl
fi
ts=$(date -u +%Y%m%dT%H%M%SZ)
# One backup at a time per folder: two overlapping runs would each pause and
# unpause the server under the other's archive.
if command -v flock >/dev/null 2>&1; then
	exec 9>"$dir/.backup.lock"
	flock -n 9 || die "another backup is running on $dir"
fi
paused=''
# A failed step leaves no half-written file behind, and a server this run
# paused is unpaused again. `paused` is set only once our pause succeeded,
# so a refused pause, or a server some operator had already paused, is never
# unpaused by the backup.
trap '[ -z "$paused" ] || hc unpause heimdall >/dev/null 2>&1 || true; rm -f "$dir"/heimdall-*-"$ts"*.part' EXIT

# seal_check is run in a one-off container while the server is paused. A
# pause can freeze the server between writing a run's seal.json and
# appending it to the experiment's seals.log; such a half-published seal
# would make the restored chain fail verification. It exits 75 when a sealed
# run is missing from its seals.log, and archive_volume then lets the server
# finish the seal and tries again.
# shellcheck disable=SC2016 # expanded by the container's shell
seal_check='cd /var/lib/heimdall && [ ! -d runs ] || find runs -path "*/slots/*/runs/*/seal.json" | while IFS= read -r f; do r=${f%/seal.json}; id=${r##*/}; e=${r%/runs/*}; e=${e%/slots/*}; grep -q "^$id " "$e/seals.log" 2>/dev/null || { echo "half-published seal: $f" >&2; exit 75; }; done'
# A root that does not exist yet (a fresh install with no organization has
# no runs/projects) is archived as an empty folder instead of failing tar.
# shellcheck disable=SC2016 # expanded by the container's shell
tar_script='cd /var/lib/heimdall && for p in "$@"; do [ -e "$p" ] || mkdir -p "$p"; done && tar -cf - "$@"'

# archive_volume FILE PATH... writes a tar.gz of PATHs (relative to
# /var/lib/heimdall) from a one-off container on the same volume while the
# server is paused: a plain tar of the live volume can pair a seals.log with
# run folders from a different moment, which the seal check then rejects.
archive_volume() {
	local out=$1 tries=0 rc
	shift
	if [ -n "$(hc ps -q --status paused heimdall 2>/dev/null)" ]; then
		die "the heimdall server is already paused (maintenance?); unpause it before the backup, which pauses it itself"
	fi
	while :; do
		hc pause heimdall >/dev/null
		paused=1
		rc=0
		hc run --rm --no-deps -T --entrypoint sh heimdall -c "$seal_check" >&2 || rc=$?
		[ "$rc" -eq 75 ] || break
		hc unpause heimdall >/dev/null
		paused=''
		tries=$((tries + 1))
		[ "$tries" -lt 10 ] || die "a seal stayed half-published across $tries pauses; not archiving an inconsistent seal chain"
		sleep "${HEIMDALL_BACKUP_RETRY_SLEEP:-1}"
	done
	[ "$rc" -eq 0 ] || die "seal check failed (exit $rc)"
	hc run --rm --no-deps -T --entrypoint sh heimdall -c "$tar_script" sh "$@" | gzip -c >"$out.part"
	hc unpause heimdall >/dev/null
	paused=''
	mv "$out.part" "$out"
}

# Database: a consistent snapshot while the server keeps running.
db_file=$dir/heimdall-db-$ts.dump
log "dumping the database to $db_file"
hc exec -T postgres pg_dump -U heimdall -d heimdall --format=custom --no-owner >"$db_file.part"
mv "$db_file.part" "$db_file"
# The env file holds the master key: never a plain copy beside the dump.
if [ -n "$passphrase_file" ]; then
	openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass "file:$passphrase_file" -in "$ENV_FILE" -out "$dir/heimdall-env-$ts.enc.part"
	mv "$dir/heimdall-env-$ts.enc.part" "$dir/heimdall-env-$ts.enc"
	log "env file encrypted to $dir/heimdall-env-$ts.enc"
fi
if [ -n "$env_dir" ]; then
	cp "$ENV_FILE" "$env_dir/heimdall-env-$ts"
	chmod 600 "$env_dir/heimdall-env-$ts"
	log "env file copied to $env_dir (kept apart from the database dump)"
fi
if [ -z "$passphrase_file" ] && [ -z "$env_dir" ]; then
	log "warning: $ENV_FILE (HEIMDALL_MASTER_KEY, database password) is not in this backup. Keep it in your secrets manager, or set HEIMDALL_BACKUP_PASSPHRASE_FILE for an encrypted copy or pass --env-dir for a copy in another folder. Without the master key, stored provider keys cannot be decrypted after a restore."
fi

mirror_rc=0
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
		# A failed mirror pass (one refused experiment is enough) must not
		# skip the project archive below, which the mirror never holds: the
		# failure is remembered and returned once the archive is written.
		hc exec -T heimdall sh -c 'd=$(mktemp -d) && rc=0 && bifrost-bench evidence backup -out "$d" || rc=$?; rm -rf "$d"; exit $rc' || mirror_rc=$?
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
if [ "$mirror_rc" -ne 0 ]; then
	die "backup incomplete: the S3 evidence backup failed (exit $mirror_rc); the database dump and project archive in $dir were written"
fi
log "backup done: $(du -sh "$dir" | cut -f1) in $dir"
