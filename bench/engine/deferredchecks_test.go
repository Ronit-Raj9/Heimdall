package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestDeferredPlanSkipsGatewayCapabilityChecks: a hosted plan (gateway
// deferred to runners) does not demand gateway settings or capabilities only
// a runner has; a plan with a gateway still checks them (audit 8-B-04).
func TestDeferredPlanSkipsGatewayCapabilityChecks(t *testing.T) {
	ctx := context.Background()
	cfg := &schemas.AgentConfig{}
	cfg.Gateway.Routing = &schemas.RoutingPolicy{Strategy: schemas.RoutingRule, When: `complexity_tier == "SIMPLE"`}
	for _, deferred := range []bool{true, false} {
		p := &Planner{reg: &schemas.Registry{}, deferGateway: deferred}
		gm := &schemas.GatewayManifest{Name: "fake"}
		if deferred {
			gm = deferredGateway()
		}
		errs := []error{p.checkComplexityRouting(ctx, cfg), p.checkPromptCacheProvider(ctx, "openai")}
		errs = append(errs, routeRetries(0, "openai", gm)...)
		for i, err := range errs {
			if (err != nil) == deferred {
				t.Errorf("deferred=%v check %d: err = %v", deferred, i, err)
			}
		}
	}
}
