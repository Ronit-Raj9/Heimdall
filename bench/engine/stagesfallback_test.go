package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestGatewayIntegrityDeclaredFallback: a fallback row served by the
// routing policy's declared fallback model is a fallback event, not an
// "unplanned gateway fallback" integrity error; one without a declared chain
// stays an error (audit 8-B-08).
func TestGatewayIntegrityDeclaredFallback(t *testing.T) {
	row := schemas.GatewayRequest{ID: "r1", FallbackIndex: 1, ModelRequested: "gpt-4o", ModelServed: "claude-haiku-4-5"}
	log := &schemas.RequestLog{Requests: []schemas.GatewayRequest{row}}
	run := &schemas.Run{RunID: "run1"}
	declared := &schemas.AgentConfig{Model: "gpt-4o"}
	declared.Gateway.Routing = &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain,
		Targets: []schemas.RouteTarget{{Model: "gpt-4o", Weight: 1}}, Fallbacks: []schemas.RouteTarget{{Model: "claude-haiku-4-5"}}}
	plain := &schemas.AgentConfig{Model: "gpt-4o"}
	for _, tc := range []struct {
		name   string
		agent  *schemas.AgentConfig
		typ    schemas.EventType
		status schemas.EventStatus
	}{
		{"declared", declared, schemas.EventFallback, schemas.EventStatusSuccess},
		{"undeclared", plain, schemas.EventIntegrity, schemas.EventStatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evs := gatewayIntegrityEvents(&schemas.RunContext{Run: run, Agent: tc.agent}, log)
			if len(evs) != 1 || evs[0].Type != tc.typ || evs[0].Status != tc.status {
				t.Fatalf("events = %+v, want one %s/%s", evs, tc.typ, tc.status)
			}
		})
	}
}
