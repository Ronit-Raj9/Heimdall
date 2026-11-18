package gates

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelFidelityRouted (W9): under a routing policy a served model the
// policy names is an expected switch, reported with its count and never a
// fire; a model outside the policy, an undeclared fallback and a cache hit
// still fire.
func TestModelFidelityRouted(t *testing.T) {
	row := func(id, served string, fallback int) schemas.GatewayRequest {
		// ModelRequested is what Bifrost sent upstream: for a routed row, the routed model.
		return schemas.GatewayRequest{ID: id, Kind: "message", ModelRequested: served, ModelServed: served, FallbackIndex: fallback, Status: "success"}
	}
	chain := &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain,
		Targets: []schemas.RouteTarget{{Model: "strong"}}, Fallbacks: []schemas.RouteTarget{{Provider: "anthropic", Model: "cheap"}}}
	split := &schemas.RoutingPolicy{Strategy: schemas.RoutingWeighted,
		Targets: []schemas.RouteTarget{{Model: "strong", Weight: 0.5}, {Model: "cheap", Weight: 0.5}}}
	rc := func(p *schemas.RoutingPolicy) *schemas.RunContext {
		return &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "strong", Gateway: schemas.GatewayPolicy{Routing: p}}}
	}
	primaryFailed := schemas.GatewayRequest{ID: "e1", Kind: "message", ModelRequested: "strong", Status: "error", ErrorStatus: 503}
	cached := row("c1", "strong", 0)
	cached.CacheHit = true
	cases := []struct {
		name     string
		rc       *schemas.RunContext
		reqs     []schemas.GatewayRequest
		fired    bool
		evidence []string
		detail   string
	}{
		{"primary only", rc(chain), []schemas.GatewayRequest{row("r1", "strong", 0)}, false, nil, ""},
		{"declared fallback is an expected switch", rc(chain), []schemas.GatewayRequest{primaryFailed, row("r2", "anthropic/cheap", 1)}, false, nil,
			"routing policy fallback_chain: 1 request(s) served by another policy model than strong (expected switch)"},
		{"weighted split counts switches", rc(split), []schemas.GatewayRequest{row("r1", "strong", 0), row("r2", "cheap", 0), row("r3", "cheap", 0)}, false, nil,
			"2 request(s) served by another policy model"},
		{"model outside the policy fires", rc(chain), []schemas.GatewayRequest{row("r1", "opus", 0)}, true, []string{"r1"}, "1 served by a model outside the routing policy"},
		{"fallback without declared fallbacks fires", rc(split), []schemas.GatewayRequest{row("r1", "cheap", 1)}, true, []string{"r1"}, "1 undeclared fallback"},
		{"cache hit fires", rc(chain), []schemas.GatewayRequest{cached}, true, []string{"c1"}, "1 cache hit"},
	}
	g := newModelFidelityGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), c.rc, &schemas.Evidence{Requests: &schemas.RequestLog{Requests: c.reqs, Complete: true}})
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || !reflect.DeepEqual(res.Evidence, c.evidence) || !strings.Contains(res.Detail, c.detail) {
				t.Fatalf("fired=%v evidence=%v detail=%q, want %v %v %q", res.Fired, res.Evidence, res.Detail, c.fired, c.evidence, c.detail)
			}
		})
	}
}
