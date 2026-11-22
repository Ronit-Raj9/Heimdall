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
#   - creates a public_read organization and imports the recorded runs of
#     bench/testdata/recorded into it with the CLI, then checks that
#     anonymous HTTPS reads are refused (hosted installs require sign-in)
#   - with --s3: a local S3 endpoint (SeaweedFS, pinned) as the evidence
#     mirror; backup.sh then uploads a database dump and mirrors the demo
#     experiments, and `evidence list` must show both
#   - tears everything down (volumes included), also on failure; only a
#     project this invocation created is ever torn down
#
#   deploy/hosted/smoke.sh [--keep] [--no-build] [--s3]
#
# --demo-runner is retired with the demo-runner service (hosted installs
# have no demo) and is ignored.
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

keep='' build=1 s3=''
while [ $# -gt 0 ]; do
	case $1 in
	--keep) keep=1; shift ;;
	--no-build) build=; shift ;;
	--demo-runner) log "--demo-runner is retired (no demo runner on hosted installs); ignored"; shift ;;
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
# this name is never touched. The name is claimed first (a lock folder held
# until exit), so two invocations given the same SMOKE_PROJECT cannot both
# pass the empty check, and an inventory command that fails is treated as
# "not empty": a Docker error never authorizes the teardown.
claim=${TMPDIR:-/tmp}/heimdall-smoke-$HEIMDALL_PROJECT.lock
mkdir "$claim" 2>/dev/null || die "compose project $HEIMDALL_PROJECT is claimed by another smoke run ($claim); pick another SMOKE_PROJECT"
trap 'rmdir "$claim" 2>/dev/null || true' EXIT
label=com.docker.compose.project=$HEIMDALL_PROJECT
inventory() {
	local out
	out=$("$@") || die "docker inventory failed ($*); refusing to arm the teardown for $HEIMDALL_PROJECT"
	[ -z "$out" ] || die "compose project $HEIMDALL_PROJECT already has containers, volumes or networks; pick another SMOKE_PROJECT (a --keep project is removed with the command it printed)"
}
inventory docker ps -aq --filter "label=$label"
inventory docker volume ls -q --filter "label=$label"
inventory docker network ls -q --filter "label=$label"
inventory docker network ls -q --filter "name=^$HEIMDALL_PROJECT-evalnet\$"

work=$(mktemp -d)
# No provider key ever reaches this stack's fault proxy, whatever the
# calling shell exports: compose prefers the shell's values to the env file.
export ANTHROPIC_API_KEY='' OPENAI_API_KEY=''
export HEIMDALL_ENV_FILE=$work/smoke.env
ENV_FILE=$HEIMDALL_ENV_FILE
export HEIMDALL_EVAL_NETWORK=$HEIMDALL_PROJECT-evalnet
export HEIMDALL_BUILD_NETWORK=$HEIMDALL_PROJECT-buildnet
# The smoke tags go in the process environment too: compose prefers the
# shell's HEIMDALL_IMAGE to the env file, so a caller's exported production
# tag would otherwise be rebuilt by `hc build`. A smoke tag naming a
# production image is refused.
smoke_image=${SMOKE_IMAGE:-heimdall/server:smoke}
smoke_runner_image=${SMOKE_RUNNER_IMAGE:-heimdall/runner:smoke}
for pair in "$smoke_image|${HEIMDALL_IMAGE:-}|heimdall/server:local" "$smoke_runner_image|${HEIMDALL_RUNNER_IMAGE:-}|heimdall/runner:local"; do
	IFS='|' read -r want prod def <<<"$pair"
	if [ "$want" = "$def" ] || { [ -n "$prod" ] && [ "$want" = "$prod" ]; }; then
		die "smoke image $want is a production image tag; set SMOKE_IMAGE / SMOKE_RUNNER_IMAGE to a smoke-only tag"
	fi
done
export HEIMDALL_IMAGE=$smoke_image HEIMDALL_RUNNER_IMAGE=$smoke_runner_image
cat >"$HEIMDALL_ENV_FILE" <<EOF
HEIMDALL_DOMAIN=$domain
HEIMDALL_PUBLIC_URL=$origin
HEIMDALL_ACME_EMAIL=smoke@example.com
HEIMDALL_SIGNUP=invite
HEIMDALL_PG_PASSWORD=$(random_hex)
HEIMDALL_HTTP_PUBLISH=127.0.0.1:$http_port
HEIMDALL_HTTPS_PUBLISH=127.0.0.1:$https_port
HEIMDALL_EVAL_NETWORK=$HEIMDALL_EVAL_NETWORK
HEIMDALL_BUILD_NETWORK=$HEIMDALL_BUILD_NETWORK
HEIMDALL_REGISTRY_AUTH_DIR=$work/registry-auth
HEIMDALL_BENCH_DIR=$BENCH_DIR
HEIMDALL_IMAGE=$HEIMDALL_IMAGE
HEIMDALL_RUNNER_IMAGE=$HEIMDALL_RUNNER_IMAGE
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

teardown() {
	local rc=$?
	if [ "$rc" -ne 0 ]; then
		log "FAILED (exit $rc); last server and caddy logs:"
		hc logs --no-color --tail 40 heimdall caddy >&2 || true
	fi
	if [ -n "$keep" ]; then
		log "--keep: stack left running as project $HEIMDALL_PROJECT; env file $HEIMDALL_ENV_FILE"
		log "tear down: HEIMDALL_ENV_FILE=$HEIMDALL_ENV_FILE HEIMDALL_PROJECT=$HEIMDALL_PROJECT bash -c '. $HOSTED_DIR/lib.sh; hc down -v --remove-orphans'"
	else
		hc down -v --remove-orphans >/dev/null 2>&1 || true
		rm -rf "$work" || log "could not remove $work"
	fi
	rmdir "$claim" 2>/dev/null || true
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

# A public_read organization with imported runs (seals verified). Hosted
# installs have no public demo: anonymous reads are refused even here.
out=$(bench -- org create -slug demo -name Demo -public-read 2>&1) || fail "org create demo: $out"
ok "org create demo -public-read"
out=$(bench -v "$BENCH_DIR/testdata/recorded:/import:ro" -- org import -org demo -from /import 2>&1) || fail "org import: $out"
ok "org import: $(printf '%s\n' "$out" | tail -n 1)"
for path in /api/bench/experiments /api/bench/catalog; do
	for hdr in "" "X-Heimdall-Org: demo"; do
		code=$(curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" -o /dev/null -w '%{http_code}' ${hdr:+-H "$hdr"} "$origin$path")
		[ "$code" = 401 ] || fail "anonymous GET $path ${hdr:+($hdr) }answered $code, want 401"
	done
done
ok "anonymous HTTPS reads refused (401), public_read org included"
code=$(curl -sS --cacert "$ca" --resolve "$domain:$https_port:127.0.0.1" -o /dev/null -w '%{http_code}' -X POST \
	-H 'Content-Type: application/yaml' -H "Origin: $origin" --data-binary 'name: x' "$origin/api/bench/experiments")
case $code in 401 | 403) ok "anonymous write refused ($code)" ;; *) fail "anonymous POST /api/bench/experiments answered $code" ;; esac

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

log "smoke test passed: $pass checks"
