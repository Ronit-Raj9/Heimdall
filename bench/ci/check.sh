#!/usr/bin/env bash
# Bifrost Bench pull-request gate: run `bifrost-bench check` and exit with its
# code, so a pull request that makes an agent worse fails the build.
#
# Usage: ci/check.sh [recorded|live] [experiments/<file>.yaml]
#
# Modes:
#   recorded (default)  Re-analyze a sealed experiment from a recorded bundle.
#                       The bundle is copied to a temp runs root first, so the
#                       checked-in files never change. Needs no Docker, no
#                       gateway and no key.
#   live                Run an experiment file end to end. Needs the stack up
#                       (make up), its tokens in the environment and a
#                       provider key in the fault proxy.
#
# Inputs (environment). Relative paths resolve against the bench root.
#   BENCH_CI_MODE            recorded | live (the first argument wins)
#   BENCH_CI_BIN             bifrost-bench binary; built with `go build` when unset
#   BENCH_CI_CONFIG          bench config (default deploy/bench.yaml)
#   BENCH_CI_FAIL_ON         verdicts that fail the gate (default regressed,invalid)
#   BENCH_CI_POLICY          policy file; empty uses BENCH_CI_DEFAULT_POLICY when it
#                            exists; "none" turns policy off, including the
#                            policy the experiment froze (check -policy none)
#   BENCH_CI_DEFAULT_POLICY  default policy file (default policies/default.yaml)
#   BENCH_CI_OUT             markdown report path (default
#                            $RUNNER_TEMP/bench-check/report.md); check writes
#                            report.json beside it
#   BENCH_CI_BUNDLE          recorded: bundle root (default testdata/recorded)
#   BENCH_CI_EXP_ID          recorded: experiment id (default: the only one in the bundle)
#   BENCH_CI_DATA_MODE       recorded: recorded (default) | synthetic, the mode the
#                            report is labelled with
#   BENCH_CI_EXPERIMENT      live: experiment file (the second argument wins)
#   BENCH_CI_ENV_FILE        live: optional env file to source first (e.g. deploy/.env)
#   BENCH_CI_KEEP            1 keeps the temp runs root and prints its path
#
# Outputs: the verdict line on stdout; the report appended to
# $GITHUB_STEP_SUMMARY and verdict, action, policy-decision, exit-code, report
# and report-json written to $GITHUB_OUTPUT when those are set.
#
# Exit codes (those of `bifrost-bench check`): 0 pass, 1 regressed,
# 2 inconclusive, 3 invalid, 4 infrastructure, 5 policy decision block,
# 64 usage. Codes 1-3 only when that verdict is listed in BENCH_CI_FAIL_ON.
set -euo pipefail

readonly exit_infra=4
readonly exit_usage=64

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
bench_dir=$(cd "${BENCH_CI_BENCH_DIR:-$script_dir/..}" && pwd)

mode=${1:-${BENCH_CI_MODE:-recorded}}
config=${BENCH_CI_CONFIG:-deploy/bench.yaml}
fail_on=${BENCH_CI_FAIL_ON:-regressed,invalid}
policy=${BENCH_CI_POLICY:-}
default_policy=${BENCH_CI_DEFAULT_POLICY:-policies/default.yaml}
out=${BENCH_CI_OUT:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/bench-check/report.md}
bin=${BENCH_CI_BIN:-}

work=""

usage_error() {
  echo "check.sh: $*" >&2
  exit "$exit_usage"
}

infra_error() {
  echo "check.sh: $*" >&2
  exit "$exit_infra"
}

# shellcheck disable=SC2317 # reached through the EXIT trap
cleanup() {
  if [ -z "$work" ] || [ ! -d "$work" ]; then
    return
  fi
  if [ "${BENCH_CI_KEEP:-}" = "1" ]; then
    echo "check.sh: kept temp runs root $work" >&2
    return
  fi
  # Sealed runs are read-only; make them removable first.
  chmod -R u+w "$work" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

# bench_path resolves a path against the bench root unless it is absolute.
bench_path() {
  case $1 in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$bench_dir/$1" ;;
  esac
}

# json_get prints one dotted field of a JSON file, or nothing.
json_get() {
  local file=$1 path=$2
  [ -f "$file" ] || return 0
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg p "$path" 'getpath($p | split(".")) // empty' "$file" 2>/dev/null || true
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$path" <<'PY' 2>/dev/null || true
import json, sys
v = json.load(open(sys.argv[1]))
for k in sys.argv[2].split("."):
    v = v.get(k) if isinstance(v, dict) else None
print("" if v is None else v)
PY
  fi
}

# put_output writes one step output when running under GitHub Actions.
put_output() {
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    printf '%s=%s\n' "$1" "$2" >>"$GITHUB_OUTPUT"
  fi
}

case $mode in
  recorded | live) ;;
  *) usage_error "mode must be recorded or live, got \"$mode\"" ;;
esac

work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/bench-check.XXXXXX")
out=$(bench_path "$out")
mkdir -p "$(dirname "$out")"
report_json="${out%.*}.json"
rm -f "$out" "$report_json"

# The binary: given, or built here with the module's own go.mod.
if [ -z "$bin" ]; then
  bin="$work/bifrost-bench"
  echo "check.sh: building bifrost-bench" >&2
  (cd "$bench_dir" && GOWORK=off CGO_ENABLED=0 go build -o "$bin" ./cmd/bifrost-bench) ||
    infra_error "go build ./cmd/bifrost-bench failed"
fi
bin=$(bench_path "$bin")
[ -x "$bin" ] || infra_error "binary $bin is missing or not executable"

# Policy: passed only when this bifrost-bench has the -policy flag. The flag
# arrives with policy-as-code; older binaries run the gate without it.
check_help=$("$bin" check -h 2>&1 || true)
supports_policy=false
if grep -Eq '^[[:space:]]+-policy([[:space:]]|$)' <<<"$check_help"; then
  supports_policy=true
fi
policy_note="none"
policy_args=()
policy_auto=false
if [ "$policy" = "none" ]; then
  policy=""
  # "none" must also switch off the policy the experiment froze, which only a
  # bifrost-bench that accepts -policy none can do. An older binary still
  # evaluates the frozen policy; say so instead of claiming policy is off.
  if grep -q 'none turns policy evaluation off' <<<"$check_help"; then
    policy_args=(-policy none)
    policy_note="off (\`-policy none\`)"
  else
    policy_note="no override; this bifrost-bench still evaluates the policy the experiment froze"
    echo "check.sh: policy none: this bifrost-bench cannot turn off the frozen policy" >&2
  fi
elif [ -z "$policy" ] && [ -f "$(bench_path "$default_policy")" ]; then
  policy=$default_policy
  policy_auto=true
fi
if [ -n "$policy" ]; then
  [ -f "$(bench_path "$policy")" ] || usage_error "policy file $policy not found"
  if $supports_policy; then
    policy_args=(-policy "$policy")
    policy_note="\`$policy\`"
  else
    policy_note="\`$policy\` not evaluated: this bifrost-bench has no -policy flag"
    echo "check.sh: $policy not evaluated: bifrost-bench check has no -policy flag" >&2
    # Only a policy asked for by name is worth a warning annotation.
    if ! $policy_auto && [ "${GITHUB_ACTIONS:-}" = "true" ]; then
      echo "::warning title=Bifrost Bench gate::policy $policy not evaluated: bifrost-bench check has no -policy flag"
    fi
  fi
fi

args=(check -config "$config" -fail-on "$fail_on" -out "$out")
if [ "${#policy_args[@]}" -gt 0 ]; then
  args+=("${policy_args[@]}")
fi

if [ "$mode" = "recorded" ]; then
  bundle=$(bench_path "${BENCH_CI_BUNDLE:-testdata/recorded}")
  [ -d "$bundle" ] || usage_error "bundle $bundle not found"
  exp_id=${BENCH_CI_EXP_ID:-}
  if [ -z "$exp_id" ]; then
    found=()
    for m in "$bundle"/*/manifest.json; do
      if [ -f "$m" ]; then
        found+=("$(basename "$(dirname "$m")")")
      fi
    done
    [ "${#found[@]}" -eq 1 ] ||
      usage_error "bundle $bundle holds ${#found[@]} experiments; set BENCH_CI_EXP_ID"
    exp_id=${found[0]}
  fi
  [ -f "$bundle/$exp_id/manifest.json" ] || usage_error "experiment $exp_id not in $bundle"
  data_mode=${BENCH_CI_DATA_MODE:-recorded}
  case $data_mode in
    recorded | synthetic) ;;
    *) usage_error "BENCH_CI_DATA_MODE must be recorded or synthetic, got \"$data_mode\"" ;;
  esac
  # check -exp writes a new analysis folder into the runs root: work on a copy.
  mkdir -p "$work/runs"
  cp -R "$bundle/$exp_id" "$work/runs/" || infra_error "copy $exp_id to the temp runs root failed"
  chmod -R u+w "$work/runs"
  export BENCH_RUNS_ROOT="$work/runs"
  export BENCH_SERVER_MODE="$data_mode"
  args+=(-exp "$exp_id")
  subject="recorded replay of \`$exp_id\` (data: $data_mode), no agent ran in this job"
else
  experiment=${2:-${BENCH_CI_EXPERIMENT:-}}
  [ -n "$experiment" ] || usage_error "live mode needs an experiment file (argument 2 or BENCH_CI_EXPERIMENT)"
  [ -f "$(bench_path "$experiment")" ] || usage_error "experiment $experiment not found"
  if [ -n "${BENCH_CI_ENV_FILE:-}" ]; then
    env_file=$(bench_path "$BENCH_CI_ENV_FILE")
    [ -f "$env_file" ] || usage_error "env file $BENCH_CI_ENV_FILE not found"
    set -a
    # shellcheck disable=SC1090 # the env file is chosen at run time
    . "$env_file"
    set +a
  fi
  export BENCH_SERVER_MODE=live
  args+=("$experiment")
  subject="live run of \`$experiment\`"
fi

echo "check.sh: $mode: bifrost-bench ${args[*]}" >&2
stdout_log="$work/check.stdout"
set +e
(cd "$bench_dir" && "$bin" "${args[@]}") | tee "$stdout_log"
code=${PIPESTATUS[0]}
set -e

line=$(grep -E '^verdict: ' "$stdout_log" | tail -n 1 || true)
verdict=$(sed -nE 's/^verdict: ([a-z_]+).*/\1/p' <<<"$line")
action=$(sed -nE 's/^verdict: [a-z_]+, action: ([a-z_]+).*/\1/p' <<<"$line")
[ -n "$verdict" ] || verdict=$(json_get "$report_json" verdict.decision)
[ -n "$action" ] || action=$(json_get "$report_json" verdict.action)
policy_decision=$(json_get "$report_json" policy.decision)
if [ -z "$policy_decision" ] && [ "$code" -eq 5 ]; then
  policy_decision=block
fi
verdict=${verdict:-none}
action=${action:-none}

case $code in
  0) outcome="pass" ;;
  1) outcome="fail: regressed" ;;
  2) outcome="fail: inconclusive" ;;
  3) outcome="fail: evaluation invalid" ;;
  4) outcome="error: infrastructure (see the job log)" ;;
  5) outcome="fail: policy decision block" ;;
  64) outcome="error: usage" ;;
  *) outcome="error: exit $code" ;;
esac

echo "bench gate: verdict=$verdict action=$action policy=${policy_decision:-n/a} exit=$code ($outcome)"

put_output verdict "$verdict"
put_output action "$action"
put_output policy-decision "${policy_decision:-}"
put_output exit-code "$code"
put_output report "$out"
put_output report-json "$report_json"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "## Bifrost Bench gate: ${outcome}"
    echo
    echo "| Field | Value |"
    echo "|---|---|"
    echo "| Subject | $subject |"
    echo "| Verdict | \`$verdict\` |"
    echo "| Action | \`$action\` |"
    echo "| Policy | $policy_note${policy_decision:+, decision \`$policy_decision\`} |"
    echo "| Fails on | \`$fail_on\`${policy_args[*]:+, policy block} |"
    echo "| Exit code | $code |"
    echo
    if [ -f "$out" ]; then
      cat "$out"
    else
      echo "No report: \`bifrost-bench check\` stopped before writing one."
    fi
    echo
  } >>"$GITHUB_STEP_SUMMARY"
fi

if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
  if [ "$code" -ne 0 ]; then
    echo "::error title=Bifrost Bench gate::$outcome (verdict $verdict, action $action, exit $code)"
  else
    echo "::notice title=Bifrost Bench gate::pass (verdict $verdict, action $action)"
  fi
fi

exit "$code"
