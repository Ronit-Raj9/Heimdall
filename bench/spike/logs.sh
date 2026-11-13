#!/usr/bin/env bash
# logs.sh SLOT DIR [PAGE_SIZE]
# Poll /api/logs for the slot's virtual key, paging with limit/offset, every 2 s until no row is
# "processing" and two consecutive polls are identical (60 s timeout). Then fetch every detail row.
# start_time (SPIKE_START, default 2 h ago) bounds the window: a bounded window makes
# pagination.total_count a raw COUNT instead of the lagging mv_logs_hourly matview value.
# Writes DIR/rows.json (all list rows), DIR/detail/<id>.json, DIR/poll.log.
source "$(dirname "$0")/common.sh"
slot=$1 dir=$2 page=${3:-2}
mkdir -p "$dir/detail"
vk_id=$(state_get ".slots[\"$slot\"].vk_id")
prev='' start=$(date +%s)
since=${SPIKE_START:-$(date -u -d '-2 hours' +%Y-%m-%dT%H:%M:%SZ)}
: > "$dir/poll.log"
while true; do
  rows='[]' offset=0 pages=0
  while true; do
    resp=$(api GET "/api/logs?virtual_key_ids=$vk_id&start_time=$since&limit=$page&offset=$offset&order=asc")
    n=$(jq '.logs | length' <<< "$resp")
    rows=$(jq --argjson add "$(jq '.logs' <<< "$resp")" '. + $add' <<< "$rows")
    pages=$((pages + 1))
    echo "poll t=$(($(date +%s) - start))s offset=$offset got=$n total_count=$(jq .pagination.total_count <<< "$resp") has_logs=$(jq .has_logs <<< "$resp")" >> "$dir/poll.log"
    [ "$n" -lt "$page" ] && break
    offset=$((offset + page))
  done
  snap=$(jq -c 'map({id, status}) | sort_by(.id)' <<< "$rows")
  processing=$(jq '[.[] | select(.status == "processing")] | length' <<< "$rows")
  echo "snapshot rows=$(jq length <<< "$rows") pages=$pages processing=$processing" >> "$dir/poll.log"
  if [ "$processing" = 0 ] && [ "$snap" = "$prev" ] && [ "$(jq length <<< "$rows")" -gt 0 ]; then break; fi
  if [ $(($(date +%s) - start)) -ge 60 ]; then echo "TIMEOUT usage unknown" >> "$dir/poll.log"; break; fi
  prev=$snap
  sleep 2
done
echo "$rows" > "$dir/rows.json"
for id in $(jq -r '.[].id' "$dir/rows.json"); do
  api GET "/api/logs/$id" > "$dir/detail/$id.json"
done
echo "$slot rows=$(jq length "$dir/rows.json") processing=$processing $(tail -1 "$dir/poll.log")"
