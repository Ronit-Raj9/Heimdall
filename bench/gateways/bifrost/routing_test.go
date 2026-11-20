package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const testCheap = "claude-haiku-4-5"

// bifrostCreateVK mirrors the fields the bench sends of Bifrost v2.5.1's
// CreateVirtualKeyRequest (transports/bifrost-http/handlers/governance.go:371),
// tags copied verbatim. Decoding the bench's body into it with unknown fields
// disallowed proves every key the bench sends is one Bifrost reads.
type bifrostCreateVK struct {
	Name            string `json:"name"`
	ProviderConfigs []struct {
		Provider      string            `json:"provider"`
		Weight        *float64          `json:"weight,omitempty"`
		AllowedModels []string          `json:"allowed_models,omitempty"`
		Budgets       []bifrostBudget   `json:"budgets,omitempty"`
		RateLimit     *bifrostRateLimit `json:"rate_limit,omitempty"`
		KeyIDs        []string          `json:"key_ids,omitempty"`
	} `json:"provider_configs,omitempty"`
	Budgets   []bifrostBudget   `json:"budgets,omitempty"`
	RateLimit *bifrostRateLimit `json:"rate_limit,omitempty"`
	IsActive  *bool             `json:"is_active,omitempty"`
}

// bifrostBudget mirrors CreateBudgetRequest (handlers/governance.go:573).
type bifrostBudget struct {
	MaxLimit      float64 `json:"max_limit"`
	ResetDuration string  `json:"reset_duration"`
}

// bifrostRateLimit mirrors CreateRateLimitRequest (handlers/governance.go:603).
type bifrostRateLimit struct {
	TokenMaxLimit        *int64  `json:"token_max_limit,omitempty"`
	TokenResetDuration   *string `json:"token_reset_duration,omitempty"`
	RequestMaxLimit      *int64  `json:"request_max_limit,omitempty"`
	RequestResetDuration *string `json:"request_reset_duration,omitempty"`
}

// bifrostCreateRule mirrors CreateRoutingRuleRequest
// (transports/bifrost-http/handlers/routing.go:175) and RoutingTarget (:164).
// Fallbacks decode as the legacy "provider/model" strings
// configstore/tables.RoutingFallback.UnmarshalJSON accepts.
type bifrostCreateRule struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
	ChainRule     *bool  `json:"chain_rule,omitempty"`
	CelExpression string `json:"cel_expression"`
	Targets       []struct {
		Provider      *string `json:"provider,omitempty"`
		Model         *string `json:"model,omitempty"`
		KeyID         *string `json:"key_id,omitempty"`
		Weight        float64 `json:"weight"`
		TTFTTimeoutMs *int    `json:"ttft_timeout_ms,omitempty"`
	} `json:"targets"`
	Fallbacks []string       `json:"fallbacks,omitempty"`
	Scope     string         `json:"scope,omitempty"`
	ScopeID   *string        `json:"scope_id,omitempty"`
	Query     map[string]any `json:"query,omitempty"`
	Priority  int            `json:"priority,omitempty"`
}

// strictDecode decodes b into v refusing unknown fields.
func strictDecode(t *testing.T, b []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("body %s is not a Bifrost v2.5.1 request: %v", b, err)
	}
}

// canonical re-encodes JSON with sorted keys for golden comparison.
func canonical(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func chainPolicy() *schemas.RoutingPolicy {
	return &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain,
		Targets:   []schemas.RouteTarget{{Model: testModel}},
		Fallbacks: []schemas.RouteTarget{{Provider: "anthropic", Model: testCheap}},
		Limits:    &schemas.RouteLimits{BudgetUSD: 0.5, RequestMax: 3}}
}

func TestRoutedKeyBody(t *testing.T) {
	tests := []struct {
		name string
		pol  *schemas.RoutingPolicy
		want string
		err  string
	}{
		{name: "fallback chain with limits", pol: chainPolicy(),
			want: `{"budgets":[{"max_limit":0.5,"reset_duration":"1d"}],"is_active":true,"name":"r_route0001","provider_configs":[{"allowed_models":["claude-sonnet-4-6","claude-haiku-4-5"],"key_ids":["key-1"],"provider":"anthropic","weight":1}],"rate_limit":{"request_max_limit":3,"request_reset_duration":"1h"}}`},
		{name: "fixed with token limit only", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingFixed,
			Targets: []schemas.RouteTarget{{Model: testModel}}, Limits: &schemas.RouteLimits{TokenMax: 1000, TokenReset: "5m"}},
			want: `{"is_active":true,"name":"r_route0001","provider_configs":[{"allowed_models":["claude-sonnet-4-6"],"key_ids":["key-1"],"provider":"anthropic","weight":1}],"rate_limit":{"token_max_limit":1000,"token_reset_duration":"5m"}}`},
		{name: "another provider is refused", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain,
			Targets: []schemas.RouteTarget{{Model: testModel}}, Fallbacks: []schemas.RouteTarget{{Provider: "openai", Model: "gpt-5-mini"}}},
			err: "not on the run's provider anthropic"},
		{name: "no target", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingFixed}, err: "has no target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := routedKeyBody("r_route0001", "anthropic", "key-1", tt.pol)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := canonical(t, body); got != tt.want {
				t.Errorf("body =\n%s\nwant\n%s", got, tt.want)
			}
			raw, _ := json.Marshal(body)
			strictDecode(t, raw, &bifrostCreateVK{})
		})
	}
}

func TestRouteRule(t *testing.T) {
	desc := func(s string) string {
		return "Heimdall routing policy under test for run r_route0001 (" + s + "); deleted at run cleanup"
	}
	tests := []struct {
		name string
		pol  *schemas.RoutingPolicy
		want string // "" = no rule
		err  string
	}{
		{name: "fixed needs no rule", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingFixed, Targets: []schemas.RouteTarget{{Model: testModel}}}},
		{name: "fallback chain", pol: chainPolicy(),
			want: `{"cel_expression":"true","description":"` + desc("fallback_chain") + `","enabled":true,"fallbacks":["anthropic/claude-haiku-4-5"],"name":"heimdall-route-r_route0001","priority":0,"scope":"virtual_key","scope_id":"vk-1","targets":[{"model":"claude-sonnet-4-6","provider":"anthropic","weight":1}]}`},
		{name: "weighted split", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingWeighted,
			Targets: []schemas.RouteTarget{{Model: testModel, Weight: 0.7}, {Provider: "anthropic", Model: testCheap, Weight: 0.3}}},
			want: `{"cel_expression":"true","description":"` + desc("weighted") + `","enabled":true,"name":"heimdall-route-r_route0001","priority":0,"scope":"virtual_key","scope_id":"vk-1","targets":[{"model":"claude-sonnet-4-6","provider":"anthropic","weight":0.7},{"model":"claude-haiku-4-5","provider":"anthropic","weight":0.3}]}`},
		{name: "rule with condition", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingRule, When: ` request_type == "count_tokens" `,
			Targets: []schemas.RouteTarget{{Model: testCheap}}},
			want: `{"cel_expression":"request_type == \"count_tokens\"","description":"` + desc("rule") + `","enabled":true,"name":"heimdall-route-r_route0001","priority":0,"scope":"virtual_key","scope_id":"vk-1","targets":[{"model":"claude-haiku-4-5","provider":"anthropic","weight":1}]}`},
		{name: "weights not summing to 1", pol: &schemas.RoutingPolicy{Strategy: schemas.RoutingWeighted,
			Targets: []schemas.RouteTarget{{Model: testModel, Weight: 0.5}, {Model: testCheap, Weight: 0.2}}}, err: "sum to 0.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := routeRule("r_route0001", "vk-1", "anthropic", tt.pol)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if rule != nil {
					t.Fatalf("rule = %+v, want none", rule)
				}
				return
			}
			if got := canonical(t, rule); got != tt.want {
				t.Errorf("rule =\n%s\nwant\n%s", got, tt.want)
			}
			raw, _ := json.Marshal(rule)
			strictDecode(t, raw, &bifrostCreateRule{})
		})
	}
}

func TestMintRoutedKeyAndCleanup(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_route0001", testModel)
	rc.Agent.Gateway.Routing = chainPolicy()
	key, err := g.MintKey(ctx, rc)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	if key.RoutingRuleID != "rule-1" || len(f.rules) != 1 {
		t.Fatalf("key = %+v, rules = %v", key, f.rules)
	}
	vk := f.lastBody("POST /api/governance/virtual-keys")
	pc := vk["provider_configs"].([]any)[0].(map[string]any)
	if fmt.Sprint(pc["allowed_models"]) != "[claude-sonnet-4-6 claude-haiku-4-5]" || vk["budgets"] == nil {
		t.Errorf("virtual key body = %v", vk)
	}
	rule := f.lastBody("POST /api/routing/rules")
	if rule["scope"] != "virtual_key" || rule["scope_id"] != key.ID || rule["name"] != "heimdall-route-r_route0001" {
		t.Errorf("rule body = %v", rule)
	}
	if got := fmt.Sprint(g.models["fp-slot-1"][testCheap], g.models["fp-slot-1"][testModel]); got != "true true" {
		t.Errorf("slot models = %v, want both policy models", g.models["fp-slot-1"])
	}
	if err := g.RevokeKey(ctx, key); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if len(f.rules) != 0 || len(f.vks) != 0 || f.count("DELETE /api/routing/rules/{id}") != 1 {
		t.Errorf("after revoke: rules %v, keys %v", f.rules, f.vks)
	}
	if err := g.RevokeKey(ctx, key); err != nil { // a rule already gone counts as deleted
		t.Fatalf("second RevokeKey: %v", err)
	}
}

func TestMintRoutedKeyRuleFailure(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	f.down = map[string]bool{"POST /api/routing/rules": true}
	rc := runContext("r_route0002", testModel)
	rc.Agent.Gateway.Routing = chainPolicy()
	if _, err := g.MintKey(ctx, rc); err == nil || !strings.Contains(err.Error(), "create routing rule heimdall-route-r_route0002") {
		t.Fatalf("err = %v", err)
	}
	if len(f.vks) != 0 {
		t.Errorf("the key outlived its failed rule: %v", f.vks)
	}
	f.down = nil
	if _, err := g.MintKey(ctx, runContext("r_route0003", testModel)); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
}

func TestRevokeStaleRules(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	f.rules = []map[string]any{
		{"id": "a", "name": RouteRuleName("r_active01"), "scope": "virtual_key", "scope_id": "vk-a"},
		{"id": "b", "name": RouteRuleName("r_stale001"), "scope": "virtual_key", "scope_id": "vk-b"},
		{"id": "c", "name": "team-rule", "scope": "virtual_key", "scope_id": "vk-c"},
		{"id": "d", "name": RouteRuleName("r_stale002"), "scope": "global"},
	}
	f.vks = []map[string]any{{"id": "vk-a", "name": "r_active01"}, {"id": "vk-b", "name": "r_stale001"}}
	n, err := g.RevokeStale(context.Background(), map[string]bool{"r_active01": true})
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, r := range f.rules {
		left = append(left, r["id"].(string))
	}
	if n != 1 || fmt.Sprint(left) != "[a c d]" || len(f.vks) != 1 {
		t.Errorf("n = %d, rules left %v, keys left %v", n, left, f.vks)
	}
}

func TestParseDetailRouting(t *testing.T) {
	raw := `{"id":"row-2","parent_request_id":"row-1","object":"chat_completion","provider":"anthropic","model":"claude-haiku-4-5",` +
		`"fallback_index":1,"status":"success","routing_rule_id":"rule-1","routing_rule_name":"heimdall-route-r_route0001",` +
		`"routing_engines_used":["routing-rule","core"]}`
	p, err := parseDetail([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	r := p.Req
	if r.RoutingRuleID != "rule-1" || r.RoutingRuleName != "heimdall-route-r_route0001" || fmt.Sprint(r.RoutingEngines) != "[routing-rule core]" ||
		r.FallbackIndex != 1 || r.ParentID != "row-1" {
		t.Errorf("parsed = %+v", r)
	}
}

// postRule and deleteRule serve the routing rules API of the fake (f.mu is
// held by guard).
func (f *fakeBifrost) postRule(w http.ResponseWriter, _ *http.Request) {
	body := f.bodies["POST /api/routing/rules"][len(f.bodies["POST /api/routing/rules"])-1]
	rule := map[string]any{"id": fmt.Sprintf("rule-%d", f.calls["POST /api/routing/rules"])}
	for k, v := range body {
		rule[k] = v
	}
	f.rules = append(f.rules, rule)
	writeJSONT(w, 200, map[string]any{"message": "Routing rule created successfully", "rule": rule})
}

func (f *fakeBifrost) deleteRule(w http.ResponseWriter, r *http.Request) {
	for i, rule := range f.rules {
		if rule["id"] == r.PathValue("id") {
			f.rules = append(f.rules[:i:i], f.rules[i+1:]...)
			writeJSONT(w, 200, map[string]any{"message": "Routing rule deleted successfully"})
			return
		}
	}
	writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "routing rule not found"}})
}

// TestRevokeKeyRuleFailure (W9): a rule that cannot be deleted does not keep
// the key or the slot; the error names it and the janitor deletes the rule.
func TestRevokeKeyRuleFailure(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_route0004", testModel)
	rc.Agent.Gateway.Routing = chainPolicy()
	key, err := g.MintKey(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	f.down = map[string]bool{"DELETE /api/routing/rules/{id}": true}
	if err := g.RevokeKey(ctx, key); err == nil || !strings.Contains(err.Error(), "delete routing rule") {
		t.Fatalf("err = %v", err)
	}
	if len(f.vks) != 0 || len(f.rules) != 1 {
		t.Errorf("keys %v rules %v", f.vks, f.rules)
	}
	f.down = nil
	if _, err := g.MintKey(ctx, runContext("r_route0005", testModel)); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	if _, err := g.MintKey(ctx, runContext("r_route0006", testModel)); err != nil {
		t.Fatalf("second slot: %v", err)
	}
}

// TestMintComplexityRoutedKeyAllowsAgentModel is 8-B-01: a complexity
// router's key allows its routed target and the agent's own model, which
// serves every request the rule does not match.
func TestMintComplexityRoutedKeyAllowsAgentModel(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	rc := runContext("r_route0009", testCheap)
	rc.Agent.Gateway.Routing = &schemas.RoutingPolicy{Strategy: schemas.RoutingRule, When: `complexity_tier == "COMPLEX"`,
		Targets: []schemas.RouteTarget{{Model: testModel}}}
	key, err := g.MintKey(ctx, rc)
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	vk := f.lastBody("POST /api/governance/virtual-keys")
	pc := vk["provider_configs"].([]any)[0].(map[string]any)
	if got := fmt.Sprint(pc["allowed_models"]); got != fmt.Sprint([]string{testModel, testCheap}) {
		t.Fatalf("allowed_models = %s, want the routed target and the agent's model", got)
	}
	if got := fmt.Sprint(g.models["fp-slot-1"][testCheap], g.models["fp-slot-1"][testModel]); got != "true true" {
		t.Errorf("slot models = %v", g.models["fp-slot-1"])
	}
	_ = g.RevokeKey(ctx, key)
}
