package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
)

// TestCheckEnvConfigKeepsServiceGateway guards 3-C-04: agent check must keep
// the normal service gateway wiring, so a model-backed service does not fail
// with "not wired to the gateway" before the agent starts.
func TestCheckEnvConfigKeepsServiceGateway(t *testing.T) {
	base := environments.DockerWorkspaceConfig{EvalNetwork: "eval", ServiceGateway: environments.ServiceGatewayConfig{AgentURL: "http://gw:8080", PriceRef: "p"}}
	got := checkEnvConfig(base)
	if !got.NetWatch || got.ServiceGateway.AgentURL != "http://gw:8080" || got.ServiceGateway.PriceRef != "p" || got.EvalNetwork != "eval" {
		t.Fatalf("check env lost wiring: %+v", got.ServiceGateway)
	}
	if base.NetWatch {
		t.Fatal("base config mutated")
	}
}
