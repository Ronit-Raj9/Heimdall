package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func routedAgent(pol *schemas.RoutingPolicy) schemas.AgentConfig {
	return schemas.AgentConfig{ID: "toy", Model: "claude-sonnet-4-6", Gateway: schemas.GatewayPolicy{Provider: "anthropic", Routing: pol}}
}

func TestValidateRouting(t *testing.T) {
	gm := &schemas.GatewayManifest{Settings: map[string]string{"anthropic.max_retries": "0"}}
	tgt := func(model string, w float64) schemas.RouteTarget { return schemas.RouteTarget{Model: model, Weight: w} }
	zero, one := 0, 1
	cases := []struct {
		name string
		pol  *schemas.RoutingPolicy
		err  string // "" = valid
	}{
		{"fixed", &schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Retries: &zero}, ""},
		{"fallback chain", &schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{tgt("anthropic/claude-sonnet-4-6", 0)}, Fallbacks: []schemas.RouteTarget{tgt("claude-haiku-4-5", 0)}}, ""},
		{"weighted", &schemas.RoutingPolicy{Strategy: "weighted", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0.5), tgt("claude-haiku-4-5", 0.5)}}, ""},
		{"rule", &schemas.RoutingPolicy{Strategy: "rule", When: `request_type == "chat_completion"`, Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}}, ""},
		{"unknown strategy", &schemas.RoutingPolicy{Strategy: "smart", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}}, "strategy must be"},
		{"fixed with a fallback", &schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Fallbacks: []schemas.RouteTarget{tgt("claude-haiku-4-5", 0)}}, "one target and no fallbacks"},
		{"chain without fallback", &schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}}, "at least one fallback"},
		{"weights off", &schemas.RoutingPolicy{Strategy: "weighted", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0.6), tgt("claude-haiku-4-5", 0.6)}}, "sum to 1.2"},
		{"primary is not the agent model", &schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{tgt("claude-haiku-4-5", 0)}, Fallbacks: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}}, "first target claude-haiku-4-5 must be the model the agent asks for"},
		{"other provider", &schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Fallbacks: []schemas.RouteTarget{{Provider: "openai", Model: "gpt-5-mini"}}}, "a run key holds one key slot"},
		{"retries differ", &schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Retries: &one}, "Bifrost sets retries per provider"},
		{"bad duration", &schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Limits: &schemas.RouteLimits{RequestMax: 3, RequestReset: "1 hour"}}, "not a Bifrost duration"},
		{"duplicate target", &schemas.RoutingPolicy{Strategy: "weighted", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0.5), tgt("claude-sonnet-4-6", 0.5)}}, "listed twice"},
		{"when outside rule", &schemas.RoutingPolicy{Strategy: "fixed", When: "true", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}}, "when is only read by strategy rule"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := routedAgent(c.pol)
			err := ValidateRouting(&cfg, gm)
			if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
				t.Fatalf("err = %v, want %q", err, c.err)
			}
		})
	}
	cfg := routedAgent(&schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{tgt("claude-sonnet-4-6", 0)}, Retries: &zero})
	if err := ValidateRouting(&cfg, nil); err == nil || !strings.Contains(err.Error(), "without a gateway preflight") {
		t.Errorf("retries without preflight: %v", err)
	}
}

// TestCheckRoutingManifest: a plan without routing adds nothing to the
// manifest (old manifests keep their hash); a routed arm gets a stable
// policy hash; an invalid policy refuses the plan.
func TestCheckRoutingManifest(t *testing.T) {
	plain := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "toy"}}}}
	got, err := checkRouting(plain)
	if err != nil || got != nil {
		t.Fatalf("unrouted plan: %v %v", got, err)
	}
	plain.Routing = got
	b, _ := json.Marshal(plain)
	if strings.Contains(string(b), `"routing"`) {
		t.Errorf("unrouted manifest carries a routing key: %s", b)
	}
	chain := &schemas.RoutingPolicy{Strategy: "fallback_chain", Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-6"}}, Fallbacks: []schemas.RouteTarget{{Model: "claude-haiku-4-5"}}}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Config: routedAgent(nil)}, "B": {Config: routedAgent(chain)},
	}}
	first, err := checkRouting(m)
	if err != nil || len(first) != 1 || len(first["B"]) != 64 {
		t.Fatalf("routed plan: %v %v", first, err)
	}
	second, _ := checkRouting(m)
	if first["B"] != second["B"] {
		t.Errorf("policy hash not stable: %s %s", first["B"], second["B"])
	}
	bad := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"B": {Config: routedAgent(&schemas.RoutingPolicy{Strategy: "weighted"})}}}
	if _, err := checkRouting(bad); err == nil || !strings.Contains(err.Error(), "arm B") {
		t.Errorf("invalid policy: %v", err)
	}
}

// TestRoutingOverride: an arm sets the policy with set: like any field.
func TestRoutingOverride(t *testing.T) {
	doc := []byte("id: toy\nadapter: scripted\nmodel: claude-sonnet-4-6\ngateway:\n  provider: anthropic\n")
	cfg, err := ResolveAgentConfig(doc, map[string]any{
		"gateway.routing": map[string]any{"strategy": "fallback_chain",
			"targets":   []any{map[string]any{"model": "claude-sonnet-4-6"}},
			"fallbacks": []any{map[string]any{"model": "claude-haiku-4-5"}}},
		"gateway.routing.limits.request_max": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Gateway.Routing
	if p == nil || p.Strategy != "fallback_chain" || p.Fallbacks[0].Model != "claude-haiku-4-5" || p.Limits.RequestMax != 3 {
		t.Fatalf("routing = %+v", p)
	}
}

// TestValidateRoutingRefusesTeams is 8-B-02: a routing policy reaches only
// the lead's key while the fidelity gate scores every member against it, so
// a team arm (team: or a2a.team) with gateway.routing is refused at plan time.
func TestValidateRoutingRefusesTeams(t *testing.T) {
	pol := &schemas.RoutingPolicy{Strategy: "fixed", Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-6"}}}
	gm := &schemas.GatewayManifest{Settings: map[string]string{"anthropic.max_retries": "0"}}
	team := routedAgent(pol)
	team.Team = []schemas.TeamMember{{ID: "lead"}, {ID: "worker", Model: "model-b"}}
	a2a := routedAgent(pol)
	a2a.A2A = &schemas.A2AAgent{Team: []schemas.A2AMember{{ID: "lead"}, {ID: "writer"}}}
	for name, cfg := range map[string]schemas.AgentConfig{"team": team, "a2a team": a2a} {
		if err := ValidateRouting(&cfg, gm); err == nil || !strings.Contains(err.Error(), "cannot be combined with a team") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	solo := routedAgent(pol)
	if err := ValidateRouting(&solo, gm); err != nil {
		t.Fatalf("solo routed arm refused: %v", err)
	}
}
