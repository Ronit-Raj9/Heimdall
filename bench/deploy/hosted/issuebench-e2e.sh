#!/usr/bin/env bash
# Real end to end run of From issues on a hosted-like stack
# (audit/ISSUEWORKER_CONTRACT.md, lane W12; usage notes in
# deploy/hosted/issuebench-e2e.md). It brings the hosted stack up with the
# shared-runner profile as a separate compose project on spare loopback
# ports (Caddy's internal CA for heimdall.localhost, like smoke.sh), then:
#
#   1. signs up the owner, creates an organization and an API token
#   2. makes the owner a server admin (bifrost-bench admin grant) and turns
#      the shared pool on for the organization (PUT /admin/quotas/{id})
#   3. adds the provider key on the models route (POST /models/keys)
#   4. waits for GET /issuebench/readiness: worker_online and gateway
#   5. scans the repository (POST /issuebench/runs), starts the run with
#      consent and the builder model, and follows the log and each issue's
#      status, stage and job id
#   6. accepts every ready issue, waits for the benchmark, then follows the
#      suite.lint, suite.lock and suite.admit jobs to the end
#   7. prints a summary and PASS (at least one emitted task with review
#      pending whose admit job succeeded) or FAIL <reason>
#
#   deploy/hosted/issuebench-e2e.sh [--check-only] [--keep] [--no-build]
#
# --check-only stops after the readiness check (step 4); no model is called.
# --keep leaves the stack running and prints the command that removes it.
# --no-build uses the images already tagged (ISSUEBENCH_IMAGE and
# ISSUEBENCH_RUNNER_IMAGE).
#
# Environment:
#   ISSUEBENCH_PROVIDER, ISSUEBENCH_MODEL   builder model (required unless --check-only)
#   ISSUEBENCH_PROVIDER_KEY                 that provider's key (required unless --check-only)
#   GITHUB_TOKEN                            GitHub token for the scan (required unless --check-only)
#   ISSUEBENCH_REPO      default marshmallow-code/marshmallow
#   ISSUEBENCH_LIMIT     merged pull requests to scan (not issues), default 25
#   ISSUEBENCH_MAX_USD   dollar cap per build, default 3
#   ISSUEBENCH_TIMEOUT   whole run, e.g. 90m or 4h, default 4h
#   HEIMDALL_SHARED_ALLOW_RUNC=1  a dev box without gVisor: the shared
#                        worker runs under runc and the org is set to runc
#   ISSUEBENCH_HTTPS_PORT (default 47543), ISSUEBENCH_HTTP_PORT (47180),
#   ISSUEBENCH_PROJECT (default heimdall-ibe2e-<UTC time>-<pid>),
#   ISSUEBENCH_IMAGE (heimdall/server:ib-e2e),
#   ISSUEBENCH_RUNNER_IMAGE (heimdall/runner:ib-e2e)
#
# Secrets are never echoed: the provider key reaches curl on stdin, the API
# token through a header file (mode 0600), the GitHub token through the
# compose environment, and every printed line has them replaced with
# [redacted]. Needs docker, curl, jq and openssl.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

keep='' build=1 check_only=''
while [ $# -gt 0 ]; do
	case $1 in
	--keep) keep=1; shift ;;
	--no-build) build=''; shift ;;
	--check-only) check_only=1; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1 (see --help)" ;;
	esac
done

# seconds_of turns 90, 90s, 30m or 4h into seconds.
seconds_of() {
	case $1 in
	*[!0-9smh]* | '' | [smh]*) die "ISSUEBENCH_TIMEOUT must look like 3600, 90m or 4h, not '$1'" ;;
	*h) echo $((${1%h} * 3600)) ;;
	*m) echo $((${1%m} * 60)) ;;
	*s) echo "${1%s}" ;;
	*) echo "$1" ;;
	esac
}

repo=${ISSUEBENCH_REPO:-marshmallow-code/marshmallow}
limit=${ISSUEBENCH_LIMIT:-25}
max_usd=${ISSUEBENCH_MAX_USD:-3}
timeout_s=$(seconds_of "${ISSUEBENCH_TIMEOUT:-4h}")
provider=${ISSUEBENCH_PROVIDER:-}
model=${ISSUEBENCH_MODEL:-}
case $repo in */*) ;; *) die "ISSUEBENCH_REPO must be owner/name: $repo" ;; esac
case $limit in '' | *[!0-9]*) die "ISSUEBENCH_LIMIT must be a number: $limit" ;; esac
if [ -z "$check_only" ]; then
	[ -n "$provider" ] && [ -n "$model" ] || die "set ISSUEBENCH_PROVIDER and ISSUEBENCH_MODEL (or use --check-only)"
	[ -n "${ISSUEBENCH_PROVIDER_KEY:-}" ] || die "set ISSUEBENCH_PROVIDER_KEY (or use --check-only)"
	[ -n "${GITHUB_TOKEN:-}" ] || die "set GITHUB_TOKEN for the scan (or use --check-only)"
fi
need docker curl jq openssl
docker compose version >/dev/null 2>&1 || die "the Docker Compose plugin (docker compose) is required"

https_port=${ISSUEBENCH_HTTPS_PORT:-47543}
http_port=${ISSUEBENCH_HTTP_PORT:-47180}
export HEIMDALL_PROJECT=${ISSUEBENCH_PROJECT:-heimdall-ibe2e-$(date -u +%Y%m%d%H%M%S)-$$}
domain=heimdall.localhost
origin=https://$domain:$https_port
started=$(date +%s)
deadline=$((started + timeout_s))

[ "$HEIMDALL_PROJECT" != bifrost-bench ] || die "refusing the compose project name bifrost-bench"
[ "$HEIMDALL_PROJECT" != heimdall ] || die "refusing the production compose project name heimdall"
for p in "$https_port" "$http_port"; do
	case $p in 80 | 443 | 5432 | 8090 | 809[2-7] | 18080 | 19101 | 47443 | 47080) die "port $p is reserved; pick another ISSUEBENCH_*_PORT" ;; esac
	if (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then die "port $p is already in use on 127.0.0.1"; fi
done

# Secret values replaced in every printed line (redact). Values shorter
# than 8 characters are left alone, as on the server (RedactValues).
secrets=()
add_secret() { [ "${#1}" -lt 8 ] || secrets+=("$1"); }
redact() {
	local s=$1 v
	for v in ${secrets[@]+"${secrets[@]}"}; do s=${s//"$v"/[redacted]}; done
	printf '%s' "$s"
}
say() { printf '[ib-e2e] %s\n' "$(redact "$*")" >&2; }
add_secret "${ISSUEBENCH_PROVIDER_KEY:-}"
add_secret "${GITHUB_TOKEN:-}"

# The project name is claimed (a lock folder held until exit) and must have
# nothing yet: the teardown deletes its volumes, so it is armed only for a
# project this invocation created (same rules as smoke.sh).
claim=${TMPDIR:-/tmp}/heimdall-ibe2e-$HEIMDALL_PROJECT.lock
mkdir "$claim" 2>/dev/null || die "compose project $HEIMDALL_PROJECT is claimed by another run ($claim)"
trap 'rmdir "$claim" 2>/dev/null || true' EXIT
label=com.docker.compose.project=$HEIMDALL_PROJECT
inventory() {
	local out
	out=$("$@") || die "docker inventory failed ($*); refusing to arm the teardown for $HEIMDALL_PROJECT"
	[ -z "$out" ] || die "compose project $HEIMDALL_PROJECT already has containers, volumes or networks; pick another ISSUEBENCH_PROJECT"
}
inventory docker ps -aq --filter "label=$label"
inventory docker volume ls -q --filter "label=$label"
inventory docker network ls -q --filter "label=$label"
for n in evalnet buildnet rtgate; do
	inventory docker network ls -q --filter "name=^$HEIMDALL_PROJECT-$n\$"
done

# Files this script writes (cookies, token header, env file) are private;
# the folders the containers read get explicit modes below.
umask 077
work=$(mktemp -d)
# Containers run with no capabilities, so they need to traverse the work
# folder to reach the registry credentials and the runner folder in it.
chmod 0711 "$work"
# The shared worker's data folder: mounted at the same host path, written by
# the runner's uid 10001 (throwaway, under the work folder).
runner_dir=$work/runner
mkdir -p "$runner_dir"
chmod 0777 "$runner_dir"
export HEIMDALL_ENV_FILE=$work/ib-e2e.env
ENV_FILE=$HEIMDALL_ENV_FILE
export HEIMDALL_EVAL_NETWORK=$HEIMDALL_PROJECT-evalnet
export HEIMDALL_BUILD_NETWORK=$HEIMDALL_PROJECT-buildnet
export HEIMDALL_RTGATE_NETWORK=$HEIMDALL_PROJECT-rtgate
# No server-wide provider key reaches this stack, whatever the shell exports.
export ANTHROPIC_API_KEY='' OPENAI_API_KEY=''
image=${ISSUEBENCH_IMAGE:-heimdall/server:ib-e2e}
runner_image=${ISSUEBENCH_RUNNER_IMAGE:-heimdall/runner:ib-e2e}
case "$image $runner_image" in *heimdall/server:local* | *heimdall/runner:local*) die "ISSUEBENCH_IMAGE / ISSUEBENCH_RUNNER_IMAGE must not be the production :local tags" ;; esac
export HEIMDALL_IMAGE=$image HEIMDALL_RUNNER_IMAGE=$runner_image
shared_secret=$(openssl rand -hex 32)
add_secret "$shared_secret"
(
	umask 077
	cat >"$HEIMDALL_ENV_FILE" <<EOF
HEIMDALL_DOMAIN=$domain
HEIMDALL_PUBLIC_URL=$origin
HEIMDALL_ACME_EMAIL=ib-e2e@example.com
HEIMDALL_SIGNUP=invite
HEIMDALL_PG_PASSWORD=$(random_hex)
HEIMDALL_MASTER_KEY=$(openssl rand -base64 32)
HEIMDALL_PULL_SIGNING_KEY=$(random_hex)
HEIMDALL_GATEWAY_USAGE_SECRET=$(random_hex)
HEIMDALL_SHARED_RUNNER_SECRET=$shared_secret
HEIMDALL_SHARED_ALLOW_RUNC=${HEIMDALL_SHARED_ALLOW_RUNC:-}
BIFROST_SETUP_TOKEN=$(random_hex)
BIFROST_PG_PASSWORD=$(random_hex)
BENCH_FAULTPROXY_TOKEN=$(random_hex)
HEIMDALL_HTTP_PUBLISH=127.0.0.1:$http_port
HEIMDALL_HTTPS_PUBLISH=127.0.0.1:$https_port
HEIMDALL_EVAL_NETWORK=$HEIMDALL_EVAL_NETWORK
HEIMDALL_BUILD_NETWORK=$HEIMDALL_BUILD_NETWORK
HEIMDALL_RTGATE_NETWORK=$HEIMDALL_RTGATE_NETWORK
HEIMDALL_RUNNER_DIR=$runner_dir
HEIMDALL_REGISTRY_AUTH_DIR=$work/registry-auth
HEIMDALL_BENCH_DIR=$BENCH_DIR
HEIMDALL_IMAGE=$HEIMDALL_IMAGE
HEIMDALL_RUNNER_IMAGE=$HEIMDALL_RUNNER_IMAGE
BENCH_ISSUEBENCH_PROVIDER=$provider
BENCH_ISSUEBENCH_MODEL=$model
BENCH_ISSUEBENCH_MAX_USD=$max_usd
EOF
)
# The server reads GITHUB_TOKEN for the scan (ibStages.token falls back to it
# when no GitHub connection is stored: the connection is OAuth only, there is
# no route that stores a token). Compose takes the value from this process's
# environment, so it is never written to a file.
cat >"$work/ib-e2e.override.yml" <<'YAML'
services:
  heimdall:
    environment:
      GITHUB_TOKEN: ${GITHUB_TOKEN:-}
YAML
export HEIMDALL_COMPOSE_OVERRIDE=$work/ib-e2e.override.yml
export HEIMDALL_PROFILES=shared-runner
export GITHUB_TOKEN=${GITHUB_TOKEN:-}

org_id=''
teardown() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		say "exit $rc; last server and shared worker logs:"
		hc logs --no-color --tail 60 heimdall shared-runner 2>&1 | while IFS= read -r l; do redact "$l"; printf '\n'; done >&2 || true
	fi
	if [ -n "$keep" ]; then
		say "--keep: stack left running as project $HEIMDALL_PROJECT; env file $HEIMDALL_ENV_FILE (secrets inside, mode 0600)"
		say "tear down: HEIMDALL_ENV_FILE=$HEIMDALL_ENV_FILE HEIMDALL_PROJECT=$HEIMDALL_PROJECT HEIMDALL_PROFILES=shared-runner HEIMDALL_COMPOSE_OVERRIDE=$HEIMDALL_COMPOSE_OVERRIDE bash -c '. $HOSTED_DIR/lib.sh; hc down -v --remove-orphans'"
	else
		hc down -v --remove-orphans >/dev/null 2>&1 || true
		# Containers and networks the shared worker started for this run's org
		# (agents, build sessions, issue sandboxes) are not compose objects.
		if [ -n "$org_id" ]; then
			docker ps -aq --filter "label=heimdall.org=$org_id" | xargs -r docker rm -f >/dev/null 2>&1 || true
			docker network ls -q --filter "label=heimdall.org=$org_id" | xargs -r docker network rm >/dev/null 2>&1 || true
		fi
		rm -rf "$work" 2>/dev/null || say "could not remove all of $work (files the shared worker wrote as uid 10001); remove it as root"
	fi
	rmdir "$claim" 2>/dev/null || true
	exit "$rc"
}
trap teardown EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

fail() {
	say "FAIL $*"
	printf 'FAIL %s\n' "$(redact "$*")"
	exit 1
}
left() { echo $((deadline - $(date +%s))); }

# --- 1. Stack ---------------------------------------------------------------
if [ -n "$build" ]; then
	say "building the server and runner images (log: $work/build.log)"
	hc build heimdall shared-runner >"$work/build.log" 2>&1 || { tail -n 40 "$work/build.log" >&2; fail "image build"; }
fi
ensure_registry_auth
say "starting Postgres"
hc up -d --wait postgres >>"$work/compose.log" 2>&1 || { tail -n 40 "$work/compose.log" >&2; fail "Postgres did not become healthy"; }
if bench_has db; then bench -- db migrate >&2; fi
say "starting the stack with the shared-runner profile on 127.0.0.1:$https_port"
hc up -d --wait --wait-timeout 300 caddy heimdall postgres >>"$work/compose.log" 2>&1 || { tail -n 40 "$work/compose.log" >&2; fail "the stack did not become healthy"; }
# The shared worker has no health check; it starts once the server and Bifrost are healthy.
hc up -d >>"$work/compose.log" 2>&1 || { tail -n 40 "$work/compose.log" >&2; fail "the shared-runner profile did not start"; }

ca=$work/caddy-root.crt
for _ in $(seq 1 30); do
	hc exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >"$ca" 2>/dev/null && [ -s "$ca" ] && break
	sleep 1
done
[ -s "$ca" ] || fail "Caddy's internal CA root certificate never appeared"

# --- 2. Owner, organization, API token ---------------------------------------
cookies=$work/cookies.txt
auth_hdr=$work/auth.hdr
curl_base=(-sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1")
code='' body=''
# sess METHOD PATH [JSON]: a call with the session cookie (CSRF on writes).
sess() {
	local args=("${curl_base[@]}" -b "$cookies" -c "$cookies" -X "$1" -H "Origin: $origin" -o "$work/resp" -w '%{http_code}')
	[ -z "${csrf:-}" ] || args+=(-H "X-Heimdall-CSRF: $csrf")
	[ -z "${3:-}" ] || args+=(-H 'Content-Type: application/json' --data-binary "$3")
	code=$(curl "${args[@]}" "$origin$2") || code=000
	body=$(cat "$work/resp" 2>/dev/null || true)
}
# api METHOD PATH [JSON | -]: a call with the API token ("-" reads the body
# from stdin, so a secret in it never sits in argv).
api() {
	local args=("${curl_base[@]}" -X "$1" -H @"$auth_hdr" -o "$work/resp" -w '%{http_code}')
	if [ "${3:-}" = - ]; then
		args+=(-H 'Content-Type: application/json' --data-binary @-)
	elif [ -n "${3:-}" ]; then
		args+=(-H 'Content-Type: application/json' --data-binary "$3")
	fi
	code=$(curl "${args[@]}" "$origin$2") || code=000
	body=$(cat "$work/resp" 2>/dev/null || true)
}
want() {
	local c
	for c in "${@:2}"; do [ "$code" = "$c" ] && return 0; done
	fail "$1 answered $code: $(printf '%s' "$body" | head -c 400)"
}

email="ib-e2e-$(openssl rand -hex 4)@example.com"
password="Ib-$(openssl rand -hex 16)"
add_secret "$password"
csrf=''
sess POST /api/bench/auth/signup "$(jq -nc --arg e "$email" --arg p "$password" '{email: $e, password: $p, name: "Issue bench e2e"}')"
want "POST /auth/signup" 200 201
# This stack sends no email, so the owner is verified on the server (the
# same command an operator runs); POST /orgs refuses an unverified account.
out=$(bench -- user verify -email "$email" 2>&1) || fail "bifrost-bench user verify -email $email: $(printf '%s' "$out" | tail -n 3)"
sess GET /api/bench/auth/me
csrf=$(printf '%s' "$body" | jq -r '.csrf // .session.csrf // empty')
[ -n "$csrf" ] || fail "no CSRF token in /auth/me"
add_secret "$csrf"
slug="ib-$(openssl rand -hex 3)"
sess POST /api/bench/orgs "$(jq -nc --arg s "$slug" '{slug: $s, name: "Issue bench e2e"}')"
want "POST /orgs" 200 201
org_id=$(printf '%s' "$body" | jq -r '.id // .org.id // empty')
[ -n "$org_id" ] || fail "POST /orgs returned no id"
say "owner $email, organization $slug ($org_id)"

args=("${curl_base[@]}" -b "$cookies" -X POST -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" -H "X-Heimdall-Org: $org_id"
	-H 'Content-Type: application/json' --data-binary '{"name":"issuebench-e2e","expires_in_days":1}' -o "$work/resp" -w '%{http_code}')
code=$(curl "${args[@]}" "$origin/api/bench/auth/tokens") || code=000
body=$(cat "$work/resp")
want "POST /auth/tokens" 200 201
token=$(printf '%s' "$body" | jq -r '.token // empty')
[ -n "$token" ] || fail "POST /auth/tokens returned no token"
add_secret "$token"
(umask 077 && printf 'Authorization: Bearer %s\n' "$token" >"$auth_hdr")
: >"$work/resp"
body=''
unset token
say "API token created (1 day)"

# --- 3. Shared pool, provider key ---------------------------------------------
out=$(bench -- admin grant -email "$email" 2>&1) || fail "bifrost-bench admin grant -email $email: $(printf '%s' "$out" | tail -n 3)"
sess GET "/api/bench/admin/quotas/$org_id"
want "GET /admin/quotas/$org_id" 200
limits=$(printf '%s' "$body" | jq -c '.limits // {} | .shared_pool = true')
if [ -n "${HEIMDALL_SHARED_ALLOW_RUNC:-}" ]; then limits=$(printf '%s' "$limits" | jq -c '.runtime = "runc"'); fi
sess PUT "/api/bench/admin/quotas/$org_id" "$limits"
want "PUT /admin/quotas/$org_id" 200
say "shared pool on for $slug (runtime $(printf '%s' "$limits" | jq -r '.runtime // "default"'))"

if [ -n "${ISSUEBENCH_PROVIDER_KEY:-}" ]; then
	[ -n "$provider" ] || fail "ISSUEBENCH_PROVIDER_KEY is set but ISSUEBENCH_PROVIDER is empty"
	# jq reads the key from the environment and curl the body from stdin.
	jq -nc '{provider: env.ISSUEBENCH_PROVIDER, label: "issuebench-e2e", value: env.ISSUEBENCH_PROVIDER_KEY}' | api POST /api/bench/models/keys -
	want "POST /models/keys" 200 201
	say "provider key added for $provider"
fi

# --- 4. Readiness ----------------------------------------------------------------
say "waiting for a worker that takes issuebench.work and the server's gateway (10 min cap)"
ready_by=$(($(date +%s) + 600))
last=''
while :; do
	api GET /api/bench/issuebench/readiness
	if [ "$code" = 200 ]; then
		now=$(printf '%s' "$body" | jq -c '{hosted, worker_online, gateway, github, default_model}')
		[ "$now" = "$last" ] || say "readiness $now $(printf '%s' "$body" | jq -r '.message // ""')"
		last=$now
		printf '%s' "$body" | jq -e '.worker_online and .gateway' >/dev/null && break
	else
		say "readiness answered $code"
	fi
	[ "$(date +%s)" -lt "$ready_by" ] || fail "not ready after 10 min: $(printf '%s' "$body" | jq -r '.message // .' 2>/dev/null | head -c 400)"
	sleep 10
done
if [ -n "$check_only" ]; then
	printf 'PASS check-only: hosted stack ready (worker online, gateway up)\n'
	exit 0
fi

# --- 5. Scan, start, follow ------------------------------------------------------
api POST /api/bench/issuebench/runs "$(jq -nc --arg r "$repo" --argjson l "$limit" '{repo: $r, limit: $l}')"
want "POST /issuebench/runs" 200 201
run_id=$(printf '%s' "$body" | jq -r '.id')
say "scan $repo: run $run_id, $(printf '%s' "$body" | jq -r '.items | length') candidate issues"
[ "$(printf '%s' "$body" | jq -r '.items | length')" -gt 0 ] || fail "the scan found no candidate issue in $repo"
api POST "/api/bench/issuebench/runs/$run_id/start" "$(jq -nc --arg p "$provider" --arg m "$model" '{consent: true, provider: $p, model: $m}')"
if [ "$code" = 409 ]; then fail "start answered 409: $(printf '%s' "$body" | jq -r '.message // .error // .' | head -c 400)"; fi
want "POST /issuebench/runs/$run_id/start" 200 202

since=0
declare -A seen=()
state=''
while :; do
	api GET "/api/bench/issuebench/runs/$run_id?since=$since"
	if [ "$code" = 200 ]; then
		state=$body
		while IFS=$'\t' read -r seq line; do
			[ -n "$seq" ] || continue
			say "log | $line"
			since=$seq
		done < <(printf '%s' "$state" | jq -r '.log[]? | "\(.seq)\t\(.line)"')
		while IFS=$'\t' read -r n st sg jid; do
			[ -n "$n" ] || continue
			if [ "${seen[$n]:-}" != "$st/$sg/$jid" ]; then
				say "issue #$n status=$st stage=$sg job=$jid"
				seen[$n]="$st/$sg/$jid"
			fi
		done < <(printf '%s' "$state" | jq -r '.items[] | [.candidate.issue, .status, (if (.stage // "") == "" then "-" else .stage end), (if (.job_id // "") == "" then "-" else .job_id end)] | @tsv')
		rs=$(printf '%s' "$state" | jq -r .status)
		busy=$(printf '%s' "$state" | jq '[.items[] | select(.status | IN("queued", "building", "built", "validated"))] | length')
		case $rs in done | failed | canceled) [ "$busy" = 0 ] && break ;; esac
	else
		say "GET run answered $code"
	fi
	[ "$(left)" -gt 0 ] || fail "timed out after ${timeout_s}s with the run still going"
	sleep 10
done
say "run $run_id ended: $(printf '%s' "$state" | jq -r '"\(.status) \(.detail // "")"')"

# --- 6. Accept, emit, admit ---------------------------------------------------------
for n in $(printf '%s' "$state" | jq -r '.items[] | select(.status == "ready") | .candidate.issue'); do
	api POST "/api/bench/issuebench/runs/$run_id/issues/$n/review" '{"decision":"accept"}'
	if [ "$code" = 200 ]; then say "issue #$n accepted"; else say "accept issue #$n answered $code: $(printf '%s' "$body" | head -c 300)"; fi
done
api GET "/api/bench/issuebench/runs/$run_id"
state=$body
bench_id=$(printf '%s' "$state" | jq -r '.benchmark // empty')

jobs_json='[]'
if [ -n "$bench_id" ]; then
	say "benchmark $bench_id; following suite.lint, suite.lock and suite.admit"
	jobs_by=$(($(date +%s) + 300))
	while :; do
		api GET "/api/bench/jobs?target=suite:$bench_id&limit=100"
		if [ "$code" = 200 ]; then
			# The newest job of each admission kind.
			jobs_json=$(printf '%s' "$body" | jq -c '[(.jobs // []) | group_by(.kind)[] | select(.[0].kind | IN("suite.lint", "suite.lock", "suite.admit")) | max_by(.created_at) | {kind, id, status, error}]')
			n_jobs=$(printf '%s' "$jobs_json" | jq length)
			open=$(printf '%s' "$jobs_json" | jq '[.[] | select(.status | IN("queued", "running"))] | length')
			[ "$n_jobs" = 0 ] && [ "$(date +%s)" -ge "$jobs_by" ] && break
			[ "$n_jobs" = 3 ] && [ "$open" = 0 ] && break
			say "admission jobs: $(printf '%s' "$jobs_json" | jq -r 'map("\(.kind)=\(.status)") | join(" ")')"
		fi
		[ "$(left)" -gt 0 ] || break
		sleep 15
	done
fi

# Tasks of the benchmark whose task.yaml says review: pending (read inside
# the server container: the review state is not in the suites API).
pending_tasks=''
if [ -n "$bench_id" ]; then
	pending_tasks=$(hc exec -T heimdall sh -c 'find /var/lib/heimdall -path "*/benchmarks/$1/*" -name task.yaml 2>/dev/null | xargs -r grep -l "^review: *pending" 2>/dev/null' sh "$bench_id" | wc -l) || pending_tasks=0
	api GET /api/bench/suites
	if [ "$code" = 200 ]; then
		say "suite $bench_id: $(printf '%s' "$body" | jq -r --arg s "$bench_id" '.suites[] | select(.id == $s) | "\(.task_count) tasks, \(.admitted_count) admitted, self-contained: \([.tasks[] | select(.environment.dockerfile != null and (.environment.image // "") == "")] | length)"')"
	fi
fi

# --- 7. Summary -------------------------------------------------------------------
printf '\n%-8s %-10s %-10s %s\n' issue status cost reason
printf '%s' "$state" | jq -r '. as $s | .items[] | [(.candidate.issue | tostring), .status, ((.cost_usd // 0) | tostring), (.reason // "")] | @tsv' |
	while IFS=$'\t' read -r n st c r; do printf '%-8s %-10s %-10s %s\n' "#$n" "$st" "\$$c" "$(redact "$r" | head -c 160)"; done
printf 'run %s: status %s, cost $%s, benchmark %s, %s task(s) with review pending\n' "$run_id" \
	"$(printf '%s' "$state" | jq -r .status)" "$(printf '%s' "$state" | jq -r .cost_usd)" "${bench_id:-none}" "${pending_tasks:-0}"
if [ "$jobs_json" != '[]' ]; then printf 'admission: %s\n' "$(redact "$(printf '%s' "$jobs_json" | jq -r 'map("\(.kind)=\(.status)\(if .error then " (" + .error[0:120] + ")" else "" end)") | join(", ")')")"; fi

admit=$(printf '%s' "$jobs_json" | jq -r '.[] | select(.kind == "suite.admit") | .status')
if [ -z "$bench_id" ]; then
	fail "no task was emitted (no issue reached ready, or the accept failed)"
elif [ "${pending_tasks:-0}" -lt 1 ]; then
	fail "benchmark $bench_id has no task with review: pending"
elif [ "$admit" != succeeded ]; then
	fail "suite.admit for $bench_id is ${admit:-missing}"
fi
printf 'PASS %s: %s task(s) emitted with review pending, suite.admit succeeded (%ss)\n' "$bench_id" "$pending_tasks" "$(($(date +%s) - started))"
