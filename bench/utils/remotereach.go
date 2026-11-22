package utils

import (
	"net"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Y03 (G1 P0-1): an agent that runs outside the worker (a deployed
// remote_http or A2A endpoint) can only use the run's gateway and tool gate
// when the worker publishes them at an address the agent can reach. A run
// that would send it an address on the worker's own networks is refused
// before it starts, with the reason, instead of running and scoring 0.

// Reach is where a worker publishes the run's gateway and tool gate.
type Reach struct {
	GatewayPublicURL string // gateway.public_url
	ToolsPublicURL   string // remote_tools.public_url
	ToolsListen      string // remote_tools.listen
}

// Stable refusal sentences: the planner lists each once.
const (
	RefuseGatewayUnreachable = "the agent's endpoint is on the internet and sends its model calls through Heimdall (model_traffic gateway), but this worker's gateway has no public address (gateway.public_url), so the agent could not reach it; use model_traffic external, or run it on a worker whose gateway is published (the hosted shared worker is)"
	RefuseToolsUnreachable   = "the agent's endpoint is on the internet and uses the task's tools (tools expose), but this worker publishes tool gates only on its own machine (remote_tools.public_url is empty), so the agent could not reach them; run it on a worker whose tools are published (the hosted shared worker is)"
)

// RemoteReachRefusals lists why agent a cannot reach what its runs need
// from a worker published as r. Agents that run beside the worker (every
// in-container agent), and endpoints on private or compose-internal
// addresses (which share the worker's networks), are never refused here. It
// is pure.
func RemoteReachRefusals(a *schemas.AgentConfig, r Reach) []string {
	if a == nil {
		return nil
	}
	var endpoint string
	var gatewayTraffic, tools bool
	switch {
	case a.A2A != nil:
		endpoint, tools = a.A2A.CardURL, a.A2A.Tools == schemas.RemoteToolsExpose
	case a.Remote != nil:
		endpoint = a.Remote.URL
		gatewayTraffic = a.Remote.ModelTraffic == "gateway"
		tools = a.Remote.Tools == schemas.RemoteToolsExpose
	default:
		return nil
	}
	if !PublicEndpoint(endpoint) {
		return nil
	}
	var out []string
	if gatewayTraffic && strings.TrimSpace(r.GatewayPublicURL) == "" {
		out = append(out, RefuseGatewayUnreachable)
	}
	if tools && strings.TrimSpace(r.ToolsPublicURL) == "" && !publicListen(r.ToolsListen) {
		out = append(out, RefuseToolsUnreachable)
	}
	return out
}

// PublicEndpoint reports whether raw names a host on the internet: a
// public IP, or a dotted name outside the local and internal suffixes. A
// bare name (a compose service), localhost, and private, loopback,
// link-local or CGNAT addresses are not. It is pure (no DNS).
func PublicEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		return PublicIP(ip)
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suf := range []string{".localhost", ".local", ".internal", ".lan", ".home.arpa", ".cluster.local", ".svc", ".docker"} {
		if strings.HasSuffix(host, suf) {
			return false
		}
	}
	return true
}

// PublicIP reports whether ip is a global address outside every range
// the network guard refuses (BlockedIP: private, loopback, link-local,
// metadata, CGNAT and the rest). It is pure.
func PublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !BlockedIP(ip)
}

// publicListen reports whether remote_tools.listen binds a public IP, so a
// gate's http://<ip>:<port> address is reachable without a public URL.
func publicListen(listen string) bool {
	host, _, err := SplitRemoteToolsListen(listen)
	return err == nil && PublicIP(net.ParseIP(host))
}
