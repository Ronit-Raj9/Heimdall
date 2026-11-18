package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestA2AMetrics(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	l := func(v float64) *float64 { return &v }
	rc := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a/card"}}}
	ev := &schemas.Evidence{A2A: &schemas.A2AEvidence{Complete: true,
		Operations: []schemas.A2AOperation{
			{Caller: "bench", Timestamp: t0, LatencyMS: l(100), TaskID: "t1", Status: "success"},
			{Caller: "lead", Timestamp: t0.Add(10 * time.Millisecond), LatencyMS: l(5), TaskID: "t2", Status: "error"},
			{Caller: "bench", Timestamp: t0.Add(200 * time.Millisecond), LatencyMS: l(50), TaskID: "t1", Status: "success"},
		},
		TaskStates: map[string]string{"t1": "TASK_STATE_COMPLETED", "t2": "TASK_STATE_FAILED"},
		Hops:       []schemas.A2AHop{{From: "lead", To: "writer"}},
	}}
	want := map[string]float64{A2ATasks: 2, A2ALatencyMS: 250, A2AErrors: 2, A2AHops: 1}
	for name, w := range want {
		v, err := newA2AMetric(name).Compute(context.Background(), rc, ev)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := v.Float(); !ok || got != w || v.Source != schemas.EvidenceSourceBifrostLog {
			t.Errorf("%s = %+v, want %v from the bifrost log", name, v, w)
		}
	}
	plain := &schemas.RunContext{Agent: &schemas.AgentConfig{Adapter: "command"}}
	if v, _ := newA2AMetric(A2ATasks).Compute(context.Background(), plain, ev); v.State != schemas.ValueNotApplicable {
		t.Errorf("not applicable to other agents, got %+v", v)
	}
	if v, _ := newA2AMetric(A2AHops).Compute(context.Background(), rc, &schemas.Evidence{}); v.State != schemas.ValueUnknown {
		t.Errorf("unknown without a witness, got %+v", v)
	}
	ev.A2A.Complete, ev.A2A.Reason = false, "1 launcher operation(s) are not in the agent log"
	if v, _ := newA2AMetric(A2ATasks).Compute(context.Background(), rc, ev); v.State != schemas.ValueUnknown || v.Reason == "" {
		t.Errorf("unknown with the reason when the witness is incomplete, got %+v", v)
	}
}
