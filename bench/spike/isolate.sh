#!/usr/bin/env bash
# isolate.sh [SLOT]
# From a throwaway container on evalnet (default capabilities, so a failed ping means the network
# refused it, not a missing CAP_NET_RAW), probe what an agent container could reach.
# Prints one line per probe: "PROBE <name> <expect> <result> <detail>"; result is PASS when the
# observed outcome equals the expectation. Writes out/isolate.txt.
source "$(dirname "$0")/common.sh"
slot=${1:-slot-a}
vk=$(state_get ".slots[\"$slot\"].vk")
net=$(docker network ls --filter label=com.docker.compose.project=bbspike --filter name=evalnet --format '{{.Name}}' | head -1)
docker run --rm --name bbspike-isolate --label bifrost-bench=1 --network "$net" -e VK="$vk" alpine:3.20 sh -c '
probe() { # name expect(reach|block) cmd...
  name=$1 expect=$2; shift 2
  out=$(timeout 8 "$@" 2>&1); rc=$?
  if [ $rc -eq 0 ]; then got=reach; else got=block; fi
  [ "$got" = "$expect" ] && res=PASS || res=FAIL
  echo "PROBE $name expect=$expect got=$got rc=$rc $res :: $(echo "$out" | tr "\n" " " | cut -c1-160)"
}
api() { # name url header: the management API must not answer 2xx to an agent credential
  s=$(wget -S -q -O /dev/null -T 5 --header "$3" "$2" 2>&1 | grep -m1 "HTTP/" || echo "no-response")
  if echo "$s" | grep -q " 2[0-9][0-9]"; then got=reach; res=FAIL; else got=block; res=PASS; fi
  echo "PROBE $1 expect=block got=$got $res :: $(echo "$s" | tr -d "\r" | sed "s/^ *//")"
}
echo "ip: $(ip -o addr show | awk "{print \$2, \$4}" | tr "\n" ";")"
echo "routes4: $(ip route | tr "\n" ";")"
echo "routes6: $(ip -6 route 2>/dev/null | tr "\n" ";")"
probe bifrost_health_control reach wget -q -O - -T 5 http://bifrost:8080/health
api api_logs_with_vk_x_api_key http://bifrost:8080/api/logs "x-api-key: $VK"
api api_logs_with_vk_bearer http://bifrost:8080/api/logs "Authorization: Bearer $VK"
api api_logs_with_vk_x_bf_vk http://bifrost:8080/api/logs "x-bf-vk: $VK"
api api_providers_with_vk http://bifrost:8080/api/providers "x-api-key: $VK"
api api_virtual_keys_with_vk http://bifrost:8080/api/governance/virtual-keys "Authorization: Bearer $VK"
api api_logs_no_credential http://bifrost:8080/api/logs "x-probe: 1"
probe inference_with_vk_control reach wget -q -O - -T 10 --header "x-api-key: $VK" --header "content-type: application/json" --header "anthropic-version: 2023-06-01" --post-data "{\"model\":\"claude-sonnet-4-6\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"isolation control\"}]}" http://bifrost:8080/anthropic/v1/messages
probe dns_external block nslookup example.com
probe dns_external_8888 block nslookup example.com 8.8.8.8
probe tcp_1111_80 block wget -q -O /dev/null -T 5 http://1.1.1.1/
probe tcp_1111_443 block nc -w 4 1.1.1.1 443
probe icmp_1111 block ping -c 1 -W 3 1.1.1.1
probe ipv6_icmp block ping -6 -c 1 -W 3 2606:4700:4700::1111
probe ipv6_tcp block nc -w 4 2606:4700:4700::1111 443
probe metadata_169254 block wget -q -O /dev/null -T 5 http://169.254.169.254/latest/meta-data/
probe faultproxy_direct block wget -q -O /dev/null -T 5 http://faultproxy:9100/v1/models
probe postgres_direct block nc -w 4 postgres 5432
' | tee "$OUT/isolate.txt"
