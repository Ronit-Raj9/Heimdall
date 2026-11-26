#!/usr/bin/env bash
# Back up the hosted server and copy the backup off the machine with restic
# (docs/operations.md, Off-site backups). Run daily by
# heimdall-offsite-backup.timer; it can also be run by hand as root.
#
#   deploy/hosted/ops/offsite-backup.sh [--dir /var/backups/heimdall]
#   deploy/hosted/ops/offsite-backup.sh --notify-failure
#
# Steps: deploy/hosted/backup.sh --dir DIR (database dump, project or volume
# archive, encrypted env copy); manifest-<UTC time>.json in DIR with counts
# only (organizations, users, experiments) and the size and sha256 of each
# new backup file; restic backup of DIR (tag heimdall, host heimdall-vm);
# restic forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune;
# on Sundays restic check --read-data-subset=5%.
#
# restic encrypts on this machine before anything leaves it. Its settings
# come from /etc/heimdall/restic.env ($HEIMDALL_RESTIC_ENV, root, 0600):
# RESTIC_REPOSITORY, AZURE_ACCOUNT_NAME, AZURE_ACCOUNT_SAS and
# RESTIC_PASSWORD_FILE=/etc/heimdall/restic.pass. None of them is printed.
#
# --notify-failure posts "Heimdall off-site backup failed on <host>" to the
# webhook in /etc/heimdall/alert.env (HEIMDALL_ALERT_WEBHOOK); the service
# unit runs it when a backup fails.
set -euo pipefail
# shellcheck source=../lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"

RESTIC_ENV=${HEIMDALL_RESTIC_ENV:-/etc/heimdall/restic.env}
ALERT_ENV=${HEIMDALL_ALERT_ENV:-/etc/heimdall/alert.env}
dir=${HEIMDALL_BACKUP_DIR:-/var/backups/heimdall} notify=''
while [ $# -gt 0 ]; do
	case $1 in
	--dir) dir=${2:?--dir needs a value}; shift 2 ;;
	--notify-failure) notify=1; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done

# post_alert TEXT sends TEXT to the alert webhook. The URL is passed to curl
# on standard input, never on its command line.
post_alert() {
	local hook
	[ -f "$ALERT_ENV" ] || die "no alert settings at $ALERT_ENV"
	hook=$(sed -n 's/^HEIMDALL_ALERT_WEBHOOK=//p' "$ALERT_ENV" | tail -n 1)
	[ -n "$hook" ] || die "HEIMDALL_ALERT_WEBHOOK is not set in $ALERT_ENV"
	printf 'url = "%s"\n' "$hook" | curl -fsS --max-time 10 -K - -H 'Content-Type: application/json' \
		--data "$(jq -cn --arg t "$1" '{text: $t}')" >/dev/null
}

if [ -n "$notify" ]; then
	post_alert "Heimdall off-site backup failed on $(hostname)"
	exit 0
fi

need restic jq sha256sum
[ -f "$RESTIC_ENV" ] || die "no restic settings at $RESTIC_ENV (docs/operations.md, Off-site backups)"
set -a
# shellcheck disable=SC1090
. "$RESTIC_ENV"
set +a
[ -n "${RESTIC_REPOSITORY:-}" ] || die "RESTIC_REPOSITORY is not set in $RESTIC_ENV"

mkdir -p "$dir"
marker=$(mktemp "$dir/.offsite-start.XXXXXX")
trap 'rm -f "$marker"' EXIT
"$HOSTED_DIR/backup.sh" --dir "$dir"

ts=$(date -u +%Y%m%dT%H%M%SZ)
sql() { hc exec -T postgres psql -U heimdall -d heimdall -tAc "$1" | tr -d '[:space:]'; }
orgs=$(sql 'SELECT count(*) FROM orgs')
users=$(sql 'SELECT count(*) FROM users')
# shellcheck disable=SC2016 # expanded by the container's shell
experiments=$(hc exec -T heimdall sh -c 'find /var/lib/heimdall/runs -type d -name "e_*" -prune 2>/dev/null | wc -l' | tr -d '[:space:]')

files_json=$(find "$dir" -maxdepth 1 -type f -newer "$marker" -name 'heimdall-*' | sort | while IFS= read -r f; do
	jq -cn --arg name "$(basename "$f")" --argjson size "$(stat -c %s "$f")" --arg sha256 "$(sha256sum "$f" | cut -d' ' -f1)" \
		'{name: $name, size: $size, sha256: $sha256}'
done | jq -cs .)
manifest=$dir/manifest-$ts.json
jq -n --arg ts "$ts" --argjson orgs "${orgs:-0}" --argjson users "${users:-0}" --argjson experiments "${experiments:-0}" \
	--argjson files "$files_json" '{time: $ts, orgs: $orgs, users: $users, experiments: $experiments, files: $files}' >"$manifest"
log "wrote $(basename "$manifest")"

restic backup "$dir" --tag heimdall --host heimdall-vm
restic forget --tag heimdall --host heimdall-vm --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune
if [ "$(date -u +%u)" = 7 ]; then
	restic check --read-data-subset=5%
fi
log "off-site backup done: $orgs orgs, $experiments experiments, backup of $ts"
