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
# The registry org builds read is read-only and needs credentials (G3 B2).
# This script is the only writer: it starts registry-writer (same storage,
# sync identity only, on the registrysync network that nothing else joins)
# for the copy and removes it afterwards. Each image goes `docker save` ->
# the registry-sync tool container (crane) -> registry-writer, so neither
# registry needs a host port. The digest each push produced is recorded in
# the registry manifest (HEIMDALL_REGISTRY_AUTH_DIR/runner/manifest); the
# shared worker builds FROM those digests only, so a tag moved later cannot
# change what an org build starts from. Missing credentials are generated
# first (ensure_registry_auth in lib.sh).
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

ensure_registry_auth
auth_dir=$HEIMDALL_REGISTRY_AUTH_DIR
manifest=$auth_dir/runner/manifest

log "starting the registry"
hc up -d --no-deps registry
if [ -n "$registry_auth_changed" ]; then
	hc restart registry >/dev/null # it reads its htpasswd at start
fi
log "starting the registry's write side for this copy"
hc up -d --no-deps registry-writer
trap 'hc rm -sf registry-writer >/dev/null 2>&1 || true' EXIT

# set_pin REF DIGEST records REF's digest in the manifest (replacing an
# older line for REF), atomically: the shared worker reads it on every build.
set_pin() {
	local tmp
	tmp=$(mktemp "$manifest.XXXXXX")
	{ [ -f "$manifest" ] && awk -v r="$1" '$1 != r' "$manifest"; printf '%s %s\n' "$1" "$2"; } >"$tmp"
	chgrp "$(docker_gid)" "$tmp" 2>/dev/null || true
	chmod 0640 "$tmp"
	mv -f "$tmp" "$manifest"
}

# The tool logs in with the sync identity (password on stdin, never on a
# command line), waits for the writer, pushes and prints the digest ref.
push_script='set -e
cat > /tmp/image.tar
i=0; until wget -q -O /dev/null http://registry-writer:5000/; do i=$((i+1)); [ $i -lt 30 ] || { echo "registry-writer did not start" >&2; exit 1; }; sleep 1; done
IFS=: read -r u p < /auth/push.auth
printf "%s" "$p" | crane auth login registry-writer:5000 -u "$u" --password-stdin >/dev/null 2>&1
crane push --insecure /tmp/image.tar "registry-writer:5000/$SYNC_REF"'

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
	if out=$(docker save "$ref" | hc run --rm --no-deps -T -e "SYNC_REF=$ref" registry-sync "$push_script") &&
		digest=${out##*@} && [[ $digest =~ ^sha256:[0-9a-f]{64}$ ]]; then
		set_pin "$ref" "$digest"
		log "$ref pinned at $digest"
		copied=$((copied + 1))
	else
		log "FAILED $ref: the push to $registry did not complete${out:+ ($out)}"
		failed=$((failed + 1))
	fi
done
log "$copied image(s) copied to $registry and pinned in $manifest, $failed failed"
[ "$failed" -eq 0 ]
