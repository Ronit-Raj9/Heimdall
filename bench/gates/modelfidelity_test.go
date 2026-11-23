package gates

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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
		{"dated snapshot of the alias is the same model", noFallback, []schemas.GatewayRequest{req("r1", "anthropic/claude-sonnet-4-5", "claude-sonnet-4-5-20250929")}, false, nil},
		{"other dated snapshot is a different model", noFallback, []schemas.GatewayRequest{req("r1", "claude-sonnet-4-5-20250514", "claude-sonnet-4-5-20250929")}, true, []string{"r1"}},
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
		{"claude-x", "claude-x-20250101", true},
		{"claude-x-20250101", "claude-x-20250202", false},
		{"gpt-5.4", "gpt-5.4-mini-2026-03-17", false},
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

// liveSnapshotRows are the seven rows of run r_v2079k4d8u (N200): OpenAI
// answers a request for gpt-5.4-mini as its dated snapshot.
func liveSnapshotRows(served string, fallbackIndex int) []schemas.GatewayRequest {
	var rows []schemas.GatewayRequest
	for i := 0; i < 7; i++ {
		rows = append(rows, schemas.GatewayRequest{ID: fmt.Sprintf("q%d", i), Kind: "message", Provider: "openai",
			ModelRequested: "gpt-5.4-mini", ModelServed: served, FallbackIndex: fallbackIndex})
	}
	return rows
}

func checkFidelity(t *testing.T, rc *schemas.RunContext, rows []schemas.GatewayRequest) *schemas.GateResult {
	t.Helper()
	res, err := newModelFidelityGate().Check(context.Background(), rc, &schemas.Evidence{Requests: &schemas.RequestLog{Requests: rows, Complete: true}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestModelFidelityAcceptsDatedSnapshot(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "gpt-5.4-mini"}}
	if res := checkFidelity(t, rc, liveSnapshotRows("gpt-5.4-mini-2026-03-17", 0)); res.Fired {
		t.Fatalf("dated snapshot fired the gate: %q", res.Detail)
	}
}

func TestModelFidelityFlagsOtherFamily(t *testing.T) {
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "gpt-5.4-mini"}}
	for _, served := range []string{"gpt-5.4", "gpt-5.4-2026-03-17", "gpt-4o-mini-2024-07-18", "claude-haiku-4-5"} {
		res := checkFidelity(t, rc, liveSnapshotRows(served, 0))
		if !res.Fired || !strings.Contains(res.Detail, "7 served by another model") {
			t.Fatalf("served %s: fired=%v detail=%q, want fired with 7 swaps", served, res.Fired, res.Detail)
		}
	}
}

func TestModelFidelityDeclaredFallbackDated(t *testing.T) {
	rows := []schemas.GatewayRequest{{ID: "f1", Kind: "message", ModelRequested: "gpt-6-luna", ModelServed: "gpt-5.4-mini-2026-03-17", FallbackIndex: 1}}
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Model: "gpt-6-luna", Gateway: schemas.GatewayPolicy{Fallbacks: []string{"openai/gpt-5.4-mini"}}}}
	if res := checkFidelity(t, rc, rows); res.Fired {
		t.Fatalf("declared fallback served dated fired the gate: %q", res.Detail)
	}
	rows[0].ModelServed = "gpt-5.4-2026-03-17"
	if res := checkFidelity(t, rc, rows); !res.Fired {
		t.Fatal("a fallback outside the declared family did not fire the gate")
	}
}

func TestModelFidelityVersionBumped(t *testing.T) {
	if v := newModelFidelityGate().GetVersion(); v != "1.1.0" {
		t.Fatalf("model_fidelity version %s, want 1.1.0 so Re-score checks stored runs again", v)
	}
	if version != "1.0.0" {
		t.Fatalf("shared gate version moved to %s; only model_fidelity changes version (TestBuiltin pins each gate)", version)
	}
}
