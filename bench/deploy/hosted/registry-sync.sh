#!/usr/bin/env bash
# Copy host images into the shared worker's private image registry
# (docs/hosting.md, Shared worker):
#
#   deploy/hosted/registry-sync.sh [IMAGE...]
#
# Org Dockerfiles build on the isolated BuildKit, which cannot see images on
# the host daemon. Their bench and platform base images (FROM
# bench/coding-core-base:1, or an image in HEIMDALL_SHARED_PLATFORM_IMAGES)
# are served from the registry service instead; this script puts them there.
#
# Without arguments it copies bench/coding-core-base:1 and every entry of
# HEIMDALL_SHARED_PLATFORM_IMAGES (a prefix ending in "/" or ":" stands for
# every host image it starts). Images missing on the host are pulled first.
# Entries pinned by digest or naming a registry host are skipped: BuildKit
# pulls those itself. Run it again whenever one of these images is rebuilt.
#
# Each image goes `docker save` -> the registry-sync tool container (crane)
# on the builder network -> registry:5000, so the registry never needs a
# host port.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

case ${1:-} in -h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;; esac

need docker
require_configured
export HEIMDALL_PROFILES=${HEIMDALL_PROFILES:+$HEIMDALL_PROFILES,}shared-runner,registry-sync

registry=${HEIMDALL_SHARED_REGISTRY:-$(env_get HEIMDALL_SHARED_REGISTRY)}
registry=${registry:-registry:5000}

# has_registry_host REF: the first path part has a dot or a port, or is
# localhost (the same rule as the worker's policy, utils/dockerbase.go).
has_registry_host() {
	local first=${1%%/*}
	[ "$first" != "$1" ] && { [[ $first == *[.:]* ]] || [ "$first" = localhost ]; }
}

images=()
if [ $# -gt 0 ]; then
	images=("$@")
else
	images=(bench/coding-core-base:1)
	platform=${HEIMDALL_SHARED_PLATFORM_IMAGES:-$(env_get HEIMDALL_SHARED_PLATFORM_IMAGES)}
	IFS=, read -r -a entries <<<"$platform"
	for e in "${entries[@]}"; do
		e=$(printf '%s' "$e" | tr -d '[:space:]')
		[ -n "$e" ] || continue
		case $e in
		*/ | *:)
			mapfile -t found < <(docker image ls --format '{{.Repository}}:{{.Tag}}' | grep -F -- "$e" | awk -v p="$e" 'index($0, p) == 1 && $0 !~ /<none>/')
			[ ${#found[@]} -gt 0 ] || log "no host image starts with $e; nothing to copy for it"
			images+=("${found[@]}")
			;;
		*) images+=("$e") ;;
		esac
	done
fi

log "starting the registry"
hc up -d --no-deps registry

copied=0 failed=0
declare -A seen=()
for ref in "${images[@]}"; do
	[ -z "${seen[$ref]:-}" ] || continue
	seen[$ref]=1
	if [[ $ref == *@* ]]; then
		log "skipping $ref: pinned by digest, BuildKit pulls it itself"
		continue
	fi
	if has_registry_host "$ref"; then
		log "skipping $ref: it names a registry, BuildKit pulls it itself"
		continue
	fi
	if ! docker image inspect "$ref" >/dev/null 2>&1; then
		log "$ref is not on this host; pulling it"
		if ! docker pull -q "$ref" >/dev/null; then
			log "FAILED $ref: not on this host and the pull failed"
			failed=$((failed + 1))
			continue
		fi
	fi
	log "copying $ref to $registry/$ref"
	if docker save "$ref" | hc run --rm --no-deps -T -e "SYNC_DEST=$registry/$ref" registry-sync \
		'cat > /tmp/image.tar && crane push --insecure /tmp/image.tar "$SYNC_DEST" >/dev/null'; then
		copied=$((copied + 1))
	else
		log "FAILED $ref: the push to $registry did not complete"
		failed=$((failed + 1))
	fi
done
log "$copied image(s) copied to $registry, $failed failed"
[ "$failed" -eq 0 ]
