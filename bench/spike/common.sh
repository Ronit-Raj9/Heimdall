#!/usr/bin/env bash
# Shared helpers for the spike scripts. Sourced, never run directly.
set -euo pipefail
SPIKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="${OUT:-$SPIKE_DIR/out}"
BIFROST_URL="${BIFROST_URL:-http://127.0.0.1:18080}"
PROXY_CTL="${PROXY_CTL:-http://127.0.0.1:19101}"
MODEL="${MODEL:-claude-sonnet-4-6}"
mkdir -p "$OUT/http"

setup_token() { cat "$OUT/setup-token"; }

compose() { docker compose -f "$SPIKE_DIR/compose.yaml" --env-file "$OUT/spike.env" "$@"; }

# api METHOD PATH [BODY]: management API call authenticated with the setup token.
# Prints the response body; fails on a non-2xx status. Every call is appended to out/http/api.log.
api() {
  local method=$1 path=$2 body=${3:-} code
  local args=(-sS -X "$method" -H "X-Bifrost-Setup-Token: $(setup_token)" -o "$OUT/http/last-body" -w '%{http_code}')
  if [ -n "$body" ]; then args+=(-H 'Content-Type: application/json' --data-binary "$body"); fi
  code=$(curl "${args[@]}" "$BIFROST_URL$path")
  printf '%s %s %s\n' "$method" "$path" "$code" >> "$OUT/http/api.log"
  cat "$OUT/http/last-body"
  if [[ $code != 2* ]]; then echo "api $method $path -> $code: $(cat "$OUT/http/last-body")" >&2; return 1; fi
}

# wait_health URL SECONDS: poll /health until 200; prints elapsed seconds.
wait_health() {
  local url=$1 limit=${2:-60} i
  for ((i = 0; i < limit; i++)); do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' "$url/health")" = 200 ]; then echo "$i"; return 0; fi
    sleep 1
  done
  return 1
}

# state_get JQ_FILTER: read a value from out/state.json.
state_get() { jq -r "$1" "$OUT/state.json"; }
