package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N200 (group B): a fallback row a provider answered as a dated
// snapshot of a declared fallback model is a declared fallback, by the same
// "same model" rule the model_fidelity gate applies; another model is not.
func TestDeclaredFallbackAcceptsDatedSnapshot(t *testing.T) {
	agent := &schemas.AgentConfig{Model: "gpt-5.4"}
	agent.Gateway.Fallbacks = []string{"openai/gpt-5.4-mini"}
	cases := []struct {
		served string
		want   bool
	}{
		{"gpt-5.4-mini", true},
		{"gpt-5.4-mini-2026-03-17", true},
		{"openai/gpt-5.4-mini-2026-03-17", true},
		{"gpt-5.4-mini-high", false},
		{"claude-sonnet-4-5", false},
	}
	for _, c := range cases {
		req := schemas.GatewayRequest{ModelRequested: "gpt-5.4", ModelServed: c.served, FallbackIndex: 1}
		if got := declaredFallback(agent, req); got != c.want {
			t.Errorf("declaredFallback(served %q) = %v, want %v", c.served, got, c.want)
		}
	}
}
