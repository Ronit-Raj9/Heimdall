package metrics

import (
	"context"
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func proofSteps(classes ...string) []schemas.StepRecord {
	out := []schemas.StepRecord{{Kind: schemas.StepModelTurn, Source: schemas.StepSourceGateway, Counted: true}}
	for _, c := range classes {
		out = append(out, schemas.StepRecord{Kind: schemas.StepToolRequest, Source: schemas.StepSourceGateway, ProofClass: c})
	}
	return out
}

func TestToolProofMetrics(t *testing.T) {
	complete := &schemas.RequestLog{Complete: true}
	on := []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}}
	ev := &schemas.Evidence{Requests: complete, Observers: on, Steps: proofSteps(
		schemas.ProofProxy, schemas.ProofProcess, schemas.ProofFileEffect, schemas.ProofShim,
		schemas.ProofResultReturned, schemas.ProofResultReturned, schemas.ProofNone, schemas.ProofProcess,
	)}
	want := map[string]float64{
		ToolProofRate: 4.0 / 8, ToolProofProxy: 1.0 / 8, ToolProofProcess: 2.0 / 8, ToolProofFileEffect: 1.0 / 8,
		ToolProofShim: 1.0 / 8, ToolProofResultReturned: 2.0 / 8, ToolProofNone: 1.0 / 8,
	}
	sum := 0.0
	for name, w := range want {
		v, err := newToolProofMetric(name).Compute(context.Background(), nil, ev)
		if err != nil || v.State != schemas.ValueObserved || v.V == nil || math.Abs(*v.V-w) > 1e-9 {
			t.Fatalf("%s = %+v (%v), want %v", name, v, err, w)
		}
		if name != ToolProofRate {
			sum += *v.V
		}
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("class shares add up to %v, want 1", sum)
	}
}

func TestToolProofMetricsUnknown(t *testing.T) {
	complete := &schemas.RequestLog{Complete: true}
	on := []schemas.ObserverStatus{{Observer: schemas.ObserverFS, State: schemas.ObserverOn}}
	tests := []struct {
		name string
		ev   *schemas.Evidence
		rate bool // only the rate is unknown
	}{
		{name: "no gateway log", ev: &schemas.Evidence{Steps: proofSteps(schemas.ProofProxy)}},
		{name: "no timeline", ev: &schemas.Evidence{Requests: complete}},
		{name: "no requested calls", ev: &schemas.Evidence{Requests: complete, Observers: on, Steps: proofSteps()}},
		{name: "timeline before proof classes", ev: &schemas.Evidence{Requests: complete, Observers: on, Steps: proofSteps("")}},
		{name: "no trusted witness available", ev: &schemas.Evidence{Requests: complete, Steps: proofSteps(schemas.ProofResultReturned)}, rate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{ToolProofRate, ToolProofNone} {
				v, _ := newToolProofMetric(name).Compute(context.Background(), nil, tt.ev)
				wantUnknown := name == ToolProofRate || !tt.rate
				if (v.State == schemas.ValueUnknown) != wantUnknown {
					t.Errorf("%s state %s, want unknown %v", name, v.State, wantUnknown)
				}
			}
		})
	}
	// A tool proxy ledger alone makes a trusted witness available.
	ev := &schemas.Evidence{Requests: complete, ToolCalls: []schemas.ToolCall{}, Steps: proofSteps(schemas.ProofNone)}
	if v, _ := newToolProofMetric(ToolProofRate).Compute(context.Background(), nil, ev); v.State != schemas.ValueObserved || *v.V != 0 {
		t.Fatalf("rate with a ledger = %+v, want 0", v)
	}
}
