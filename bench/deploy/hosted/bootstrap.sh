#!/usr/bin/env bash
# First run of a hosted Heimdall (docs/hosting.md). Safe to run again: every
# step skips what is already done.
#
#   deploy/hosted/bootstrap.sh --domain heimdall.example.com --email ops@example.com \
#       --owner-email you@example.com [--owner-name "Your Name"] [--owner-org "Acme"] \
#       [--no-build]
#
# Steps:
#   1. deploy/hosted/.env: created from .env.example with random database and
#      gateway secrets (chmod 600); --domain and --email fill in the site.
#      HEIMDALL_MASTER_KEY is generated when missing, never replaced.
#   2. build the server image; --no-build uses the images already there
#      (HEIMDALL_IMAGE). The shared worker (profile shared-runner) is set up
#      separately: docs/hosting.md, Shared worker
#   3. start Postgres, apply database migrations (bifrost-bench db migrate)
#   4. first owner account and its organization (bifrost-bench user
#      create); the generated password is printed once
#   5. no demo organization: a hosted install requires sign-in for every
#      organization and has no public demo (--demo-runs, --no-demo and
#      --demo-runner are still accepted and ignored, so older commands keep
#      working)
#   6. start everything and wait for health; print the URL
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

usage() {
	header_help "${BASH_SOURCE[0]}"
	exit "${1:-0}"
}

domain='' email='' owner_email='' owner_name='' owner_org='' demo_flag='' build=1
while [ $# -gt 0 ]; do
	case $1 in
	--domain) domain=${2:?--domain needs a value}; shift 2 ;;
	--email) email=${2:?--email needs a value}; shift 2 ;;
	--owner-email) owner_email=${2:?--owner-email needs a value}; shift 2 ;;
	--owner-name) owner_name=${2:?--owner-name needs a value}; shift 2 ;;
	--owner-org) owner_org=${2:?--owner-org needs a value}; shift 2 ;;
	--demo-runs) : "${2:?--demo-runs needs a directory}"; demo_flag=1; shift 2 ;; # retired, ignored
	--no-demo) shift ;;                                                         # retired: there is never a demo
	--demo-runner) demo_flag=1; shift ;;                                         # retired, ignored
	--no-build) build=''; shift ;;
	-h | --help) usage 0 ;;
	*) printf 'unknown argument: %s\n\n' "$1" >&2; usage 64 ;;
	esac
done

need docker openssl sed grep
docker compose version >/dev/null 2>&1 || die "the Docker Compose plugin (docker compose) is required"

# 1. Env file.
if [ ! -f "$ENV_FILE" ]; then
	log "creating $ENV_FILE with random secrets"
	(umask 077 && cp "$HOSTED_DIR/.env.example" "$ENV_FILE")
	chmod 600 "$ENV_FILE"
	for k in HEIMDALL_PG_PASSWORD BIFROST_SETUP_TOKEN BIFROST_PG_PASSWORD BENCH_FAULTPROXY_TOKEN; do
		env_set "$k" "$(random_hex)"
	done
fi
# The keystore master key (provider keys, org secrets). An env file made
# before the key existed gets one too; an existing key is never replaced,
# since that would make every stored key unreadable.
if [ -z "$(env_get HEIMDALL_MASTER_KEY)" ]; then
	log "generating HEIMDALL_MASTER_KEY (keep a copy in your secrets manager: backups leave it out)"
	env_set HEIMDALL_MASTER_KEY "$(openssl rand -base64 32)"
fi
# Shared by the server and Bifrost (heimdall plugin usage export). Older env
# files get one too; an existing value is kept.
[ -n "$(env_get HEIMDALL_GATEWAY_USAGE_SECRET)" ] || env_set HEIMDALL_GATEWAY_USAGE_SECRET "$(random_hex)"
[ -n "$(env_get HEIMDALL_BENCH_DIR)" ] || env_set HEIMDALL_BENCH_DIR "$BENCH_DIR"
[ -z "$domain" ] || env_set HEIMDALL_DOMAIN "$domain"
[ -z "$email" ] || env_set HEIMDALL_ACME_EMAIL "$email"
perm=$(stat -c %a "$ENV_FILE")
[ "$perm" = 600 ] || { log "tightening $ENV_FILE from $perm to 600"; chmod 600 "$ENV_FILE"; }
require_configured
domain=$(env_get HEIMDALL_DOMAIN)

# 2. Images.
if [ -z "$build" ]; then
	log "--no-build: using the images already present"
else
	log "building the server image (first build takes a few minutes: npm ci, vite, go build)"
	hc build heimdall
fi

# 3. Database.
log "starting Postgres"
hc up -d --wait postgres
if bench_has db; then
	log "applying database migrations"
	bench -- db migrate
else
	log "this build has no 'db' command; the server applies migrations itself on start"
fi

# 4. First owner.
if [ -n "$owner_email" ]; then
	if bench_has user; then
		pw=$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-24)
		# -org takes an existing org's id or slug, or names a new one.
		args=(user create -email "$owner_email" -org "${owner_org:-Heimdall}" -role owner)
		[ -z "$owner_name" ] || args+=(-name "$owner_name")
		if out=$(printf '%s\n' "$pw" | bench -- "${args[@]}" 2>&1); then
			log "owner account created: $owner_email"
			printf '\n  Owner password (shown once, change it after signing in): %s\n\n' "$pw" >&2
		elif printf '%s' "$out" | grep -qi 'already exists'; then
			log "owner account $owner_email already exists; unchanged"
		else
			printf '%s\n' "$out" >&2
			die "creating the owner account failed"
		fi
		unset pw
	else
		log "this build has no 'user' command: sign up at https://$domain/ instead (set HEIMDALL_SIGNUP=open for that first signup, then back to invite)"
	fi
else
	log "no --owner-email: create the first account later with deploy/hosted/bootstrap.sh --owner-email you@example.com"
fi

# 5. No demo organization. Hosted installs require sign-in everywhere; an
# organization left over from an older bootstrap is ignored by the server
# (anonymous visitors never reach it) and can be deleted by its owner.
if [ -n "$demo_flag" ]; then
	log "--demo-runs and --demo-runner are ignored: hosted installs have no demo organization or demo runner"
fi

# 6. Everything up.
log "starting the stack"
hc up -d --wait
public_url=${HEIMDALL_PUBLIC_URL:-$(env_get HEIMDALL_PUBLIC_URL)}
log "Heimdall is up: ${public_url:-https://$domain}/"
log "status: docker compose -f $COMPOSE_FILE --env-file $ENV_FILE ps   logs: ... logs -f heimdall caddy"
