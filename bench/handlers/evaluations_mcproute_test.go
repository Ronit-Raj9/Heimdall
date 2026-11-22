package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestEvaluationMCPRouteNormalize checks direct is stored as "" (old
// evaluations hash as before), the Bifrost routes are kept, and nothing
// else is accepted.
func TestEvaluationMCPRouteNormalize(t *testing.T) {
	for in, want := range map[string]string{"": "", " direct ": "", "bifrost": schemas.MCPRouteBifrost, "bifrost_code_mode": schemas.MCPRouteBifrostCodeMode} {
		spec := schemas.EvaluationSpec{MCPRoute: in}
		if err := normalizeMCPRoute(&spec); err != nil || spec.MCPRoute != want {
			t.Fatalf("route %q: %q %v", in, spec.MCPRoute, err)
		}
	}
	if err := normalizeMCPRoute(&schemas.EvaluationSpec{MCPRoute: "toolproxy"}); err == nil {
		t.Fatal("bad route accepted")
	}
}

// TestEvaluationMCPRouteArms checks every arm of a Bifrost-routed
// evaluation sets gateway.bifrost.mcp, with code_mode as chosen, and a
// direct evaluation sets nothing new.
func TestEvaluationMCPRouteArms(t *testing.T) {
	arms := []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "gpt-5-mini"}, {Label: "B", Agent: "codex", Model: "gpt-5"}}
	for route, want := range map[string]any{"": nil, schemas.MCPRouteBifrost: false, schemas.MCPRouteBifrostCodeMode: true} {
		out := evalArms(schemas.EvaluationSpec{Arms: arms, MCPRoute: route})
		for _, l := range []string{"A", "B"} {
			got, ok := out[l].Set["gateway.bifrost.mcp.code_mode"]
			if want == nil {
				if ok {
					t.Fatalf("direct route arm %s sets %v", l, got)
				}
				continue
			}
			if !ok || got != want {
				t.Fatalf("route %q arm %s: code_mode %v (set %v), want %v", route, l, got, ok, want)
			}
			if out[l].Set["model"] == nil {
				t.Fatalf("route %q arm %s lost its model: %v", route, l, out[l].Set)
			}
		}
	}
}
