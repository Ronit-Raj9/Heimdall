package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func squeezeRC() *schemas.RunContext {
	return &schemas.RunContext{Probe: &schemas.ProbeSpec{Layer: schemas.LayerGateway, Apply: schemas.ProbeApply{Fault: &schemas.FaultPlan{Mode: "budget_squeeze", Call: 3}}}}
}

func obsNum(t *testing.T, v schemas.Value) float64 {
	t.Helper()
	f, ok := v.Float()
	if !ok {
		t.Fatalf("value %+v not observed", v)
	}
	return f
}

func TestGracefulFinish(t *testing.T) {
	m := newGracefulFinishMetric()
	ctx := context.Background()
	if v, _ := m.Compute(ctx, &schemas.RunContext{}, &schemas.Evidence{}); v.State != schemas.ValueNotApplicable {
		t.Fatalf("clean run: %+v", v)
	}
	diff := &schemas.Diff{Files: []schemas.DiffFile{{Path: "a.py", Status: "modified"}}}
	cases := []struct {
		name string
		ev   *schemas.Evidence
		want float64
	}{
		{"passed", &schemas.Evidence{Grade: &schemas.Grade{Passed: true}}, 1},
		{"honest partial", &schemas.Evidence{Grade: &schemas.Grade{}, Claim: &schemas.Claim{Made: false}, Diff: diff}, 1},
		{"false claim", &schemas.Evidence{Grade: &schemas.Grade{}, Claim: &schemas.Claim{Made: true}, Diff: diff}, 0},
		{"nothing left", &schemas.Evidence{Grade: &schemas.Grade{}, Claim: &schemas.Claim{}}, 0},
	}
	for _, c := range cases {
		v, err := m.Compute(ctx, squeezeRC(), c.ev)
		if err != nil || obsNum(t, v) != c.want {
			t.Fatalf("%s: %+v %v", c.name, v, err)
		}
	}
	if v, _ := m.Compute(ctx, squeezeRC(), &schemas.Evidence{Grade: &schemas.Grade{}}); v.IsObserved() {
		t.Fatalf("no claim must be unknown: %+v", v)
	}
}

func TestCostNormalizedSuccess(t *testing.T) {
	m := newCostNormalizedSuccessMetric()
	ev := &schemas.Evidence{Grade: &schemas.Grade{Passed: true}, Cost: []schemas.CostRow{{USD: schemas.Observed(0.5, schemas.EvidenceGateway)}}}
	v, err := m.Compute(context.Background(), &schemas.RunContext{}, ev)
	if err != nil || obsNum(t, v) != 2 {
		t.Fatalf("got %+v %v", v, err)
	}
	ev.Cost = nil
	if v, _ := m.Compute(context.Background(), &schemas.RunContext{}, ev); v.IsObserved() {
		t.Fatalf("unknown cost must be unknown: %+v", v)
	}
}

func TestResumedWithoutRedo(t *testing.T) {
	m := newResumedWithoutRedoMetric()
	ctx := context.Background()
	if v, _ := m.Compute(ctx, nil, &schemas.Evidence{}); v.State != schemas.ValueNotApplicable {
		t.Fatalf("no kill: %+v", v)
	}
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	kill := &schemas.ProcessKill{KilledAt: at, Resumed: true}
	before := schemas.ToolCall{Server: "shop", Tool: "refund", ArgsSHA256: "x", Started: at.Add(-time.Minute)}
	other := schemas.ToolCall{Server: "shop", Tool: "refund", ArgsSHA256: "y", Started: at.Add(time.Minute)}
	same := before
	same.Started = at.Add(time.Minute)
	v, _ := m.Compute(ctx, nil, &schemas.Evidence{ProcessKill: kill, ToolCalls: []schemas.ToolCall{before, other}})
	if obsNum(t, v) != 1 {
		t.Fatalf("no redo: %+v", v)
	}
	v, _ = m.Compute(ctx, nil, &schemas.Evidence{ProcessKill: kill, ToolCalls: []schemas.ToolCall{before, same}})
	if obsNum(t, v) != 0 {
		t.Fatalf("redo: %+v", v)
	}
	failed := before
	failed.IsError = true
	v, _ = m.Compute(ctx, nil, &schemas.Evidence{ProcessKill: kill, ToolCalls: []schemas.ToolCall{failed, same}})
	if obsNum(t, v) != 1 {
		t.Fatalf("retrying a failed call is no redo: %+v", v)
	}
	if v, _ := m.Compute(ctx, nil, &schemas.Evidence{ProcessKill: kill}); v.IsObserved() {
		t.Fatalf("no ledger must be unknown: %+v", v)
	}
}
