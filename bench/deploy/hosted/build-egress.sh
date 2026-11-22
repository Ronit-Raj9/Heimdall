#!/bin/sh
# Egress rules of one org image build on the shared worker (G3 B3).
#
# The shared runner starts this as the first container of every org build
# session (cmd/bifrost-bench/buildsession.go): a small container on the build
# network with only NET_ADMIN. The build's BuildKit then joins this
# container's network namespace (--network container:<this>), so every packet
# a RUN step sends leaves through the rules below. BuildKit itself is
# rootless and holds no capability over this namespace, so a RUN step cannot
# change them.
#
# Allowed: loopback (Docker's embedded DNS at 127.0.0.11), replies to
# connections the runner opened (the BuildKit API), the read-only image
# registry (TCP 5000 on the build network's own subnet) and the public
# internet (package installs).
# Refused: cloud metadata (169.254.169.254, Azure's 168.63.129.16), every
# private, shared, link-local, multicast and reserved IPv4 range (other
# compose services, the Docker host, the VPC), and all IPv6.
#
# Prints "heimdall build egress ready" once the rules are in place, then
# waits until the session removes the container.
set -eu

chain=HEIMDALL-BUILD-EGRESS
registry_port=${HEIMDALL_BUILD_REGISTRY_PORT:-5000}

# The build network's subnet, from the kernel route of the first interface.
dev=$(ip -4 route show default | awk '{for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit }}')
[ -n "$dev" ] || { echo "heimdall build egress: no default route" >&2; exit 1; }
subnet=$(ip -4 route show dev "$dev" scope link | awk 'NR == 1 { print $1 }')
[ -n "$subnet" ] || { echo "heimdall build egress: no subnet on $dev" >&2; exit 1; }

iptables -w -N "$chain"
iptables -w -A "$chain" -o lo -j RETURN
iptables -w -A "$chain" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
iptables -w -A "$chain" -p tcp -d "$subnet" --dport "$registry_port" -j RETURN
# DNS: Docker's embedded resolver forwards queries from this namespace to
# the host's upstream servers (listed in the generated resolv.conf as
# "# ExtServers: [...]"; entries written host(...) are forwarded from the
# host's namespace and need nothing here). Those servers are often private
# (Azure's 168.63.129.16, a VPC resolver), so port 53 to them, and only to
# them, is allowed.
dns=$(sed -n 's/^# ExtServers: \[\(.*\)\]$/\1/p' /etc/resolv.conf | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' || true)
for ns in $dns; do
	iptables -w -A "$chain" -p udp -d "$ns" --dport 53 -j RETURN
	iptables -w -A "$chain" -p tcp -d "$ns" --dport 53 -j RETURN
done
for net in \
	0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 168.63.129.16/32 169.254.0.0/16 \
	172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.88.99.0/24 192.168.0.0/16 \
	198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4; do
	iptables -w -A "$chain" -d "$net" -j REJECT --reject-with icmp-net-prohibited
done
iptables -w -A "$chain" -j RETURN
iptables -w -I OUTPUT 1 -j "$chain"

# No IPv6 at all (fd00:ec2::254 and every other v6 address). A host
# without IPv6 support in this namespace has no v6 route to refuse.
if ip6tables -w -L OUTPUT >/dev/null 2>&1; then
	ip6tables -w -P OUTPUT DROP
	ip6tables -w -A OUTPUT -o lo -j ACCEPT
	ip6tables -w -P INPUT DROP
	ip6tables -w -A INPUT -i lo -j ACCEPT
fi

echo "heimdall build egress ready: registry $subnet:$registry_port, DNS ${dns:-host}, public internet" | tr '\n' ' '
echo
trap 'exit 0' TERM INT
while :; do sleep 3600 & wait $!; done
