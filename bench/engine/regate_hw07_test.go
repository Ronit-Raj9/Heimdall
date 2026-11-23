package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestRegateModelFidelityVersionBump checks that Re-score clears a stored
// model_fidelity result fired by a dated snapshot (N200): the gate's version
// changed, so it is checked again from the sealed gateway rows, while a gate
// at an unchanged version keeps its sealed result.
func TestRegateModelFidelityVersionBump(t *testing.T) {
	var fidelity schemas.Gate
	for _, g := range gates.Builtin() {
		if g.GetName() == gates.ModelFidelity {
			fidelity = g
		}
	}
	if fidelity == nil {
		t.Fatal("model_fidelity gate is not built in")
	}
	reg := &schemas.Registry{Gates: []schemas.Gate{fidelity, regateGate{name: "egress", version: "1.0.0", fire: true}}}
	var rows []schemas.GatewayRequest
	for _, id := range []string{"q1", "q2", "q3"} {
		rows = append(rows, schemas.GatewayRequest{ID: id, Kind: "message", Provider: "openai", ModelRequested: "gpt-5.4-mini", ModelServed: "gpt-5.4-mini-2026-03-17"})
	}
	ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: rows, Complete: true}}
	rc := &schemas.RunContext{Experiment: &schemas.Experiment{}, Agent: &schemas.AgentConfig{Model: "gpt-5.4-mini"}}
	sealed := []schemas.GateResult{
		{Gate: gates.ModelFidelity, Version: "1.0.0", Effect: schemas.GateInvalidate, Fired: true, Detail: "3 request(s): 3 served by another model, 0 undeclared fallback, 0 cache hit"},
		{Gate: "egress", Version: "1.0.0", Effect: schemas.GateVeto, Fired: false},
	}
	got, recomputed, _ := regateGates(context.Background(), reg, rc, ev, sealed)
	if len(recomputed) != 1 || recomputed[0] != gates.ModelFidelity {
		t.Fatalf("recomputed %v, want only model_fidelity", recomputed)
	}
	for _, r := range got {
		switch r.Gate {
		case gates.ModelFidelity:
			if r.Fired || r.Version != "1.1.0" {
				t.Fatalf("model_fidelity after re-score: fired=%v version=%s detail=%q", r.Fired, r.Version, r.Detail)
			}
		case "egress":
			if r.Fired {
				t.Fatal("egress was checked again although its version did not change")
			}
		}
	}
}
