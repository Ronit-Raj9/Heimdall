#!/usr/bin/env bash
# Remove the objects the pass 6 testers left on the hosted server
# (pitch/video/pass6/LEFTOVERS.md; docs/operations.md, Test leftovers).
#
#   deploy/hosted/ops/cleanup-pass6.sh                 dry run: list everything
#   deploy/hosted/ops/cleanup-pass6.sh --apply --i-reviewed-the-list
#
# The dry run (the default) prints every file it would move and every
# database row it would delete, grouped by tester and kind, with the paths
# and the SQL. Nothing changes without --apply.
#
# --apply also needs --i-reviewed-the-list, an interactive terminal, and a
# database dump younger than 2 hours in the backup folder ($HEIMDALL_BACKUP_DIR,
# else /var/backups/heimdall). Files are moved, never deleted, into
# <data>/quarantine/pass6-<UTC time>/ keeping their paths, so moving them back
# undoes the step. Revoked test access tokens and the unnamed ingest token of
# the test organization are deleted from the database.
#
# Steps the script does not do (YAML it does not edit, an organization delete)
# are printed at the end as manual steps.
#
# Test hooks: HEIMDALL_OPS_ROOT is the data folder (default /var/lib/heimdall,
# inside the heimdall container); HEIMDALL_OPS_EXEC=direct runs the file steps
# on the host paths without Docker and skips the database steps.
set -euo pipefail
# shellcheck source=../lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"

ROOT=${HEIMDALL_OPS_ROOT:-/var/lib/heimdall}
EXEC=${HEIMDALL_OPS_EXEC:-docker}
BACKUP_DIR=${HEIMDALL_BACKUP_DIR:-/var/backups/heimdall}
TEST_ORG=org_01m8ccxwhwqbda3gsg93nmb8qy

apply='' reviewed=''
while [ $# -gt 0 ]; do
	case $1 in
	--apply) apply=1; shift ;;
	--i-reviewed-the-list) reviewed=1; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done

if [ -n "$apply" ]; then
	[ -n "$reviewed" ] || die "--apply needs --i-reviewed-the-list: run the dry run first and read the whole list"
	[ -t 0 ] || die "--apply needs an interactive terminal; it never runs from a script or a scheduled job"
	if [ -z "$(find "$BACKUP_DIR" -maxdepth 1 -name 'heimdall-db-*.dump' -mmin -120 2>/dev/null | head -n 1)" ]; then
		die "Take a backup first: deploy/hosted/backup.sh"
	fi
fi

# The leftovers: tester | kind | file name pattern (find -name). Each pattern
# matches a file or folder by its exact name; agents also match their YAML.
LEFTOVERS='p01|agent|zz-test-p01-agent
p01|agent|zz-test-p01-agent.yaml
p02|agent|zz-test-p02-ret
p02|agent|zz-test-p02-ret.yaml
p02|agent|zz-test-p02-con
p02|agent|zz-test-p02-con.yaml
p02|agent|zz-test-p02-con2
p02|agent|zz-test-p02-con2.yaml
p03|benchmark|zz-test-p03-own
p03|benchmark|zz-test-p03-judge
p03|benchmark|zz-test-p03-judge2
p03|experiment|e_admit_zz_test_p03_own_*
p05|experiment|e_zz_test_p05_cmp_*
p05|experiment|e_zz_test_p05_cmp2_*
p05|experiment|e_zz_test_p05_cmp3_*
p10|benchmark|zz-test-p10-*
p10|draft|ag_28d214e45e37
p10|draft|ag_28d214e45e37.*
p11|benchmark|zz-test-p11-qa
p11|benchmark|zz-test-p11-repo
p11|benchmark|zz-test-p11-empty
p11|benchmark|zz-test-p11-sup
p13|benchmark|zz-test-p13-qa
p13|experiment|e_canary_zz_test_p13_drift1_*
p13|experiment|e_canary_zz_test_p13_drift2_*'

# Single files by their path inside an organization folder.
FILES='p09|file|uploads/datasets/import-msibench/zz-test-p09-bad.csv
p13|file|policies/monitors/zz-test-p13-keyword.yaml'

INBOX_IDS='inc-2dcade20a283 inc-8a88f6335cf2 inc-609d65107c1a inc-42e35e4fead0 inc-ff081519e552 inc-f21146aef1b5 inc-8d7583e3be2f'

# in_data runs a shell snippet where the data folder is: on the host
# (direct) or inside the heimdall container. The snippet gets ROOT as $1.
in_data() {
	local snippet=$1
	shift
	if [ "$EXEC" = direct ]; then
		sh -c "$snippet" sh "$ROOT" "$@"
	else
		hc exec -T heimdall sh -c "$snippet" sh "$ROOT" "$@"
	fi
}

# tag WHO KIND ORG prefixes each input line with "WHO|KIND|ORG|".
tag() { awk -v p="$1|$2|$3|" '{ print p $0 }'; }

# The organization folders. The design names <data>/projects/<org> and
# <data>/orgs/<org>; the hosted runs root is <data>/runs, so both layouts
# are searched.
# shellcheck disable=SC2016 # expanded by the snippet's shell
list_orgs='for b in "$1/projects" "$1/orgs" "$1/runs/projects" "$1/runs/orgs"; do [ -d "$b" ] || continue; for d in "$b"/*; do [ -d "$d" ] && echo "$d"; done; done'

orgs=$(in_data "$list_orgs")

found=$(mktemp)
mentions=$(mktemp)
trap 'rm -f "$found" "$mentions"' EXIT

for org in $orgs; do
	while IFS='|' read -r who kind pat; do
		# shellcheck disable=SC2016
		in_data 'find "$2" -mindepth 1 -name "$3" -prune -print' "$org" "$pat" 2>/dev/null |
			tag "$who" "$kind" "$org" >>"$found" || true
	done <<<"$LEFTOVERS"
	while IFS='|' read -r who kind rel; do
		# shellcheck disable=SC2016
		in_data '[ -e "$2/$3" ] && echo "$2/$3"' "$org" "$rel" | tag "$who" "$kind" "$org" >>"$found" || true
	done <<<"$FILES"
	# Live scoring settings: only the file the tester made (its keys are
	# named zz-test-p13-vk-...); another organization's own settings stay.
	# shellcheck disable=SC2016
	in_data 'f="$2/livescore/config.yaml"; [ -f "$f" ] && grep -q "zz-test-p13-vk" "$f" && echo "$f"' "$org" |
		tag p13 file "$org" >>"$found" || true
	for id in $INBOX_IDS; do
		# Files and folders named after the item are moved; other files that
		# only mention it (an index shared with real items) are listed for a
		# person to check and are never moved.
		# shellcheck disable=SC2016
		in_data '[ -d "$2/inbox" ] && find "$2/inbox" -mindepth 1 -name "*$3*" -prune -print' "$org" "$id" |
			tag p13 "inbox item" "$org" >>"$found" || true
		# shellcheck disable=SC2016
		in_data '[ -d "$2/inbox" ] && grep -rl -- "$3" "$2/inbox"' "$org" "$id" 2>/dev/null |
			while IFS= read -r f; do
				case $f in *"$id"*) ;; *) echo "p13|$id|$f" ;; esac
			done >>"$mentions" || true
	done
done

# Drop paths inside a path already listed (a folder covers its files).
sorted=$(awk -F'|' '{ print $4 "|" $0 }' "$found" | LC_ALL=C sort -t'|' -k1,1 -u | cut -d'|' -f2-)
paths=''
keep=$(mktemp)
while IFS='|' read -r who kind org path; do
	[ -n "$path" ] || continue
	inside=''
	for p in $paths; do
		case $path in "$p"/*) inside=1 ;; esac
	done
	[ -n "$inside" ] && continue
	paths="$paths $path"
	printf '%s|%s|%s\n' "$who" "$kind" "$path" >>"$keep"
done <<<"$sorted"

echo "Files to move into $ROOT/quarantine/pass6-<UTC time>/ (paths kept):"
if [ -s "$keep" ]; then
	sort -t'|' -k1,1 -k2,2 -k3,3 "$keep" | awk -F'|' '{ if ($1 " " $2 != last) { print "  " $1 " " $2 ":"; last = $1 " " $2 } print "    " $3 }'
else
	echo "  none found"
fi
if [ -s "$mentions" ]; then
	echo "Files that mention a test inbox item but hold other data too (not moved; check by hand):"
	sort -u "$mentions" | awk -F'|' '{ print "    " $3 " (" $2 ")" }'
fi

SQL_TOKENS="DELETE FROM api_tokens WHERE name LIKE 'zz-test-%' AND revoked_at IS NOT NULL;"
SQL_INGEST="DELETE FROM inbox_tokens WHERE org_id = '$TEST_ORG' AND label = '';"
SEL_TOKENS="SELECT id, org_id, name FROM api_tokens WHERE name LIKE 'zz-test-%' AND revoked_at IS NOT NULL ORDER BY name;"
SEL_INGEST="SELECT id, org_id, last4 FROM inbox_tokens WHERE org_id = '$TEST_ORG' AND label = '';"

psql_q() { hc exec -T postgres psql -U heimdall -d heimdall -v ON_ERROR_STOP=1 -c "$1"; }

echo
echo "Database rows (p13, p15 revoked access tokens; p02 ingest token of $TEST_ORG):"
echo "  $SQL_TOKENS"
echo "  $SQL_INGEST"
if [ "$EXEC" = direct ]; then
	echo "  (database steps skipped: HEIMDALL_OPS_EXEC=direct)"
else
	psql_q "$SEL_TOKENS"
	psql_q "$SEL_INGEST"
fi

cat <<'EOF'

Manual steps (not done by this script):
  1. Remove the sources zz-test-p13-src and zz-test-p16-src from each
     organization's inbox/sources.yaml.
  2. Turned off test share links (zz-test-p03-share, zz-test-p05-share,
     zz-test-p05-share-never, zz-test-p08-share, zz-test-p16-share) stay: the
     share index table holds no link names, so they are not matched here.
  3. Delete the organization zz-test-p15-renamed in Settings > Organization
     (type its name). This also removes the benchmark zz-test-p11-qb.
  4. Rebuild the search index of the main organization:
     bifrost-bench index rebuild
Left on purpose: job history rows and revoked worker tokens.
EOF

if [ -z "$apply" ]; then
	echo
	echo "Dry run: nothing was moved or deleted. Run again with --apply --i-reviewed-the-list to do it."
	rm -f "$keep"
	exit 0
fi

ts=$(date -u +%Y%m%dT%H%M%SZ)
q="$ROOT/quarantine/pass6-$ts"
moved=0
while IFS='|' read -r _ _ path; do
	rel=${path#"$ROOT"/}
	# shellcheck disable=SC2016
	in_data 'mkdir -p "$(dirname "$2/$3")" && mv "$1/$3" "$2/$3"' "$q" "$rel"
	moved=$((moved + 1))
done <"$keep"
rm -f "$keep"
log "moved $moved paths into $q"
if [ "$EXEC" != direct ]; then
	psql_q "$SQL_TOKENS"
	psql_q "$SQL_INGEST"
fi
log "done; the manual steps above are still to do"
