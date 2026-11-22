#!/usr/bin/env bash
# Upgrade a hosted Heimdall in place (docs/hosting.md, Upgrades):
#
#   deploy/hosted/upgrade.sh [--ref origin/main] [--no-pull] [--no-backup]
#                            [--drain-timeout 10m] [--no-drain]
#
#   1. database backup (backup.sh --db-only, into $HEIMDALL_BACKUP_DIR or
#      /var/backups/heimdall), unless --no-backup
#   2. git pull --ff-only (or check out --ref), unless --no-pull
#   3. the images of the running containers are tagged :previous (an
#      existing :previous is kept when the service is not running), then
#      rebuilt from the checkout
#   4. Postgres up, migrations applied (bifrost-bench db migrate)
#   4b. drain: the running server stops handing out new leases and the
#      script waits until no run is in flight, or --drain-timeout passes
#      (bifrost-bench drain inside the server container); images are already
#      built, so the window without new leases is only the restart. Runs
#      still in flight at the timeout keep their leases (durable leases) and
#      are re-attached by the new server when the restart takes under 2
#      minutes; a shared worker container that is recreated stops its own
#      runs. Skipped
#      with --no-drain, or when the running server has no drain command
#   5. containers whose image or configuration changed are recreated, and
#      containers of services no longer in the compose file (the retired
#      demo-runner) are removed; wait for health
#   6. with the shared worker running: its registry gets fresh copies of
#      the base images (registry-sync.sh)
#
# Rollback, if the new version misbehaves (the previous commit is printed
# at the start and the end of the run):
#   git -C <checkout> checkout <previous commit>
#   docker tag heimdall/server:previous heimdall/server:local
#   docker tag heimdall/runner:previous heimdall/runner:local   # shared worker only
#   docker compose -f deploy/hosted/docker-compose.yml --env-file deploy/hosted/.env up -d --no-build --wait
# With the default tags (:local) the rollback tag is :previous. A stack with
# its own tag (HEIMDALL_IMAGE=heimdall/server:<tag>, for a second stack on
# the same Docker host) gets :<tag>-previous instead, so two stacks never
# move each other's rollback image; retag that one as :<tag>.
# Migrations only add tables and columns, so the previous server runs on the
# migrated database. If one did not, restore the dump taken in step 1
# (docs/hosting.md, Restore).
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

ref='' pull=1 backup=1 drain=1 drain_timeout=10m
while [ $# -gt 0 ]; do
	case $1 in
	--ref) ref=${2:?--ref needs a value}; shift 2 ;;
	--no-pull) pull=; shift ;;
	--no-backup) backup=; shift ;;
	--drain-timeout) drain_timeout=${2:?--drain-timeout needs a value, e.g. 10m}; shift 2 ;;
	--no-drain) drain=; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done

need docker git
require_configured
# The shared worker is upgraded with the rest when it runs here: its
# profile is added only then, so an install without it stays without it.
shared=''
if [ -n "$(HEIMDALL_PROFILES=shared-runner hc ps -q shared-runner 2>/dev/null)" ]; then
	shared=1
	export HEIMDALL_PROFILES=${HEIMDALL_PROFILES:+$HEIMDALL_PROFILES,}shared-runner
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

# running_image_id SERVICE prints the image ID of the service's running
# container, or nothing when none runs. The configured tag can already point
# at an image built by an earlier attempt that failed before recreating the
# service, so the rollback tag follows what actually runs, never the tag.
running_image_id() {
	local cid
	cid=$(hc ps -q "$1" 2>/dev/null | head -n 1) || true
	[ -n "$cid" ] || return 0
	docker inspect --format '{{.Image}}' "$cid" 2>/dev/null || true
}

# previous_tag IMAGE prints the rollback tag of IMAGE: repo:previous for the
# default :local tag (the documented rollback), repo:<tag>-previous for any
# other tag, so a second stack on the same host with its own HEIMDALL_IMAGE
# never retags the first stack's rollback image.
previous_tag() {
	local repo=${1%:*} tag=${1##*:}
	case $tag in */*) repo=$1 tag=local ;; esac # no tag, only a registry port
	if [ "$repo" = "$1" ] || [ "$tag" = local ]; then
		echo "$repo:previous"
	else
		echo "$repo:$tag-previous"
	fi
}

keep_previous() { # keep_previous SERVICE IMAGE
	local id prev
	prev=$(previous_tag "$2")
	id=$(running_image_id "$1")
	if [ -n "$id" ]; then
		docker tag "$id" "$prev"
		log "kept running $1 image $id as $prev for rollback"
	elif docker image inspect "$prev" >/dev/null 2>&1; then
		log "no running $1 container; keeping the existing $prev"
	else
		log "no running $1 container and no $prev; no rollback image for $1"
	fi
}
keep_previous heimdall "$server_image"
keep_previous shared-runner "$runner_image"

log "building images"
hc build --pull

hc up -d --wait postgres
if bench_has db; then
	log "applying database migrations"
	bench -- db migrate
fi

# drain_server turns drain mode on in the running server and waits for the
# runs in flight. A failure before the restart turns it off again (trap), so
# an aborted upgrade never leaves the old server handing out no leases.
# Exit 5 of bifrost-bench drain: the timeout passed with runs in flight.
drained=''
undrain() {
	if [ -n "$drained" ]; then
		log "upgrade stopped before the restart: turning drain mode off"
		hc exec -T heimdall /usr/local/bin/bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -off >/dev/null 2>&1 ||
			log "could not turn drain mode off; run: docker compose exec heimdall bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -off"
	fi
}
undrain_quiet() {
	[ -n "$drained" ] || return 0
	drained=''
	hc exec -T heimdall /usr/local/bin/bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -off >/dev/null 2>&1 ||
		log "could not confirm drain mode is off; check: docker compose exec heimdall bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -status"
}
drain_server() {
	if [ -z "$(hc ps -q heimdall 2>/dev/null)" ]; then
		log "no running server: nothing to drain"
		return 0
	fi
	if ! hc exec -T heimdall /usr/local/bin/bifrost-bench help 2>&1 | grep -Eq '^[[:space:]]+drain([[:space:]]|$)'; then
		log "the running server has no drain command (older build): restarting without draining"
		return 0
	fi
	drained=1
	trap undrain EXIT
	log "draining: no new leases; waiting up to $drain_timeout for runs in flight"
	local rc=0
	hc exec -T heimdall /usr/local/bin/bifrost-bench drain -config /srv/heimdall/deploy/bench.yaml -timeout "$drain_timeout" || rc=$?
	case $rc in
	0) log "drained: no run in flight" ;;
	5) log "drain timeout ($drain_timeout): the runs still in flight keep their leases and are re-attached after the restart" ;;
	*) log "drain failed (exit $rc): restarting without waiting" ;;
	esac
}
if [ -n "$drain" ]; then
	drain_server
fi

log "restarting changed services"
# --remove-orphans removes only containers of services the compose file no
# longer has (demo-runner); services of profiles not started here stay.
hc up -d --wait --remove-orphans
# A recreated server starts with drain mode off; one whose image and
# configuration did not change was not restarted, so it is turned off here.
undrain_quiet
hc ps
if [ -n "$shared" ]; then
	"$HOSTED_DIR/registry-sync.sh" || log "registry-sync.sh failed: org Dockerfiles that build FROM a bench image fail until it succeeds"
fi
log "upgrade done ($before -> $(git -C "$repo" rev-parse HEAD)). Roll back: see the header of $0"
