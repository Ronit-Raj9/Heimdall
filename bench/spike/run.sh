#!/usr/bin/env bash
# run.sh: the whole spike, end to end, from a clean state. Regenerates ../docs/spike.md and leaves
# the stack down. Evidence lands in out/evidence/, results in out/evidence/results.env.
set -euo pipefail
SPIKE_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="$SPIKE_DIR/out"
EV="$OUT/evidence"
cd "$SPIKE_DIR"

# --- clean state -------------------------------------------------------------------------------
if [ -f "$OUT/spike.env" ]; then
  docker compose -f compose.yaml --env-file "$OUT/spike.env" --profile offline down -v --remove-orphans > /dev/null 2>&1 || true
fi
docker rm -f bbspike-isolate > /dev/null 2>&1 || true
rm -rf "$OUT"
mkdir -p "$OUT/ledger" "$EV"
(cd .. && GOWORK=off CGO_ENABLED=0 go build -o spike/out/faultproxy ./spike/proxy)
echo "spike-$(head -c 16 /dev/urandom | xxd -p)" > "$OUT/setup-token"
printf 'BIFROST_SETUP_TOKEN=%s\nSPIKE_UID=%s\nSPIKE_GID=%s\n' "$(cat "$OUT/setup-token")" "$(id -u)" "$(id -g)" > "$OUT/spike.env"
source "$SPIKE_DIR/common.sh"
export SPIKE_START=$(date -u -d '-1 minute' +%Y-%m-%dT%H:%M:%SZ)
result() { echo "$1=$2" >> "$EV/results.env"; echo "== $1 $2"; }
note() { echo "$*" | tee -a "$EV/run.log"; }
compose --profile offline down -v --remove-orphans > /dev/null 2>&1 || true
docker image inspect maximhq/bifrost:v2.5.1 --format '{{index .RepoDigests 0}}' > "$EV/image-digest.txt"

# --- S1 boot -------------------------------------------------------------------------------------
compose up -d 2>&1 | tail -3 >> "$EV/run.log"
s1_secs=$(wait_health "$BIFROST_URL" 120) || s1_secs=timeout
curl -sS -w ' http=%{http_code}\n' "$BIFROST_URL/health" > "$EV/s1-health.txt"
compose logs bifrost 2>&1 | grep -E 'model parameters records|pricing sync interval|MCP library sync is disabled|successfully started bifrost|background live model refresh' | sed 's/^[^|]*| //' > "$EV/s1-main-boot.log" || true
note "S1 main health after ${s1_secs}s: $(cat "$EV/s1-health.txt")"

# --- S2 auth and setup ---------------------------------------------------------------------------
{
  echo "\$ curl -s -w ' %{http_code}' $BIFROST_URL/api/providers"
  curl -s -w ' %{http_code}\n' "$BIFROST_URL/api/providers"
  echo "\$ curl -s -w ' %{http_code}' -H 'X-Bifrost-Setup-Token: <token>' $BIFROST_URL/api/providers"
  curl -s -w ' %{http_code}\n' -H "X-Bifrost-Setup-Token: $(setup_token)" "$BIFROST_URL/api/providers"
  echo "\$ curl -s -w ' %{http_code}' -H 'X-Bifrost-Setup-Token: wrong' $BIFROST_URL/api/providers"
  curl -s -w ' %{http_code}\n' -H "X-Bifrost-Setup-Token: wrong" "$BIFROST_URL/api/providers"
} > "$EV/s2-auth.txt"
./setup.sh 0 > "$EV/s2-setup.txt"
cat "$EV/s2-setup.txt"

# --- S3 path A: plain model id through a one-slot virtual key ------------------------------------
for kind in json stream tools-stream count_tokens models; do ./request.sh slot-a "$kind" "s3-$kind"; done | tee "$EV/s3-requests.txt"

# --- S4 retries ----------------------------------------------------------------------------------
{
  ./setup.sh retries 1
  curl -sS -X PUT "$PROXY_CTL/plans/slot-b" -d '{"mode":"once","call":2,"status":503}'; echo
  ./request.sh slot-b json s4-r1-call1
  ./request.sh slot-b json s4-r1-call2
  ./setup.sh retries 0
  curl -sS -X PUT "$PROXY_CTL/plans/slot-c" -d '{"mode":"once","call":1,"status":503}'; echo
  ./request.sh slot-c json s4-r0-fail
  curl -sS -X PUT "$PROXY_CTL/plans/slot-c" -d '{"mode":"persistent","from_call":2,"status":503}'; echo
  ./request.sh slot-c json s4-p-call1
  ./request.sh slot-c json s4-p-call2
  ./request.sh slot-c json s4-p-call3
  curl -sS "$PROXY_CTL/plans"; echo
} | tee "$EV/s4-requests.txt"

# --- S5 logs, paging, join -----------------------------------------------------------------------
for slot in slot-a slot-b slot-c; do ./logs.sh "$slot" "$OUT/logs/$slot" 2; done | tee "$EV/s5-logs.txt"
for slot in slot-a slot-b slot-c; do
  dir="$OUT/logs/$slot"
  jq -s --arg s "$slot" 'map(select(.slot == $s))' "$OUT/ledger/ledger.jsonl" > "$dir/ledger.json"
  for f in "$dir"/detail/*.json; do
    sha=$(jq -j '.raw_request // ""' "$f" | sha256sum | cut -d' ' -f1)
    jq -c --arg sha "$sha" '{id, object, status, stream, model, retries: .number_of_retries,
      key: (if (.selected_key_name // "") != "" then .selected_key_name else (.attempt_trail[0].key_name // null) end),
      attempts: (.attempt_trail // [] | length), sha: $sha,
      resp_id: ([(.raw_response // "") | capture("\"id\":\"(?<m>msg_[A-Za-z0-9]+)\"").m] | first // null),
      nonce: ([(.raw_response // "") | capture("nonce (?<n>[A-Za-z0-9]+)").n] | first // null)}' "$f"
  done | jq -s . > "$dir/rows-join.json"
  jq --slurpfile led "$dir/ledger.json" 'map(. as $r
    | ($led[0] | map(select(.req_sha256 == $r.sha))) as $same
    | ($led[0] | map(select($r.resp_id != null and .response_id == $r.resp_id)) | length) as $byResp
    | ($led[0] | map(select($r.nonce != null and .nonce == $r.nonce)) | length) as $byNonce
    | $r + {ledger_same_body: ($same | map("\(.call):\(.action):\(.status)")), by_resp_id: $byResp, by_nonce: $byNonce,
        joined: (($byResp == 1) or ($byNonce == 1) or ($r.object == "count_tokens" and ($same | length) == 1)),
        attempts_match: (if $r.object | test("^responses") then ($same | length) == $r.attempts else null end)})' \
    "$dir/rows-join.json" > "$dir/join.json"
done
jq -s 'add' "$OUT"/logs/*/join.json > "$EV/s5-join.json"
jq -s 'add | map(select(.counted)) | length' "$OUT"/logs/*/ledger.json > "$EV/s5-ledger-counted.txt"
jq -s 'add | map(select(.counted and .action == "inject")) | length' "$OUT"/logs/*/ledger.json > "$EV/s5-ledger-injected.txt"
for slot in slot-a slot-b slot-c; do
  jq --slurpfile rows "$OUT/logs/$slot/join.json" '[.[] | select(.counted) | .req_sha256 as $h
    | ([$rows[0][] | select(.sha == $h)] | length)] | {counted: length, in_exactly_one_row: map(select(. == 1)) | length}' "$OUT/logs/$slot/ledger.json"
done | jq -s '{counted: (map(.counted) | add), in_exactly_one_row: (map(.in_exactly_one_row) | add)}' > "$EV/s5-ledger-coverage.json"

# --- S6 isolation --------------------------------------------------------------------------------
./isolate.sh slot-a > /dev/null
cp "$OUT/isolate.txt" "$EV/s6-isolate.txt"

# --- S1 offline boot: evalnet only, no internet --------------------------------------------------
compose --profile offline up -d bifrost-offline-default bifrost-offline-pinned 2>&1 | tail -2 >> "$EV/run.log"
offline_default=running offline_pinned=down
for ((i = 0; i < 60; i++)); do
  sleep 2
  offline_default=$(docker inspect -f '{{.State.Status}} exit={{.State.ExitCode}}' bbspike-bifrost-offline-default-1)
  offline_pinned=$(docker exec bbspike-bifrost-offline-pinned-1 wget -q -O - http://127.0.0.1:8080/health 2>/dev/null || echo down)
  if [[ $offline_default == exited* ]] && [ "$offline_pinned" != down ]; then break; fi
done
{
  echo "offline-default (default pricing URL, evalnet only): $offline_default after ~$((i * 2 + 2))s"
  docker logs bbspike-bifrost-offline-default-1 2>&1 | grep -E '"level":"fatal"' || true
  echo "offline-pinned (file:// datasheets, evalnet only): health=$offline_pinned"
  echo "offline-pinned /api/providers with setup token: $(docker exec bbspike-bifrost-offline-pinned-1 wget -S -q -O - --header "X-Bifrost-Setup-Token: $(setup_token)" http://127.0.0.1:8080/api/providers 2>&1 | grep -E '^\{|HTTP/' | tr -d '\n' | sed 's/  */ /g')"
  docker logs bbspike-bifrost-offline-pinned-1 2>&1 | grep -E 'model parameters records|MCP library sync is disabled|successfully started bifrost' || true
} > "$EV/s1-offline.txt"
cat "$EV/s1-offline.txt"
compose --profile offline stop bifrost-offline-default bifrost-offline-pinned > /dev/null 2>&1 || true

# --- response headers ----------------------------------------------------------------------------
for tag in s3-json s3-stream s4-r0-fail; do
  echo "## $tag: $(head -1 "$OUT/requests/$tag.headers" | tr -d '\r')"
  grep -i -E '^(x-bifrost-|x-request-id|request-id)' "$OUT/requests/$tag.headers" | tr -d '\r'
done > "$EV/headers.txt"

# --- S9 seal -------------------------------------------------------------------------------------
sealed="$OUT/sealed-run"
mkdir -p "$sealed"
cp -r "$OUT/ledger" "$OUT/logs" "$OUT/requests" "$OUT/setup" "$OUT/isolate.txt" "$sealed/"
{
  echo "\$ seal.sh seal out/sealed-run";   ./seal.sh seal "$sealed"
  echo "\$ seal.sh verify out/sealed-run"; ./seal.sh verify "$sealed" || echo "exit=1 (UNEXPECTED)"
  rm -rf "$OUT/sealed-run-tampered"; cp -r "$sealed" "$OUT/sealed-run-tampered"
  target="$OUT/sealed-run-tampered/ledger/ledger.jsonl"
  old=$(dd if="$target" bs=1 skip=100 count=1 2>/dev/null)
  new=X; [ "$old" = X ] && new=Y
  printf '%s' "$new" | dd of="$target" bs=1 seek=100 count=1 conv=notrunc 2>/dev/null
  echo "# flipped byte 100 of ledger/ledger.jsonl: '$old' -> '$new'"
  echo "\$ seal.sh verify out/sealed-run-tampered"
  ./seal.sh verify "$OUT/sealed-run-tampered" 2>&1 && echo "UNEXPECTED: tampered folder verified" || echo "exit=1 (expected)"
  rm -rf "$OUT/sealed-run-extra"; cp -r "$sealed" "$OUT/sealed-run-extra"; echo extra > "$OUT/sealed-run-extra/extra.txt"
  echo "\$ seal.sh verify out/sealed-run-extra   # one added file"
  ./seal.sh verify "$OUT/sealed-run-extra" 2>&1 && echo "UNEXPECTED: extra file verified" || echo "exit=1 (expected)"
} > "$EV/s9-seal.txt" 2>&1
cat "$EV/s9-seal.txt"

# --- verdicts ------------------------------------------------------------------------------------
: > "$EV/results.env"
grep -q 'http=200' "$EV/s1-health.txt" && [[ $offline_default == *exit=1* ]] && [ "$offline_pinned" != down ] \
  && result S1 PASS || result S1 FAIL
[ "$(grep -E '^ [0-9]{3}$' "$EV/s2-auth.txt" | tr -d ' \n')" = 401200403 ] && [ "$(jq -r '.slots["slot-a"].vk' "$OUT/state.json" | cut -c1-6)" = sk-bf- ] \
  && result S2 PASS || result S2 FAIL
s3_id=$(jq -r .id "$OUT/requests/s3-json.body")
[ "$(cat "$OUT/requests/s3-json.status")" = 200 ] && grep -q "\"slot\":\"slot-a\".*\"response_id\":\"$s3_id\"" "$OUT/ledger/ledger.jsonl" \
  && [ "$(jq --arg id "$s3_id" '[.[] | select(.resp_id == $id and .model == "claude-sonnet-4-6" and .key == "slot-a")] | length' "$EV/s5-join.json")" = 1 ] \
  && result S3 PASS || result S3 FAIL
r1=$(jq '[.[] | select(.key == "slot-b" and .attempts == 2 and .status == "success" and .attempts_match)] | length' "$EV/s5-join.json")
r0=$(jq '[.[] | select(.key == "slot-c" and .attempts == 1 and .status == "error" and .attempts_match)] | length' "$EV/s5-join.json")
[ "$r1" = 1 ] && [ "$r0" -ge 1 ] && result S4 PASS || result S4 FAIL
unjoined=$(jq '[.[] | select(.joined | not)] | length' "$EV/s5-join.json")
unjoined_msgs=$(jq '[.[] | select((.object | test("^responses")) and (.joined | not))] | length' "$EV/s5-join.json")
if [ "$unjoined" = 0 ]; then result S5 PASS; elif [ "$unjoined_msgs" = 0 ]; then result S5 PARTIAL; else result S5 FAIL; fi
grep -q ' FAIL ' "$EV/s6-isolate.txt" && result S6 FAIL || result S6 PASS
grep -q '^verify OK' "$EV/s9-seal.txt" && [ "$(grep -c 'exit=1 (expected)' "$EV/s9-seal.txt")" = 2 ] && result S9 PASS || result S9 FAIL

# --- report and teardown -------------------------------------------------------------------------
./report.sh > "$SPIKE_DIR/../docs/spike.md"
compose --profile offline down -v --remove-orphans > /dev/null 2>&1
docker rm -f bbspike-isolate > /dev/null 2>&1 || true
left=$(docker ps -a --filter label=bifrost-bench=1 --format '{{.Names}}' | tr '\n' ' ')
note "teardown done; labelled containers left: ${left:-none}"
cat "$EV/results.env"
