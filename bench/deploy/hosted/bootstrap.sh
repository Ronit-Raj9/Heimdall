#!/usr/bin/env bash
# First run of a hosted Heimdall (docs/hosting.md). Safe to run again: every
# step skips what is already done.
#
#   deploy/hosted/bootstrap.sh --domain heimdall.example.com --email ops@example.com \
#       --owner-email you@example.com [--owner-name "Your Name"] [--owner-org "Acme"] \
#       [--demo-runs DIR] [--demo-runner] [--no-build]
#
# Steps:
#   1. deploy/hosted/.env: created from .env.example with random database and
#      gateway secrets (chmod 600); --domain and --email fill in the site.
#   2. build the server image (and the runner image with --demo-runner);
#      --no-build uses the images already there (HEIMDALL_IMAGE)
#   3. start Postgres, apply database migrations (bifrost-bench db migrate)
#   4. first owner account and its organization (bifrost-bench user
#      create); the generated password is printed once
#   5. only with --demo-runs DIR: a demo organization, public read-only, with
#      your own sealed runs from DIR imported (bifrost-bench org import).
#      Nothing is imported by default, and Heimdall's test bundle
#      (bench/testdata/recorded) is refused: a production install holds
#      real data only. With --owner-email, a demo that already exists gets
#      that owner as a member (bifrost-bench org member add). --no-demo is
#      accepted for older scripts and changes nothing
#   6. start everything and wait for health; print the URL
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

usage() {
	header_help "${BASH_SOURCE[0]}"
	exit "${1:-0}"
}

domain='' email='' owner_email='' owner_name='' owner_org='' demo_runs='' no_demo='' demo_runner='' build=1
while [ $# -gt 0 ]; do
	case $1 in
	--domain) domain=${2:?--domain needs a value}; shift 2 ;;
	--email) email=${2:?--email needs a value}; shift 2 ;;
	--owner-email) owner_email=${2:?--owner-email needs a value}; shift 2 ;;
	--owner-name) owner_name=${2:?--owner-name needs a value}; shift 2 ;;
	--owner-org) owner_org=${2:?--owner-org needs a value}; shift 2 ;;
	--demo-runs) demo_runs=${2:?--demo-runs needs a directory}; shift 2 ;;
	--no-demo) no_demo=1; shift ;;
	--demo-runner) demo_runner=1; shift ;;
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
[ -n "$(env_get HEIMDALL_BENCH_DIR)" ] || env_set HEIMDALL_BENCH_DIR "$BENCH_DIR"
[ -z "$domain" ] || env_set HEIMDALL_DOMAIN "$domain"
[ -z "$email" ] || env_set HEIMDALL_ACME_EMAIL "$email"
perm=$(stat -c %a "$ENV_FILE")
[ "$perm" = 600 ] || { log "tightening $ENV_FILE from $perm to 600"; chmod 600 "$ENV_FILE"; }
require_configured
domain=$(env_get HEIMDALL_DOMAIN)
if [ -n "$demo_runner" ]; then
	runner_dir=$(env_get HEIMDALL_RUNNER_DIR)
	runner_dir=${runner_dir:-/var/lib/heimdall-runner}
	[ -d "$runner_dir" ] || die "create the demo runner's data folder first: sudo install -d -o 10001 -g 10001 -m 750 $runner_dir"
fi

# 2. Images.
if [ -z "$build" ]; then
	log "--no-build: using the images already present"
elif [ -n "$demo_runner" ]; then
	log "building images (first build takes a few minutes: npm ci, vite, go build)"
	HEIMDALL_PROFILES=demo-runner hc build heimdall demo-runner
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

# 5. Demo organization: opt-in, from the operator's own sealed runs only.
if [ -n "$no_demo" ] && [ -n "$demo_runs" ]; then
	die "--no-demo and --demo-runs contradict each other"
fi
if [ -n "$demo_runs" ]; then
	[ -d "$demo_runs" ] || die "demo runs folder $demo_runs does not exist"
	demo_runs=$(cd "$demo_runs" && pwd)
	case $demo_runs/ in
	"$(cd "$BENCH_DIR" && pwd)/testdata/"*) die "$demo_runs is Heimdall's test data, not your runs; a hosted install never imports it" ;;
	esac
	if bench_has org; then
		# The owner also owns the demo org, so they can create its runner token.
		org_args=(org create -slug demo -name "Demo" -public-read)
		[ -z "$owner_email" ] || org_args+=(-owner "$owner_email")
		if out=$(bench -- "${org_args[@]}" 2>&1); then
			log "demo organization created"
		elif printf '%s' "$out" | grep -qi 'exist'; then
			log "demo organization exists"
			# org create adds the owner only to an org it creates. A demo made
			# by an earlier run without --owner-email has no admin, so grant the
			# owner membership here (idempotent: an existing member is kept).
			if [ -n "$owner_email" ]; then
				if out=$(bench -- org member add -org demo -email "$owner_email" -role owner 2>&1); then
					log "$owner_email is an owner of the demo organization"
				elif printf '%s' "$out" | grep -qi 'already'; then
					log "$owner_email is already a member of the demo organization"
				elif printf '%s' "$out" | grep -qi 'unknown org command'; then
					log "warning: this build has no 'org member add'; $owner_email may not administer the demo organization (docs/hosting.md, Bootstrap)"
				else
					printf '%s\n' "$out" >&2
					die "adding $owner_email to the demo organization failed"
				fi
			fi
		else
			printf '%s\n' "$out" >&2
			die "creating the demo organization failed"
		fi
		log "importing sealed runs from $demo_runs into the demo organization (seals are verified)"
		bench -v "$demo_runs:/import:ro" -- org import -org demo -from /import
	else
		log "this build has no 'org' command; demo organization skipped"
	fi
fi

# 6. Everything up. The demo runner joins only once its token is set.
if [ -n "$demo_runner" ] && [ -n "$(env_get HEIMDALL_DEMO_RUNNER_TOKEN)" ]; then
	export HEIMDALL_PROFILES=${HEIMDALL_PROFILES:+$HEIMDALL_PROFILES,}demo-runner
fi
log "starting the stack"
hc up -d --wait
public_url=${HEIMDALL_PUBLIC_URL:-$(env_get HEIMDALL_PUBLIC_URL)}
log "Heimdall is up: ${public_url:-https://$domain}/"
log "status: docker compose -f $COMPOSE_FILE --env-file $ENV_FILE ps   logs: ... logs -f heimdall caddy"
if [ -n "$demo_runner" ] && [ -z "$(env_get HEIMDALL_DEMO_RUNNER_TOKEN)" ]; then
	log "demo runner: create a runner token for org 'demo', pool 'demo', set HEIMDALL_DEMO_RUNNER_TOKEN in $ENV_FILE, then run this script again with --demo-runner"
fi
