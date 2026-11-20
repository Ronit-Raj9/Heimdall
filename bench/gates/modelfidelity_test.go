package gates

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestModelFidelity(t *testing.T) {
	req := func(id, requested, served string) schemas.GatewayRequest {
		return schemas.GatewayRequest{ID: id, Kind: "message", ModelRequested: requested, ModelServed: served}
	}
	noFallback := &schemas.RunContext{Agent: &schemas.AgentConfig{}}
	withFallback := &schemas.RunContext{Agent: &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Fallbacks: []string{"anthropic/claude-haiku-4-5"}}}}
	semCache := &schemas.RunContext{Agent: &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{SemanticCache: &schemas.BifrostSemanticCache{}}}}}
	fallbackRow := req("r2", "claude-sonnet-4-5", "claude-haiku-4-5")
	fallbackRow.FallbackIndex = 1
	cached := req("r3", "claude-sonnet-4-5", "claude-sonnet-4-5")
	cached.CacheHit = true
	countTokens := req("r4", "claude-sonnet-4-5", "other-model")
	countTokens.Kind = "count_tokens"
	cases := []struct {
		name     string
		rc       *schemas.RunContext
		reqs     []schemas.GatewayRequest
		fired    bool
		evidence []string
	}{
		{"identical", noFallback, []schemas.GatewayRequest{req("r1", "claude-sonnet-4-5", "claude-sonnet-4-5")}, false, nil},
		{"provider prefix on request", noFallback, []schemas.GatewayRequest{req("r1", "anthropic/claude-sonnet-4-5", "claude-sonnet-4-5")}, false, nil},
		{"provider prefix on served", noFallback, []schemas.GatewayRequest{req("r1", "claude-sonnet-4-5", "anthropic/claude-sonnet-4-5")}, false, nil},
		{"date suffix is a different model", noFallback, []schemas.GatewayRequest{req("r1", "anthropic/claude-sonnet-4-5", "claude-sonnet-4-5-20250929")}, true, []string{"r1"}},
		{"swapped model", noFallback, []schemas.GatewayRequest{req("r1", "claude-sonnet-4-5", "claude-haiku-4-5")}, true, []string{"r1"}},
		{"same org different model", noFallback, []schemas.GatewayRequest{req("r1", "org/model-a", "org/model-b")}, true, []string{"r1"}},
		{"error row without served model", noFallback, []schemas.GatewayRequest{req("r1", "claude-sonnet-4-5", "")}, false, nil},
		{"undeclared fallback", noFallback, []schemas.GatewayRequest{fallbackRow}, true, []string{"r2"}},
		{"declared fallback", withFallback, []schemas.GatewayRequest{fallbackRow}, false, nil},
		{"cache hit", noFallback, []schemas.GatewayRequest{cached}, true, []string{"r3"}},
		{"cache hit on a semantic cache arm is intended", semCache, []schemas.GatewayRequest{cached}, false, nil},
		{"count_tokens ignored", noFallback, []schemas.GatewayRequest{countTokens}, false, nil},
	}
	g := newModelFidelityGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: c.reqs, Complete: true}}
			res, err := g.Check(context.Background(), c.rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || !reflect.DeepEqual(res.Evidence, c.evidence) {
				t.Fatalf("fired=%v evidence=%v detail=%q, want %v %v", res.Fired, res.Evidence, res.Detail, c.fired, c.evidence)
			}
		})
	}
	res, err := g.Check(context.Background(), noFallback, &schemas.Evidence{})
	if err != nil || res.Fired {
		t.Fatalf("no gateway log: fired=%v err=%v", res.Fired, err)
	}
}

func TestSameModel(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"anthropic/claude-x", "claude-x", true},
		{"openrouter/anthropic/claude-x", "anthropic/claude-x", true},
		{"claude-x", "claude-x-20250101", false},
		{"meta-llama/Llama-3-8B", "meta-llama/Llama-3-70B", false},
	}
	for _, c := range cases {
		t.Run(c.a+"|"+c.b, func(t *testing.T) {
			if got := sameModel(c.a, c.b); got != c.want {
				t.Fatalf("sameModel(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
