#!/usr/bin/env bash
# End-to-end check of the heimdall Bifrost plugin.
#
# Stage 1 (always): runs plugins/heimdall/e2e_test.go, which boots a real
# Bifrost core client with the heimdall plugin and an httptest OpenAI upstream,
# and proves that a named fault plan injects a 429 before the provider is
# called, that the next call reaches the upstream, and that the Heimdall ids
# are on the log dimensions.
#
# Stage 2 (only with HTTP=1): builds bifrost-http from transports/ against the
# local plugin and core, starts it with config.e2e.json and a mock upstream,
# and sends real HTTP requests. Needs the heimdall plugin registered in
# transports/bifrost-http (lane C02). Skipped with a note when that wiring is
# not present.
#
# Usage: bench/deploy/bifrost/e2e/run.sh            # stage 1
#        HTTP=1 bench/deploy/bifrost/e2e/run.sh     # stage 1 + stage 2
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
export GOWORK=off
export GOFLAGS="${GOFLAGS:--p=2 -buildvcs=false}"

echo "== stage 1: Go e2e test (core client + heimdall plugin + mock upstream)"
(cd "$ROOT/plugins/heimdall" && HEIMDALL_E2E=1 nice -n 10 go test -count=1 -run 'TestE2E' -v .)

if [[ "${HTTP:-0}" != "1" ]]; then
  echo "== stage 2 skipped (set HTTP=1 to build bifrost-http and test over HTTP)"
  exit 0
fi

if ! grep -rqs "plugins/heimdall" "$ROOT/transports/bifrost-http/server/plugins.go"; then
  echo "== stage 2 skipped: heimdall plugin is not registered in transports/bifrost-http yet"
  exit 0
fi

WORK="$(mktemp -d)"
UP_PID=""
BF_PID=""
cleanup() {
  [[ -n "$BF_PID" ]] && kill "$BF_PID" 2>/dev/null || true
  [[ -n "$UP_PID" ]] && kill "$UP_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

UP_PORT="${UP_PORT:-18711}"
BF_PORT="${BF_PORT:-18712}"

echo "== stage 2: build bifrost-http"
(cd "$ROOT/transports" && nice -n 10 go build -o "$WORK/bifrost-http" ./bifrost-http)

python3 "$HERE/mock_upstream.py" "$UP_PORT" "$WORK/upstream_hits" &
UP_PID=$!

sed -e "s#__UPSTREAM__#http://127.0.0.1:${UP_PORT}#" "$HERE/config.e2e.json" > "$WORK/config.json"
"$WORK/bifrost-http" -app-dir "$WORK" -host 127.0.0.1 -port "$BF_PORT" >"$WORK/bifrost.log" 2>&1 &
BF_PID=$!

for _ in $(seq 1 60); do
  curl -fsS "http://127.0.0.1:${BF_PORT}/health" >/dev/null 2>&1 && break
  sleep 0.5
done

call() {
  curl -sS -o "$WORK/body" -D "$WORK/headers" -w '%{http_code}' \
    -H 'content-type: application/json' \
    -H 'x-heimdall-run-id: run-http-1' -H 'x-heimdall-eval-id: eval-http' \
    -H 'x-heimdall-arm: baseline' -H 'x-heimdall-fault-plan-id: rl-first' \
    -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}' \
    "http://127.0.0.1:${BF_PORT}/v1/chat/completions"
}

fail() { echo "FAIL: $*"; echo "--- bifrost log"; tail -40 "$WORK/bifrost.log"; exit 1; }

code="$(call)"
[[ "$code" == "429" ]] || fail "call 1 want 429, got $code: $(cat "$WORK/body")"
grep -qi '^x-heimdall-fault-applied: rate_limit' "$WORK/headers" || fail "call 1 missing x-heimdall-fault-applied"
grep -qi '^x-heimdall-run-id: run-http-1' "$WORK/headers" || fail "call 1 missing x-heimdall-run-id"
grep -qi '^retry-after: 7' "$WORK/headers" || fail "call 1 missing Retry-After"
[[ ! -s "$WORK/upstream_hits" ]] || fail "upstream called during the fault"

code="$(call)"
[[ "$code" == "200" ]] || fail "call 2 want 200, got $code: $(cat "$WORK/body")"
grep -q 'upstream says hi' "$WORK/body" || fail "call 2 body not from upstream"
grep -qi '^x-heimdall-run-id: run-http-1' "$WORK/headers" || fail "call 2 missing x-heimdall-run-id"

echo "== stage 2 passed: fault injected over HTTP, tags present, upstream reached after the window"
