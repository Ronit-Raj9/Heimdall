package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The failover arm's policy (lane C07) must be one the run key's routing
// rule accepts: fallback_chain, primary target first, fallbacks after.
func TestFailoverPolicyMakesRouteRule(t *testing.T) {
	arms, err := failover.Plan("openai", "gpt-4o", failover.Option{Enabled: true,
		Fallbacks: []schemas.RouteTarget{{Model: "gpt-4o-mini"}, {Model: "gpt-4.1-mini"}}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := routeRule("r_abc", "vk_1", "openai", arms[1].Policy)
	if err != nil || rule == nil {
		t.Fatalf("rule %v err %v", rule, err)
	}
	if len(rule.Targets) != 1 || rule.Targets[0].Model != "gpt-4o" || rule.Targets[0].Weight != 1 {
		t.Fatalf("targets %+v", rule.Targets)
	}
	if len(rule.Fallbacks) != 2 || rule.Fallbacks[0] != "openai/gpt-4o-mini" || rule.Fallbacks[1] != "openai/gpt-4.1-mini" {
		t.Fatalf("fallbacks %v", rule.Fallbacks)
	}
	if rule, err := routeRule("r_abc", "vk_1", "openai", arms[2].Policy); err != nil || rule != nil {
		t.Fatalf("no-failover arm should need no rule, got %v %v", rule, err)
	}
}
