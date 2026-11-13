#!/usr/bin/env bash
# setup.sh [MAX_RETRIES]      provider anthropic -> faultproxy, key slots slot-a..c, one virtual key per slot.
# setup.sh retries N          PUT the provider again with max_retries N (keys untouched).
# Bodies and responses land in out/setup/. Key ids and virtual keys land in out/state.json.
source "$(dirname "$0")/common.sh"
mkdir -p "$OUT/setup"

provider_body() {
  jq -n --argjson r "$1" '{
    provider: "anthropic",
    network_config: {
      base_url: "http://faultproxy:9100",
      allow_private_network: true,
      max_retries: $r,
      retry_backoff_initial: 100,
      retry_backoff_max: 200,
      default_request_timeout_in_seconds: 60
    },
    concurrency_and_buffer_size: {concurrency: 8, buffer_size: 64},
    store_raw_request_response: true
  }'
}

if [ "${1:-}" = retries ]; then
  body=$(provider_body "$2" | jq 'del(.provider)')
  echo "$body" > "$OUT/setup/provider-retries-$2.request.json"
  api PUT /api/providers/anthropic "$body" > "$OUT/setup/provider-retries-$2.response.json"
  jq -c '.network_config' "$OUT/setup/provider-retries-$2.response.json"
  exit 0
fi

MAX_RETRIES=${1:-0}
wait_health "$BIFROST_URL" 90 > /dev/null
provider_body "$MAX_RETRIES" > "$OUT/setup/provider.request.json"
api POST /api/providers "$(cat "$OUT/setup/provider.request.json")" > "$OUT/setup/provider.response.json"

state='{"slots":{}}'
for slot in slot-a slot-b slot-c; do
  jq -n --arg s "$slot" --arg m "$MODEL" \
    '{name: $s, value: $s, models: [$m], weight: 1, enabled: true}' > "$OUT/setup/key-$slot.request.json"
  api POST /api/providers/anthropic/keys "$(cat "$OUT/setup/key-$slot.request.json")" > "$OUT/setup/key-$slot.response.json"
  key_id=$(jq -r .id "$OUT/setup/key-$slot.response.json")
  jq -n --arg s "$slot" --arg m "$MODEL" --arg k "$key_id" '{
    name: ("spike-vk-" + $s),
    provider_configs: [{provider: "anthropic", weight: 1, allowed_models: [$m], key_ids: [$k]}],
    is_active: true
  }' > "$OUT/setup/vk-$slot.request.json"
  api POST /api/governance/virtual-keys "$(cat "$OUT/setup/vk-$slot.request.json")" > "$OUT/setup/vk-$slot.response.json"
  state=$(jq --arg s "$slot" --arg k "$key_id" \
    --arg vid "$(jq -r .virtual_key.id "$OUT/setup/vk-$slot.response.json")" \
    --arg vk "$(jq -r .virtual_key.value "$OUT/setup/vk-$slot.response.json")" \
    '.slots[$s] = {key_id: $k, vk_id: $vid, vk: $vk}' <<< "$state")
done
echo "$state" > "$OUT/state.json"
jq -c '.slots | map_values({key_id, vk_id})' "$OUT/state.json"
