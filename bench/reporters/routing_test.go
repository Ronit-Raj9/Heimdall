package reporters

import (
	"bufio"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestWriteRouting (W9): a routed experiment gets a Gateway policy section
// describing the arms by their policy difference; a fixed-key one gets none.
func TestWriteRouting(t *testing.T) {
	cfg := func(p *schemas.RoutingPolicy) schemas.AgentConfig {
		return schemas.AgentConfig{Model: "claude-sonnet-4-5", Gateway: schemas.GatewayPolicy{Provider: "anthropic", Routing: p}}
	}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Config: cfg(nil)},
		"B": {Config: cfg(&schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-5"}},
			Fallbacks: []schemas.RouteTarget{{Model: "claude-haiku-4-5"}}, Limits: &schemas.RouteLimits{RequestMax: 6}})},
	}, Routing: map[string]string{"B": strings.Repeat("ab", 32)}}
	var sb strings.Builder
	w := bufio.NewWriter(&sb)
	writeRouting(w, m)
	w.Flush()
	got := sb.String()
	for _, want := range []string{
		"## Gateway policy",
		"| A | fixed anthropic/claude-sonnet-4-5 (no routing policy) | - |",
		"| B | fallback_chain: anthropic/claude-sonnet-4-5; fallbacks anthropic/claude-haiku-4-5; limits 6 requests per 1h | `abababababab` |",
		"- strategy: A none; B fallback_chain",
		"- fallbacks: A none; B claude-haiku-4-5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	sb.Reset()
	writeRouting(w, &schemas.Manifest{Arms: m.Arms})
	w.Flush()
	if sb.Len() != 0 {
		t.Errorf("fixed-key experiment wrote %q", sb.String())
	}
}
