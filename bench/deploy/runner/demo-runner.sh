#!/usr/bin/env bash
# Creates the demo organization's runner token (pool demo) through the admin
# API and prints, or runs, the install command of the demo runner
# (docs/runners.md, "The demo runner").
#
#   deploy/runner/demo-runner.sh [--env FILE] [--install] [--dry-run]
#
# --env      settings file (default deploy/runner/demo-runner.env; see
#            demo-runner.env.example)
# --install  run deploy/runner/install.sh with the new token (needs root)
# --dry-run  print what would be done; create nothing
set -euo pipefail

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
env_file="$here/demo-runner.env"
install=0
dry=0
while [[ $# -gt 0 ]]; do
  case $1 in
    --env) [[ $# -ge 2 ]] || die "--env needs a file"; env_file=$2; shift 2 ;;
    --install) install=1; shift ;;
    --dry-run) dry=1; shift ;;
    -h|--help) sed -n '2,13p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -f $env_file ]] || die "no settings file $env_file (copy demo-runner.env.example)"

# Read KEY=VALUE lines only; nothing in the file is executed.
declare -A cfg=()
while IFS= read -r line || [[ -n $line ]]; do
  [[ $line =~ ^[[:space:]]*(#|$) ]] && continue
  [[ $line =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]] || die "bad line in $env_file: $line"
  cfg[${BASH_REMATCH[1]}]=${BASH_REMATCH[2]}
done < "$env_file"

get() { printf '%s' "${cfg[$1]:-$2}"; }
server=$(get HEIMDALL_SERVER "")
org=$(get HEIMDALL_DEMO_ORG demo)
admin_file=$(get HEIMDALL_ADMIN_TOKEN_FILE "")
pool=$(get RUNNER_POOL demo)
name=$(get RUNNER_NAME demo-runner)
slots=$(get RUNNER_SLOTS 1)
cap=$(get SPEND_CAP_USD 5)
token_file=$(get RUNNER_TOKEN_FILE ./demo-runner-token)

[[ $server =~ ^https?://[A-Za-z0-9.:-]+(/[A-Za-z0-9._/-]*)?$ ]] || die "HEIMDALL_SERVER is not a URL: $server"
server=${server%/}
name_re='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for v in "$org" "$pool" "$name"; do
  [[ $v =~ $name_re ]] || die "bad org, pool or name: $v"
done
[[ $slots =~ ^[1-9][0-9]?$ ]] || die "RUNNER_SLOTS must be 1 to 64"
[[ $cap =~ ^[0-9]+(\.[0-9]+)?$ ]] || die "SPEND_CAP_USD must be a number"

body=$(printf '{"pool":"%s","name":"%s"}' "$pool" "$name")
install_cmd=(bash "$here/install.sh" --server "$server" --token-file "$token_file" --pool "$pool" --name "$name"
  --slots "$slots" --spend-cap-usd "$cap")

if [[ $dry -eq 1 ]]; then
  printf '[dry-run] POST %s/api/bench/runners/tokens (X-Heimdall-Org: %s, admin token from %s) %s\n' "$server" "$org" "$admin_file" "$body"
  printf '[dry-run] write the token to %s (mode 0600)\n' "$token_file"
  printf '[dry-run]'; printf ' %q' "${install_cmd[@]}"; printf '\n'
  exit 0
fi

[[ -n $admin_file && -f $admin_file ]] || die "HEIMDALL_ADMIN_TOKEN_FILE must name the admin API token file"
perm=$(stat -c '%a' "$admin_file")
[[ $perm =~ ^[0-7]00$ ]] || die "$admin_file is readable by others (mode $perm); chmod 600 it"
command -v curl >/dev/null || die "curl is required"

admin=$(tr -d '[:space:]' < "$admin_file")
answer=$(curl -fsS -X POST "$server/api/bench/runners/tokens" \
  -H @<(printf 'Authorization: Bearer %s\n' "$admin") \
  -H "X-Heimdall-Org: $org" -H 'Content-Type: application/json' --data "$body") \
  || die "creating the runner token failed (is the admin token an admin of $org?)"
token=$(printf '%s' "$answer" | grep -o '"token":"hmr_[A-Za-z0-9_-]*"' | head -n1 | cut -d'"' -f4)
[[ -n $token ]] || die "the server answered without a token"

(umask 077 && printf '%s\n' "$token" > "$token_file")
printf 'runner token for pool %s written to %s (mode 0600); it is not shown again\n' "$pool" "$token_file"

if [[ $install -eq 1 ]]; then
  [[ $(id -u) -eq 0 ]] || die "--install needs root"
  "${install_cmd[@]}"
else
  printf 'install the demo runner with:\n  sudo'; printf ' %q' "${install_cmd[@]}"; printf '\n'
fi
