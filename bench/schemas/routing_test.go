package schemas

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRoutingPolicyHelpers (W9): the primary target, the model set, the one
// line description and the arm differences.
func TestRoutingPolicyHelpers(t *testing.T) {
	var none *RoutingPolicy
	if _, ok := none.Primary(); ok || none.Models() != nil {
		t.Error("a nil policy has no primary and no models")
	}
	pol := &RoutingPolicy{Strategy: RoutingWeighted,
		Targets:   []RouteTarget{{Model: "strong", Weight: 0.7}, {Provider: "anthropic", Model: "cheap", Weight: 0.3}},
		Fallbacks: []RouteTarget{{Model: "cheap"}, {Model: "backup"}}, Limits: &RouteLimits{BudgetUSD: 0.5, TokenMax: 1000}}
	if p, ok := pol.Primary(); !ok || p.Model != "strong" {
		t.Errorf("primary = %+v", p)
	}
	if got := strings.Join(pol.Models(), ","); got != "strong,cheap,backup" {
		t.Errorf("models = %s", got)
	}
	cfg := &AgentConfig{Model: "strong", Gateway: GatewayPolicy{Provider: "anthropic", Routing: pol}}
	want := "weighted: anthropic/strong 0.7, anthropic/cheap 0.3; fallbacks anthropic/cheap then anthropic/backup; limits budget $0.5 per 1d, 1000 tokens per 1h"
	if got := RoutingSummaryLine(cfg); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if got := RoutingSummaryLine(&AgentConfig{Model: "m"}); got != "fixed anthropic/m (no routing policy)" {
		t.Errorf("fixed line = %q", got)
	}
	diffs := RoutingDifferences([]RoutingArm{{Arm: "A", Model: "strong"}, {Arm: "B", Model: "strong", Policy: pol}})
	if strings.Join(diffs, "|") != "strategy: A none; B weighted|targets: A strong; B strong 0.7, cheap 0.3|fallbacks: A none; B cheap, backup|limits: A none; B budget 0.5, requests 0, tokens 1000" {
		t.Errorf("differences = %q", diffs)
	}
	if d := RoutingDifferences([]RoutingArm{{Arm: "A"}}); len(d) != 0 {
		t.Errorf("one arm differs: %q", d)
	}
}

// TestRoutingAdditive: configs and manifests without routing encode exactly
// as before, so their hashes do not move.
func TestRoutingAdditive(t *testing.T) {
	b, err := json.Marshal(GatewayPolicy{Provider: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"provider":"anthropic","max_retries":0}` {
		t.Errorf("gateway policy = %s", b)
	}
	cases := []struct {
		v   any
		key string
	}{
		{FaultPlan{Mode: "persistent", FromCall: 3, Status: 503}, `"model"`},
		{RunKey{ID: "k"}, `"routing_rule_id"`},
		{GatewayRequest{ID: "r"}, `"routing_`},
		{StepRecord{Seq: 1}, `"model_switch"`},
		{Manifest{ExpID: "e"}, `"routing"`},
		{GatewayHygiene{}, `"bench_routing"`},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.v)
		if strings.Contains(string(b), c.key) {
			t.Errorf("%T encodes %s without routing: %s", c.v, c.key, b)
		}
	}
}
