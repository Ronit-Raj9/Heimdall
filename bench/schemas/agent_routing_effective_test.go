package schemas

import (
	"reflect"
	"testing"
)

// TestRoutingEffectiveModels is 8-B-01: a rule policy's effective models
// include the agent's model (unmatched requests keep it); other strategies
// and a rule already naming it are unchanged.
func TestRoutingEffectiveModels(t *testing.T) {
	rule := &RoutingPolicy{Strategy: RoutingRule, When: "complexity_tier", Targets: []RouteTarget{{Model: "claude-sonnet-4-5"}}}
	if got := rule.EffectiveModels("claude-haiku-4-5"); !reflect.DeepEqual(got, []string{"claude-sonnet-4-5", "claude-haiku-4-5"}) {
		t.Fatalf("rule: %v", got)
	}
	if got := rule.EffectiveModels("anthropic/claude-sonnet-4-5"); !reflect.DeepEqual(got, []string{"claude-sonnet-4-5"}) {
		t.Fatalf("rule naming the agent model: %v", got)
	}
	fixed := &RoutingPolicy{Strategy: RoutingFixed, Targets: []RouteTarget{{Model: "a"}}}
	if got := fixed.EffectiveModels("b"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("fixed: %v", got)
	}
	var none *RoutingPolicy
	if got := none.EffectiveModels("a"); got != nil {
		t.Fatalf("nil policy: %v", got)
	}
}
