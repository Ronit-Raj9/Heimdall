#!/usr/bin/env bash
# Upgrade a hosted Heimdall in place (docs/hosting.md, Upgrades):
#
#   deploy/hosted/upgrade.sh [--ref origin/main] [--no-pull] [--no-backup]
#
#   1. database backup (backup.sh --db-only, into $HEIMDALL_BACKUP_DIR or
#      /var/backups/heimdall), unless --no-backup
#   2. git pull --ff-only (or check out --ref), unless --no-pull
#   3. the running images are tagged :previous, then rebuilt from the checkout
#   4. Postgres up, migrations applied (bifrost-bench db migrate)
#   5. containers whose image or configuration changed are recreated; wait
#      for health
#
# Rollback, if the new version misbehaves (the previous commit is printed
# at the start and the end of the run):
#   git -C <checkout> checkout <previous commit>
#   docker tag heimdall/server:previous heimdall/server:local
#   docker tag heimdall/runner:previous heimdall/runner:local   # demo runner only
#   docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env up -d --no-build --wait
# Migrations only add tables and columns, so the previous server runs on the
# migrated database. If one did not, restore the dump taken in step 1
# (docs/hosting.md, Restore).
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

ref='' pull=1 backup=1
while [ $# -gt 0 ]; do
	case $1 in
	--ref) ref=${2:?--ref needs a value}; shift 2 ;;
	--no-pull) pull=; shift ;;
	--no-backup) backup=; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done

need docker git
require_configured
if [ -n "$(env_get HEIMDALL_DEMO_RUNNER_TOKEN)" ]; then
	export HEIMDALL_PROFILES=${HEIMDALL_PROFILES:+$HEIMDALL_PROFILES,}demo-runner
fi

repo=$(git -C "$BENCH_DIR" rev-parse --show-toplevel)
before=$(git -C "$repo" rev-parse HEAD)
log "current commit $before"

if [ -n "$backup" ]; then
	"$HOSTED_DIR/backup.sh" --db-only
fi

if [ -n "$pull" ]; then
	if [ -n "$(git -C "$repo" status --porcelain --untracked-files=no)" ]; then
		die "the checkout has local changes; commit or stash them, or pass --no-pull"
	fi
	git -C "$repo" fetch --prune
	if [ -n "$ref" ]; then
		git -C "$repo" checkout --detach "$ref"
	else
		git -C "$repo" pull --ff-only
	fi
	log "now at $(git -C "$repo" rev-parse HEAD)"
fi

# Same precedence as compose: the shell's value, then the env file's.
server_image=${HEIMDALL_IMAGE:-$(env_get HEIMDALL_IMAGE)}
server_image=${server_image:-heimdall/server:local}
runner_image=${HEIMDALL_RUNNER_IMAGE:-$(env_get HEIMDALL_RUNNER_IMAGE)}
runner_image=${runner_image:-heimdall/runner:local}
for img in "$server_image" "$runner_image"; do
	if docker image inspect "$img" >/dev/null 2>&1; then
		docker tag "$img" "${img%:*}:previous"
		log "kept $img as ${img%:*}:previous for rollback"
	fi
done

log "building images"
hc build --pull

hc up -d --wait postgres
if bench_has db; then
	log "applying database migrations"
	bench -- db migrate
fi

log "restarting changed services"
hc up -d --wait --remove-orphans
hc ps
log "upgrade done ($before -> $(git -C "$repo" rev-parse HEAD)). Roll back: see the header of $0"
