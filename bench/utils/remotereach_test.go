package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRemoteReachRefusals(t *testing.T) {
	remote := func(url, traffic, tools string) *schemas.AgentConfig {
		return &schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: url, ModelTraffic: traffic, Tools: tools}}
	}
	public := Reach{GatewayPublicURL: "https://h.example.com/gw", ToolsPublicURL: "https://h.example.com/rt"}
	for name, c := range map[string]struct {
		a    *schemas.AgentConfig
		r    Reach
		want []string
	}{
		"in-container agent":           {&schemas.AgentConfig{Adapter: "command"}, Reach{}, nil},
		"external traffic, no tools":   {remote("https://agent.acme.com/run", "external", ""), Reach{}, nil},
		"gateway traffic, no public":   {remote("https://agent.acme.com/run", "gateway", ""), Reach{}, []string{RefuseGatewayUnreachable}},
		"gateway traffic, public":      {remote("https://agent.acme.com/run", "gateway", ""), public, nil},
		"tools, no public":             {remote("https://agent.acme.com/run", "", schemas.RemoteToolsExpose), Reach{ToolsListen: "127.0.0.1:0"}, []string{RefuseToolsUnreachable}},
		"tools, public listen":         {remote("https://agent.acme.com/run", "", schemas.RemoteToolsExpose), Reach{ToolsListen: "8.8.4.4:0"}, nil},
		"both missing":                 {remote("https://1.1.1.1/run", "gateway", schemas.RemoteToolsExpose), Reach{}, []string{RefuseGatewayUnreachable, RefuseToolsUnreachable}},
		"compose neighbour":            {remote("http://acme-agent:8000/run", "gateway", schemas.RemoteToolsExpose), Reach{}, nil},
		"private address":              {remote("http://10.0.0.5/run", "gateway", ""), Reach{}, nil},
		"internal suffix":              {remote("http://agent.svc.cluster.local/run", "gateway", ""), Reach{}, nil},
		"a2a tools, no public":         {&schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "https://a2a.acme.com/.well-known/agent-card.json", Tools: schemas.RemoteToolsExpose}}, Reach{}, []string{RefuseToolsUnreachable}},
		"a2a on the gateway's network": {&schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a2a-agents:9000/card", Tools: schemas.RemoteToolsExpose}}, Reach{}, nil},
	} {
		got := RemoteReachRefusals(c.a, c.r)
		if len(got) != len(c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
	}
}

func TestPublicEndpoint(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://agent.acme.com/x": true, "https://8.8.8.8/": true, "http://bifrost:8080": false,
		"http://localhost:1": false, "http://127.0.0.1": false, "http://169.254.169.254/": false,
		"http://100.64.1.1": false, "http://[fd00::1]/": false, "http://x.internal": false, "": false,
	} {
		if got := PublicEndpoint(raw); got != want {
			t.Errorf("%q: %t, want %t", raw, got, want)
		}
	}
}
