#!/usr/bin/env bash
# request.sh SLOT KIND TAG
#   KIND: json | stream | tools-stream | count_tokens | models
# One Anthropic-format request through Bifrost with the slot's virtual key (x-api-key, as Claude Code sends it).
# Writes out/requests/TAG.{request.json,headers,body,status}; prints "TAG STATUS BIFROST_REQUEST_ID".
source "$(dirname "$0")/common.sh"
slot=$1 kind=$2 tag=$3
mkdir -p "$OUT/requests"
vk=$(state_get ".slots[\"$slot\"].vk")
path=/anthropic/v1/messages method=POST
case $kind in
  json | stream) body=$(jq -n --arg m "$MODEL" --arg t "$tag" --argjson s "$([ "$kind" = stream ] && echo true || echo false)" \
      '{model: $m, max_tokens: 64, stream: $s, messages: [{role: "user", content: ("spike request " + $t)}]}') ;;
  tools-stream) body=$(jq -n --arg m "$MODEL" --arg t "$tag" '{model: $m, max_tokens: 64, stream: true,
      tools: [{name: "read_file", description: "Read a file", input_schema: {type: "object", properties: {note: {type: "string"}}}}],
      messages: [{role: "user", content: ("spike request " + $t)}]}') ;;
  count_tokens) path=/anthropic/v1/messages/count_tokens
    body=$(jq -n --arg m "$MODEL" --arg t "$tag" '{model: $m, messages: [{role: "user", content: ("spike request " + $t)}]}') ;;
  models) path=/anthropic/v1/models method=GET body='' ;;
  *) echo "unknown kind $kind" >&2; exit 2 ;;
esac
printf '%s' "$body" > "$OUT/requests/$tag.request.json"
args=(-sS -N -X "$method" -D "$OUT/requests/$tag.headers" -o "$OUT/requests/$tag.body" -w '%{http_code}'
  -H "x-api-key: $vk" -H 'anthropic-version: 2023-06-01')
if [ -n "$body" ]; then args+=(-H 'content-type: application/json' --data-binary "$body"); fi
code=$(curl "${args[@]}" "$BIFROST_URL$path")
echo "$code" > "$OUT/requests/$tag.status"
rid=$(grep -i '^x-bifrost-request-id:' "$OUT/requests/$tag.headers" | tr -d '\r' | awk '{print $2}' || true)
echo "$tag $code ${rid:-none}"
