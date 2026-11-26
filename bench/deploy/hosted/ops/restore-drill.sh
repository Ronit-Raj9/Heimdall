#!/usr/bin/env bash
# Monthly restore drill: restore the latest off-site backup into a throwaway
# stack and check it (docs/operations.md, Restore drill). Run it on the
# laptop or a scratch machine, never on the live server.
#
#   deploy/hosted/ops/restore-drill.sh                       dry run: print the plan
#   deploy/hosted/ops/restore-drill.sh --apply [--work DIR] [--keep]
#
# Needs: restic, docker, jq, openssl, sha256sum; restic settings in
# ~/.config/heimdall/restic.env ($HEIMDALL_RESTIC_ENV, same names as on the
# server) and the backup passphrase file ($HEIMDALL_BACKUP_PASSPHRASE_FILE)
# from the owner's escrow.
#
# Steps with --apply (it needs an interactive terminal):
#   1. restic restore latest into DIR; every file of the newest manifest is
#      checked against its sha256.
#   2. The encrypted env copy is decrypted into DIR/env (0600), never printed.
#   3. A compose project heimdall-drill starts only postgres and heimdall
#      (serve -offline) from the hosted compose file, on 127.0.0.1:18090,
#      with its data in DIR.
#   4. pg_restore of the dump; the project archive is unpacked.
#   5. Checks: /healthz answers 200; the organization count equals the
#      manifest; evidence verify passes on three random experiments.
#   6. Prints "restore drill passed: <n> orgs, <m> experiments, backup of <ts>"
#      and removes the drill stack and its volumes (unless --keep).
set -euo pipefail
# shellcheck source=../lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"

RESTIC_ENV=${HEIMDALL_RESTIC_ENV:-$HOME/.config/heimdall/restic.env}
PASS_FILE=${HEIMDALL_BACKUP_PASSPHRASE_FILE:-}
apply='' work='' keep=''
while [ $# -gt 0 ]; do
	case $1 in
	--apply) apply=1; shift ;;
	--work) work=${2:?--work needs a folder}; shift 2 ;;
	--keep) keep=1; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done

if [ -z "$apply" ]; then
	header_help "${BASH_SOURCE[0]}" | sed -n '/^Steps/,$p'
	echo
	echo "Dry run: nothing was restored or started. Run again with --apply."
	exit 0
fi
[ -t 0 ] || die "--apply needs an interactive terminal; it never runs from a script or a scheduled job"
need restic docker jq openssl sha256sum curl
# The live server runs the compose project "heimdall"; the drill never runs there.
if [ -n "$(docker ps -q --filter label=com.docker.compose.project=heimdall 2>/dev/null)" ]; then
	die "a heimdall stack is running on this machine: run the drill on the laptop or a scratch machine"
fi
[ -f "$RESTIC_ENV" ] || die "no restic settings at $RESTIC_ENV"
[ -n "$PASS_FILE" ] && [ -f "$PASS_FILE" ] || die "set HEIMDALL_BACKUP_PASSPHRASE_FILE to the backup passphrase file"
set -a
# shellcheck disable=SC1090
. "$RESTIC_ENV"
set +a

work=${work:-$(mktemp -d "${TMPDIR:-/tmp}/heimdall-drill.XXXXXX")}
mkdir -p "$work"
chmod 700 "$work"
P=(docker compose -p heimdall-drill -f "$COMPOSE_FILE" -f "$work/drill.override.yml" --env-file "$work/env")
cleanup() {
	if [ -z "$keep" ] && [ -f "$work/drill.override.yml" ]; then
		"${P[@]}" down -v >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

# 1. Restore and check every file of the newest manifest.
restic restore latest --target "$work/restore" >&2
bk=$(find "$work/restore" -type f -name 'manifest-*.json' -printf '%h\n' | head -n 1)
[ -n "$bk" ] || die "the backup holds no manifest"
manifest=$(find "$bk" -maxdepth 1 -name 'manifest-*.json' | sort | tail -n 1)
ts=$(jq -r .time "$manifest")
while IFS=' ' read -r name sum; do
	[ -f "$bk/$name" ] || die "missing from the backup: $name"
	[ "$(sha256sum "$bk/$name" | cut -d' ' -f1)" = "$sum" ] || die "sha256 differs: $name"
done < <(jq -r '.files[] | "\(.name) \(.sha256)"' "$manifest")
log "all $(jq '.files | length' "$manifest") files of the backup of $ts match the manifest"

# 2. The env copy, decrypted without printing it.
enc=$(jq -r '.files[].name | select(startswith("heimdall-env-"))' "$manifest" | head -n 1)
[ -n "$enc" ] || die "the backup holds no encrypted env copy"
umask 077
openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass "file:$PASS_FILE" -in "$bk/$enc" -out "$work/env"
chmod 600 "$work/env"

# 3. The drill stack: postgres and heimdall only, data in the work folder.
mkdir -p "$work/pgdata" "$work/data"
cat >"$work/drill.override.yml" <<EOF
services:
  postgres:
    volumes: !override
      - $work/pgdata:/var/lib/postgresql
  heimdall:
    command: ["serve", "-offline", "-config", "/srv/heimdall/deploy/bench.yaml"]
    ports: !override
      - "127.0.0.1:18090:8090"
    volumes: !override
      - $HOSTED_DIR/bench.hosted.yaml:/srv/heimdall/deploy/bench.yaml:ro
      - $work/data:/var/lib/heimdall
EOF
"${P[@]}" up -d --wait --no-deps postgres >&2

# 4. Database and project files.
dump=$(jq -r '.files[].name | select(startswith("heimdall-db-"))' "$manifest" | head -n 1)
[ -n "$dump" ] || die "the backup holds no database dump"
"${P[@]}" exec -T postgres pg_restore -U heimdall -d heimdall --no-owner --clean --if-exists <"$bk/$dump" >&2
arch=$(jq -r '.files[].name | select(startswith("heimdall-projects-") or startswith("heimdall-evidence-"))' "$manifest" | head -n 1)
[ -z "$arch" ] || tar -xzf "$bk/$arch" -C "$work/data"
"${P[@]}" up -d --wait --no-deps heimdall >&2

# 5. Checks.
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:18090/healthz)
[ "$code" = 200 ] || die "/healthz answered $code"
want=$(jq -r .orgs "$manifest")
have=$("${P[@]}" exec -T postgres psql -U heimdall -d heimdall -tAc 'SELECT count(*) FROM orgs' | tr -d '[:space:]')
[ "$have" = "$want" ] || die "organizations: $have restored, $want in the manifest"
exps=$(find "$work/data" -type d -name 'e_*' -prune | shuf -n 3)
checked=0
for e in $exps; do
	org=$(basename "$(dirname "$(dirname "$e")")")
	"${P[@]}" exec -T heimdall bifrost-bench evidence verify -org "$org" -exp "$(basename "$e")" >&2 ||
		die "evidence verify failed for $(basename "$e")"
	checked=$((checked + 1))
done
[ "$checked" -gt 0 ] || log "warning: no experiment found to verify"
log "provider key check: skipped (no key check command yet)"

# 6. Result.
echo "restore drill passed: $have orgs, $(jq -r .experiments "$manifest") experiments, backup of $ts"
