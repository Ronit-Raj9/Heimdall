#!/usr/bin/env bash
# Shared helpers for the hosted Heimdall scripts (bootstrap.sh, backup.sh,
# upgrade.sh, smoke.sh). Sourced, never run on its own.
#
# Environment it reads:
#   HEIMDALL_ENV_FILE   env file (default deploy/hosted/.env)
#   HEIMDALL_PROJECT    compose project name (default from the compose file: heimdall)
#   HEIMDALL_PROFILES   extra compose profiles, comma-separated (e.g. shared-runner)
#   HEIMDALL_COMPOSE_OVERRIDE  extra compose file (default
#                       deploy/hosted/docker-compose.override.yml when present)

# shellcheck disable=SC2034 # the variables below are used by the scripts that source this file
HOSTED_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BENCH_DIR=$(cd "$HOSTED_DIR/../.." && pwd)
ENV_FILE=${HEIMDALL_ENV_FILE:-$HOSTED_DIR/.env}
COMPOSE_FILE=$HOSTED_DIR/docker-compose.yml

log() { printf '[heimdall] %s\n' "$*" >&2; }
# header_help prints the comment block at the top of a script (its --help).
header_help() { awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$1"; }
die() {
	printf '[heimdall] error: %s\n' "$*" >&2
	exit 1
}

need() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null 2>&1 || die "$c is required (docs/hosting.md, Install Docker)"
	done
}

# env_get KEY prints KEY's value from the env file (last assignment wins,
# surrounding quotes removed), or nothing.
env_get() {
	[ -f "$ENV_FILE" ] || return 0
	sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1 | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"
}

# env_set KEY VALUE replaces KEY's line in the env file, or appends it. The
# value must not contain '|' or a newline (hex tokens, domains, paths).
env_set() {
	case $2 in *'|'* | *$'\n'*) die "env_set $1: value contains '|' or a newline" ;; esac
	if grep -q "^$1=" "$ENV_FILE"; then
		local tmp
		tmp=$(mktemp "$ENV_FILE.XXXXXX")
		sed "s|^$1=.*|$1=$2|" "$ENV_FILE" >"$tmp"
		chmod 600 "$tmp"
		mv "$tmp" "$ENV_FILE"
	else
		printf '%s=%s\n' "$1" "$2" >>"$ENV_FILE"
	fi
}

random_hex() { openssl rand -hex 24; }

# docker_gid prints the group owning the Docker socket (the shared worker joins
# it to start agent containers).
docker_gid() {
	local sock=/var/run/docker.sock
	if [ -S "$sock" ]; then stat -c %g "$sock"; else echo 0; fi
}

# hc runs docker compose for this stack with the env file and the derived
# values every service definition expects.
hc() {
	local args=(-f "$COMPOSE_FILE")
	# Site-specific changes go in an override file, never in the tracked one.
	local override=${HEIMDALL_COMPOSE_OVERRIDE:-$HOSTED_DIR/docker-compose.override.yml}
	[ -f "$override" ] && args+=(-f "$override")
	args+=(--env-file "$ENV_FILE")
	[ -n "${HEIMDALL_PROJECT:-}" ] && args+=(-p "$HEIMDALL_PROJECT")
	local p
	if [ -n "${HEIMDALL_PROFILES:-}" ]; then
		for p in ${HEIMDALL_PROFILES//,/ }; do args+=(--profile "$p"); done
	fi
	local bench_dir
	bench_dir=${HEIMDALL_BENCH_DIR:-$(env_get HEIMDALL_BENCH_DIR)}
	HEIMDALL_BENCH_DIR=${bench_dir:-$BENCH_DIR} \
		HEIMDALL_DOCKER_GID=${HEIMDALL_DOCKER_GID:-$(docker_gid)} \
		docker compose "${args[@]}" "$@"
}

# bench runs a bifrost-bench command in a one-off server container: same
# image, networks, volumes and environment as the running server. Extra
# `docker compose run` options go before a literal "--".
bench() {
	local opts=()
	while [ $# -gt 0 ] && [ "$1" != "--" ]; do
		opts+=("$1")
		shift
	done
	[ "${1:-}" = "--" ] && shift
	hc run --rm --no-deps -T "${opts[@]}" heimdall "$@"
}

# bench_has CMD reports whether the server image's binary has subcommand CMD
# (pieces of the hosted platform land separately; the scripts say what they
# skip instead of failing obscurely).
bench_has() {
	bench --entrypoint /usr/local/bin/bifrost-bench -- help 2>&1 | grep -Eq "^[[:space:]]+$1([[:space:]]|$)"
}

# require_configured refuses placeholder values in the env file.
require_configured() {
	[ -f "$ENV_FILE" ] || die "$ENV_FILE does not exist: run deploy/hosted/bootstrap.sh first"
	local k v
	for k in HEIMDALL_DOMAIN HEIMDALL_PG_PASSWORD; do
		v=$(env_get "$k")
		[ -n "$v" ] || die "$k is empty in $ENV_FILE"
		case $v in replace-with-* | heimdall.example.com) die "$k in $ENV_FILE still holds the placeholder '$v'" ;; esac
	done
}
