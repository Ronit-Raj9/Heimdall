#!/usr/bin/env bash
# Local smoke test of the hosted stack (docs/hosting.md, Smoke test). It
# brings Caddy, Postgres and the server up as a separate compose project on
# spare loopback ports, with Caddy's internal CA for heimdall.localhost, then:
#
#   - waits for /readyz inside the server container (database, migrations,
#     runs root)
#   - checks HTTPS through Caddy with the internal CA's root certificate:
#     /healthz, HSTS and the security headers, /readyz kept internal, the
#     HTTP to HTTPS redirect, an oversized body refused, Postgres and the
#     server not published
#   - fetches the UI shell and one of its assets over HTTPS
#   - signs up through the API, reads the session, creates an organization
#     and lists it
#   - creates the public demo organization and imports the recorded runs of
#     bench/testdata/recorded into it with the CLI (bootstrap.sh's commands),
#     then reads them anonymously over HTTPS
#   - with --demo-runner: builds the runner image, switches the session to
#     the new organization and back, creates a runner token (pool demo) in
#     the signup organization through the API, starts the demo-runner
#     profile (Bifrost, its Postgres, the fault proxy and the runner
#     container), waits until the server lists the runner, creates and
#     starts a two-slot smoke experiment (toy agent, scripted model, no
#     spend) for pool demo, waits until the runner has leased, run and
#     uploaded its slots and the server has sealed and analyzed them, reads
#     the verdict over HTTPS (gateway_hygiene and gateway_versions printed),
#     then regrades it as a job and reads the regraded verdict
#   - with --s3: a local S3 endpoint (SeaweedFS, pinned) as the evidence
#     mirror; backup.sh then uploads a database dump and mirrors the demo
#     experiments, and `evidence list` must show both
#   - tears everything down (volumes included), also on failure; only a
#     project this invocation created is ever torn down
#
#   deploy/hosted/smoke.sh [--keep] [--no-build] [--demo-runner] [--s3]
#
# Environment: SMOKE_HTTPS_PORT (default 47443), SMOKE_HTTP_PORT (47080),
# SMOKE_PROJECT (default heimdall-smoke-<UTC time>-<pid>, unique per
# invocation; a name that already has containers, volumes or networks is
# refused), SMOKE_IMAGE (heimdall/server:smoke),
# SMOKE_RUNNER_IMAGE (heimdall/runner:smoke): separate from the production
# tags, so a smoke run on a live host never replaces its images. Needs
# docker, curl, jq and openssl.
set -euo pipefail
# shellcheck source=lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

keep='' build=1 demo_runner='' s3=''
while [ $# -gt 0 ]; do
	case $1 in
	--keep) keep=1; shift ;;
	--no-build) build=; shift ;;
	--demo-runner) demo_runner=1; shift ;;
	--s3) s3=1; shift ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done
need docker curl jq openssl

https_port=${SMOKE_HTTPS_PORT:-47443}
http_port=${SMOKE_HTTP_PORT:-47080}
export HEIMDALL_PROJECT=${SMOKE_PROJECT:-heimdall-smoke-$(date -u +%Y%m%d%H%M%S)-$$}
domain=heimdall.localhost
origin=https://$domain:$https_port

# Never collide with the gateway stack or the ports other work uses.
[ "$HEIMDALL_PROJECT" != bifrost-bench ] || die "refusing the compose project name bifrost-bench"
for p in "$https_port" "$http_port"; do
	case $p in 80 | 443 | 5432 | 8090 | 809[2-7] | 18080 | 19101) die "port $p is reserved; pick another SMOKE_*_PORT" ;; esac
	if (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then die "port $p is already in use on 127.0.0.1"; fi
done
# Teardown deletes the project's volumes, so it is armed only for a project
# that has nothing yet: a retained smoke project or any other stack under
# this name is never touched.
label=com.docker.compose.project=$HEIMDALL_PROJECT
if [ -n "$(docker ps -aq --filter "label=$label")" ] ||
	[ -n "$(docker volume ls -q --filter "label=$label")" ] ||
	[ -n "$(docker network ls -q --filter "label=$label")" ] ||
	docker network inspect "$HEIMDALL_PROJECT-evalnet" >/dev/null 2>&1; then
	die "compose project $HEIMDALL_PROJECT already has containers, volumes or networks; pick another SMOKE_PROJECT (a --keep project is removed with the command it printed)"
fi

work=$(mktemp -d)
# No provider key ever reaches this stack's fault proxy, whatever the
# calling shell exports: compose prefers the shell's values to the env file.
export ANTHROPIC_API_KEY='' OPENAI_API_KEY=''
export HEIMDALL_ENV_FILE=$work/smoke.env
ENV_FILE=$HEIMDALL_ENV_FILE
export HEIMDALL_EVAL_NETWORK=$HEIMDALL_PROJECT-evalnet
cat >"$HEIMDALL_ENV_FILE" <<EOF
HEIMDALL_DOMAIN=$domain
HEIMDALL_PUBLIC_URL=$origin
HEIMDALL_ACME_EMAIL=smoke@example.com
HEIMDALL_SIGNUP=invite
HEIMDALL_PG_PASSWORD=$(random_hex)
HEIMDALL_HTTP_PUBLISH=127.0.0.1:$http_port
HEIMDALL_HTTPS_PUBLISH=127.0.0.1:$https_port
HEIMDALL_EVAL_NETWORK=$HEIMDALL_EVAL_NETWORK
HEIMDALL_BENCH_DIR=$BENCH_DIR
HEIMDALL_IMAGE=${SMOKE_IMAGE:-heimdall/server:smoke}
HEIMDALL_RUNNER_IMAGE=${SMOKE_RUNNER_IMAGE:-heimdall/runner:smoke}
EOF
chmod 600 "$HEIMDALL_ENV_FILE"
if [ -n "$s3" ]; then
	# SeaweedFS 4.54 as an S3-compatible endpoint, on the edge network only.
	cat >"$work/s3.override.yml" <<'YAML'
services:
  seaweed:
    image: chrislusf/seaweedfs:latest@sha256:4b42c212543a0acd4c0e675215ecdcaf62a186ef98def76c7e12701871222d15
    command: ["server", "-dir=/data", "-s3", "-s3.port=8333", "-volume.max=0", "-master.volumeSizeLimitMB=64"]
    networks: [edge]
    tmpfs: [/data]
YAML
	export HEIMDALL_COMPOSE_OVERRIDE=$work/s3.override.yml
	{
		printf 'HEIMDALL_S3_BUCKET=heimdall-evidence\nHEIMDALL_S3_REGION=us-east-1\n'
		printf 'HEIMDALL_S3_ENDPOINT=http://seaweed:8333\nHEIMDALL_S3_ACCESS_KEY=smoke\n'
		printf 'HEIMDALL_S3_SECRET_KEY=%s\n' "$(random_hex)"
	} >>"$HEIMDALL_ENV_FILE"
fi
runner_dir=''
if [ -n "$demo_runner" ]; then
	# The runner container (uid 10001) writes here at the same path as on
	# the host; a throwaway folder, so open permissions are fine.
	runner_dir=$work/runner
	mkdir -m 0777 "$runner_dir"
	chmod 0711 "$work"
	{
		printf 'HEIMDALL_RUNNER_DIR=%s\n' "$runner_dir"
		printf 'BIFROST_SETUP_TOKEN=%s\n' "$(random_hex)"
		printf 'BIFROST_PG_PASSWORD=%s\n' "$(random_hex)"
		printf 'BENCH_FAULTPROXY_TOKEN=%s\n' "$(random_hex)"
	} >>"$HEIMDALL_ENV_FILE"
	export HEIMDALL_PROFILES=demo-runner
fi

teardown() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		log "FAILED (exit $rc); last server and caddy logs:"
		hc logs --no-color --tail 40 heimdall caddy >&2 || true
		[ -z "$demo_runner" ] || hc logs --no-color --tail 40 demo-runner bifrost faultproxy >&2 || true
	fi
	if [ -n "$keep" ]; then
		log "--keep: stack left running as project $HEIMDALL_PROJECT; env file $HEIMDALL_ENV_FILE"
		log "tear down: HEIMDALL_ENV_FILE=$HEIMDALL_ENV_FILE HEIMDALL_PROJECT=$HEIMDALL_PROJECT bash -c '. $HOSTED_DIR/lib.sh; hc down -v --remove-orphans'"
	else
		if [ -n "$runner_dir" ]; then
			# Run folders written by uid 10001 (sealed ones read-only) are
			# removed from inside, as root of a throwaway container.
			docker run --rm --user 0:0 -v "$runner_dir:$runner_dir" --entrypoint /bin/sh "${SMOKE_IMAGE:-heimdall/server:smoke}" \
				-c "rm -rf '$runner_dir'" >/dev/null 2>&1 || true
		fi
		hc down -v --remove-orphans >/dev/null 2>&1 || true
		rm -rf "$work" || log "could not remove $work"
	fi
	exit "$rc"
}
trap teardown EXIT
# A signal ends the run through the EXIT trap, so the stack still comes down.
trap 'exit 143' TERM
trap 'exit 130' INT

pass=0
ok() {
	pass=$((pass + 1))
	printf '[smoke] ok   %s\n' "$*" >&2
}
fail() {
	printf '[smoke] FAIL %s\n' "$*" >&2
	exit 1
}

if [ -n "$build" ]; then
	log "building the server image (log: $work/build.log)"
	services=(heimdall)
	[ -z "$demo_runner" ] || services+=(demo-runner)
	hc build "${services[@]}" >"$work/build.log" 2>&1 || { tail -n 40 "$work/build.log" >&2; fail "image build"; }
	ok "images built: ${services[*]}"
fi

if [ -n "$s3" ]; then
	log "starting the local S3 endpoint and creating the bucket (the mirror never creates one)"
	hc up -d seaweed >>"$work/compose.log" 2>&1 || { cat "$work/compose.log" >&2; fail "SeaweedFS did not start"; }
	made=''
	for _ in $(seq 1 30); do
		echo "s3.bucket.create -name heimdall-evidence" | hc exec -T seaweed weed shell >/dev/null 2>&1 || true
		if echo "s3.bucket.list" | hc exec -T seaweed weed shell 2>/dev/null | grep -q heimdall-evidence; then made=1 && break; fi
		sleep 2
	done
	[ -n "$made" ] || fail "could not create the S3 bucket"
	ok "S3 endpoint up, bucket heimdall-evidence created"
fi
log "starting Postgres"
hc up -d --wait postgres >>"$work/compose.log" 2>&1 || { cat "$work/compose.log" >&2; fail "Postgres did not become healthy"; }
if bench_has db; then
	bench -- db migrate >&2
	ok "db migrate"
else
	log "no 'db' command in this build; relying on the server to migrate"
fi

log "starting the server and Caddy on 127.0.0.1:$https_port (https) and :$http_port (http)"
hc up -d --wait --wait-timeout 180 caddy heimdall postgres >>"$work/compose.log" 2>&1 || { cat "$work/compose.log" >&2; fail "the stack did not become healthy"; }
ok "Caddy, server and Postgres healthy"

# Readiness inside the container (Caddy does not expose /readyz).
for _ in $(seq 1 60); do
	if ready=$(hc exec -T heimdall wget -q -O - http://127.0.0.1:8090/readyz 2>/dev/null); then break; fi
	sleep 1
done
[ -n "${ready:-}" ] || fail "/readyz never answered 200"
printf '%s\n' "$ready" | jq -e '.status == "ready"' >/dev/null || fail "/readyz: $ready"
ok "/readyz inside the container: $(printf '%s' "$ready" | jq -c '[.checks[] | "\(.name)=\(.ok)"]')"

# Caddy's internal CA root, so every request below verifies the certificate.
ca=$work/caddy-root.crt
for _ in $(seq 1 30); do
	hc exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >"$ca" 2>/dev/null && [ -s "$ca" ] && break
	sleep 1
done
[ -s "$ca" ] || fail "Caddy's internal CA root certificate never appeared"
ok "internal CA root: $(openssl x509 -in "$ca" -noout -subject | sed 's/^subject=//')"

cookies=$work/cookies.txt
c() { curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" --resolve "$domain:$http_port:127.0.0.1" -b "$cookies" -c "$cookies" "$@"; }

hdr=$work/headers.txt
code=$(c -o /dev/null -D "$hdr" -w '%{http_code}' "$origin/healthz")
[ "$code" = 200 ] || fail "GET /healthz over HTTPS answered $code"
ok "GET /healthz over HTTPS (certificate verified against the internal CA)"
grep -qi '^strict-transport-security: max-age=31536000' "$hdr" || fail "no HSTS header"
grep -qi '^x-content-type-options: nosniff' "$hdr" || fail "no X-Content-Type-Options"
grep -qi '^content-security-policy:' "$hdr" || fail "no Content-Security-Policy"
if grep -qi '^server:' "$hdr"; then fail "Server header present"; fi
[ "$(grep -ci '^x-content-type-options:' "$hdr")" = 1 ] || fail "X-Content-Type-Options duplicated"
ok "HSTS, CSP, nosniff present once; no Server header"

code=$(c -o /dev/null -w '%{http_code}' "$origin/readyz")
[ "$code" = 404 ] || fail "/readyz through Caddy answered $code, want 404"
ok "/readyz not exposed through Caddy"

code=$(c -o /dev/null -w '%{http_code}' "http://$domain:$http_port/")
case $code in 301 | 302 | 307 | 308) ok "HTTP answers $code (redirect to HTTPS)" ;; *) fail "HTTP answered $code, want a redirect" ;; esac

code=$(head -c 3145728 /dev/zero | c -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/octet-stream' --data-binary @- "$origin/api/bench/auth/login")
[ "$code" = 413 ] || fail "3 MiB body answered $code, want 413"
ok "3 MiB request body refused with 413"

published=$(hc ps --format '{{.Service}} {{.Ports}}' | grep -- '->' | grep -v '^caddy ' || true)
[ -z "$published" ] || fail "services other than Caddy publish ports: $published"
ok "only Caddy publishes ports; Postgres and the server are internal"

# UI shell and one asset.
index=$work/index.html
code=$(c -o "$index" -D "$hdr" -w '%{http_code}' "$origin/")
[ "$code" = 200 ] || fail "GET / answered $code"
grep -qi '^content-type: text/html' "$hdr" || fail "GET / is not HTML"
asset=$(grep -o 'src="/assets/[^"]*\.js"' "$index" | head -n 1 | sed 's/^src="//; s/"$//')
[ -n "$asset" ] || fail "no script asset in the UI shell (was the UI built into the image?)"
code=$(c -o /dev/null -D "$hdr" -w '%{http_code}' "$origin$asset")
[ "$code" = 200 ] || fail "GET $asset answered $code"
grep -qi '^content-type: text/javascript\|^content-type: application/javascript' "$hdr" || fail "$asset is not JavaScript"
ok "UI shell and $asset over HTTPS"

# Hosted mode is on, and an empty install takes its first signup as the
# owner even with signup: invite (the production default).
info=$(c "$origin/api/bench/auth/config")
[ "$(printf '%s' "$info" | jq -r .auth_mode)" = hosted ] || fail "auth mode is not hosted: $info"
ok "GET /api/bench/auth/config: hosted, signup $(printf '%s' "$info" | jq -r .signup), bootstrap $(printf '%s' "$info" | jq -r '.bootstrap // false')"

# Sign up, read the session, create an organization.
email="smoke-$(openssl rand -hex 4)@example.com"
password="Smoke-$(openssl rand -hex 12)"
body=$(jq -nc --arg e "$email" --arg p "$password" '{email: $e, password: $p, name: "Smoke Test"}')
resp=$(c -X POST -H 'Content-Type: application/json' -H "Origin: $origin" -w '\n%{http_code}' --data "$body" "$origin/api/bench/auth/signup")
code=${resp##*$'\n'}
resp=${resp%$'\n'*}
case $code in 200 | 201) ;; *) fail "POST /api/bench/auth/signup answered $code: $resp" ;; esac
grep -q 'heimdall' "$cookies" || fail "signup set no session cookie"
ok "signup $email (session cookie set)"

me=$(c -H "Origin: $origin" "$origin/api/bench/auth/me")
csrf=$(printf '%s' "$me" | jq -r '.csrf // .session.csrf // empty')
[ -n "$csrf" ] || csrf=$(printf '%s' "$resp" | jq -r '.csrf // .session.csrf // empty')
[ -n "$csrf" ] || fail "no CSRF token in /auth/me or the signup answer: $me"
[ "$(printf '%s' "$me" | jq -r '.user.email // .email // empty')" = "$email" ] || fail "/auth/me does not name $email: $me"
ok "GET /api/bench/auth/me names the new user; CSRF token present"

slug="smoke-$(openssl rand -hex 3)"
resp=$(c -X POST -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" -w '\n%{http_code}' \
	--data "$(jq -nc --arg s "$slug" '{slug: $s, name: "Smoke Org"}')" "$origin/api/bench/orgs")
code=${resp##*$'\n'}
resp=${resp%$'\n'*}
case $code in 200 | 201) ;; *) fail "POST /api/bench/orgs answered $code: $resp" ;; esac
org_id=$(printf '%s' "$resp" | jq -r '.id // .org.id // empty')
ok "created organization $slug ($org_id)"

code=$(c -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H "Origin: $origin" \
	--data '{"slug":"no-csrf","name":"x"}' "$origin/api/bench/orgs")
case $code in 401 | 403) ok "cookie write without the CSRF header refused ($code)" ;; *) fail "write without CSRF answered $code" ;; esac

orgs=$(c -H "Origin: $origin" "$origin/api/bench/orgs")
printf '%s' "$orgs" | grep -q "\"$slug\"" || fail "GET /api/bench/orgs does not list $slug: $orgs"
ok "GET /api/bench/orgs lists $slug"

# With signup: invite, a second stranger is refused.
code=$(curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" -o /dev/null -w '%{http_code}' -X POST \
	-H 'Content-Type: application/json' -H "Origin: $origin" \
	--data "$(jq -nc --arg p "$password" '{email: "stranger@example.com", password: $p}')" "$origin/api/bench/auth/signup")
case $code in 401 | 403) ok "second signup without an invitation refused ($code)" ;; *) fail "second signup answered $code" ;; esac

# The public demo organization, as bootstrap.sh makes it: org create with
# public read, org import of the recorded runs (seals verified), then an
# anonymous read over HTTPS.
out=$(bench -- org create -slug demo -name Demo -public-read 2>&1) || fail "org create demo: $out"
ok "org create demo -public-read"
out=$(bench -v "$BENCH_DIR/testdata/recorded:/import:ro" -- org import -org demo -from /import 2>&1) || fail "org import: $out"
ok "org import: $(printf '%s\n' "$out" | tail -n 1)"
anon=$(curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" "$origin/api/bench/experiments")
for e in e_pilot_mixed_nc93 e_smoke_sdoj; do
	printf '%s' "$anon" | jq -e --arg e "$e" '[.experiments[]?.exp_id] | index($e) != null' >/dev/null ||
		fail "anonymous GET /api/bench/experiments does not list $e: ${anon:0:300}"
done
ok "anonymous HTTPS read of the demo org: $(printf '%s' "$anon" | jq -c '[.experiments[]? | "\(.exp_id) \(.state) \(.slots.done)/\(.slots.total)"]')"
code=$(curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" -o /dev/null -w '%{http_code}' -X POST \
	-H 'Content-Type: application/yaml' -H "Origin: $origin" --data-binary 'name: x' "$origin/api/bench/experiments")
case $code in 401 | 403) ok "anonymous write to the demo org refused ($code)" ;; *) fail "anonymous POST /api/bench/experiments answered $code" ;; esac

if [ -n "$s3" ]; then
	HEIMDALL_BACKUP_DIR=$work/backups "$HOSTED_DIR/backup.sh" >"$work/backup.log" 2>&1 || { tail -n 20 "$work/backup.log" >&2; fail "backup.sh with the S3 mirror"; }
	ls "$work"/backups/heimdall-db-*.dump >/dev/null 2>&1 || fail "backup.sh wrote no local database dump"
	ok "backup.sh: local pg_dump, then evidence backup to the bucket"
	list=$(hc exec -T heimdall bifrost-bench evidence list 2>&1) || fail "evidence list: $list"
	for e in e_pilot_mixed_nc93 e_smoke_sdoj; do
		printf '%s\n' "$list" | grep -E "$e +live +true" >/dev/null || fail "$e is not mirrored: $list"
	done
	printf '%s\n' "$list" | grep -q '\.pgdump' || fail "no database backup in the bucket: $list"
	ok "evidence list: both demo experiments mirrored, a database backup in _backups/db/"
fi

if [ -n "$demo_runner" ]; then
	# The experiment runs in the organization signup created (seeded with
	# the coding-core suite). Switching to the new one and back checks
	# switch-org on the way.
	home_org=$(c -H "Origin: $origin" "$origin/api/bench/auth/me" | jq -r '.current_org // empty')
	[ -n "$home_org" ] || fail "/auth/me names no current organization"
	for target in "$slug" "$home_org"; do
		code=$(c -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" \
			--data "$(jq -nc --arg s "$target" '{org: $s}')" "$origin/api/bench/auth/switch-org")
		[ "$code" = 200 ] || fail "POST /api/bench/auth/switch-org to $target answered $code"
		csrf=$(c -H "Origin: $origin" "$origin/api/bench/auth/me" | jq -r '.csrf // empty')
	done
	[ "$(c -H "Origin: $origin" "$origin/api/bench/auth/me" | jq -r '.current_org // empty')" = "$home_org" ] || fail "switch-org back to $home_org did not stick"
	ok "switch-org to $slug and back to the signup organization $home_org"
	resp=$(c -X POST -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" -w '\n%{http_code}' \
		--data '{"pool":"demo","name":"smoke demo runner"}' "$origin/api/bench/runners/tokens")
	code=${resp##*$'\n'}
	resp=${resp%$'\n'*}
	[ "$code" = 201 ] || fail "POST /api/bench/runners/tokens answered $code: $resp"
	token=$(printf '%s' "$resp" | jq -r .token)
	case $token in hmr_*) ;; *) fail "runner token does not look like hmr_...: ${token:0:8}" ;; esac
	env_set HEIMDALL_DEMO_RUNNER_TOKEN "$token"
	ok "runner token created through the API (pool demo, shown once)"
	hc up -d --wait --wait-timeout 300 >>"$work/compose.log" 2>&1 || { cat "$work/compose.log" >&2; fail "the demo-runner profile did not start"; }
	ok "demo-runner profile up: Bifrost, its Postgres, fault proxy, runner"
	state=''
	for _ in $(seq 1 60); do
		state=$(c -H "Origin: $origin" "$origin/api/bench/runners" | jq -r '.runners[]? | select(.name == "demo-runner") | .state' 2>/dev/null || true)
		[ -n "$state" ] && break
		sleep 2
	done
	[ -n "$state" ] || fail "the server never listed the demo runner"
	ok "server lists runner demo-runner (state $state)"

	# One experiment through the runner, end to end. The organization's
	# project has the suite but no agent configs yet: the toy agent from the
	# image goes in through the container (there is no agent upload API).
	hc exec -T heimdall sh -c "cp /srv/heimdall/agents/toy.yaml /var/lib/heimdall/runs/projects/$home_org/agents/toy.yaml" ||
		fail "could not seed agents/toy.yaml into the organization's project"
	exp_yaml='name: hosted-smoke
smoke: true
suite: coding-core@1.0.0
base: agents/toy.yaml
arms:
  A: {label: toy}
  B: {label: toy with a shorter wall clock, set: {budgets.wall_s: 600}}
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: [t01], trials: 1}
budget: {total_usd: 0}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261118
concurrency: 1
runner: {pool: demo}'
	resp=$(c -X POST -H 'Content-Type: application/yaml' -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" -w '\n%{http_code}' \
		--data-binary "$exp_yaml" "$origin/api/bench/experiments")
	code=${resp##*$'\n'}
	resp=${resp%$'\n'*}
	[ "$code" = 201 ] || fail "POST /api/bench/experiments answered $code: $resp"
	exp_id=$(printf '%s' "$resp" | jq -r .exp_id)
	ok "experiment $exp_id planned and frozen on the server (pool demo)"
	code=$(c -o /dev/null -w '%{http_code}' -X POST -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" "$origin/api/bench/experiments/$exp_id/start")
	case $code in 200 | 202) ;; *) fail "POST /api/bench/experiments/$exp_id/start answered $code" ;; esac
	ok "experiment started"
	st=''
	for _ in $(seq 1 "${SMOKE_RUN_WAIT:-180}"); do
		st=$(c -H "Origin: $origin" "$origin/api/bench/experiments/$exp_id" || true)
		case $(printf '%s' "$st" | jq -r '.state // empty' 2>/dev/null) in done | failed | cancelled) break ;; esac
		sleep 5
	done
	printf '%s' "$st" | jq -c '{state, slots, spent_usd}' >&2 || true
	done_slots=$(printf '%s' "$st" | jq -r '.slots.done // 0' 2>/dev/null || echo 0)
	[ "$(printf '%s' "$st" | jq -r '.state // empty' 2>/dev/null)" = "done" ] || fail "experiment $exp_id did not finish: $st"
	[ "${done_slots:-0}" -ge 1 ] || fail "no slot of $exp_id finished: $st"
	ok "runner leased, ran and uploaded $done_slots slot(s); the server sealed them and analyzed $exp_id"

	verdict=$(c -H "Origin: $origin" -w '\n%{http_code}' "$origin/api/bench/experiments/$exp_id/verdict")
	code=${verdict##*$'\n'}
	verdict=${verdict%$'\n'*}
	[ "$code" = 200 ] || fail "GET /api/bench/experiments/$exp_id/verdict answered $code: ${verdict:0:300}"
	ok "verdict over HTTPS: $(printf '%s' "$verdict" | jq -c '{decision: .verdict.decision, action: .verdict.action, analysis: .analysis_id}')"
	for inst in gateway_hygiene gateway_versions runner_images; do
		row=$(printf '%s' "$verdict" | jq -c --arg n "$inst" '.verdict.instruments[]? | select(.name == $n)')
		[ -n "$row" ] || fail "verdict has no $inst instrument"
		ok "instrument $row"
	done

	resp=$(c -X POST -H 'Content-Type: application/json' -H "Origin: $origin" -H "X-Heimdall-CSRF: $csrf" -w '\n%{http_code}' \
		--data '{"note":"hosted smoke regrade"}' "$origin/api/bench/experiments/$exp_id/regrade")
	code=${resp##*$'\n'}
	resp=${resp%$'\n'*}
	[ "$code" = 202 ] || fail "POST /api/bench/experiments/$exp_id/regrade answered $code: $resp"
	job=$(printf '%s' "$resp" | jq -r .id)
	js=''
	for _ in $(seq 1 120); do
		js=$(c -H "Origin: $origin" "$origin/api/bench/jobs/$job" | jq -r '.status // empty' 2>/dev/null || true)
		case $js in succeeded | failed | cancelled) break ;; esac
		sleep 2
	done
	[ "$js" = succeeded ] || fail "regrade job $job ended $js: $(c -H "Origin: $origin" "$origin/api/bench/jobs/$job")"
	rg=$(c -H "Origin: $origin" "$origin/api/bench/jobs/$job" | jq -r '.params.id // .result.id // empty')
	code=$(c -o /dev/null -w '%{http_code}' -H "Origin: $origin" "$origin/api/bench/experiments/$exp_id/verdict?grade_source=$rg")
	[ "$code" = 200 ] || fail "regraded verdict (grade_source=$rg) answered $code"
	ok "regrade job $job succeeded in the signup organization; verdict with grade_source=$rg over HTTPS"
fi

log "smoke test passed: $pass checks"
