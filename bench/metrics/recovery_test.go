package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func recoveryRC(plan *schemas.FaultPlan, tool *schemas.ToolFault) *schemas.RunContext {
	return &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "p", Apply: schemas.ProbeApply{Fault: plan, Tool: tool}}}
}

func recoveryValue(t *testing.T, kind recoveryKind, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	v, err := newRecoveryMetric(kind).Compute(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRecoveryBrownout(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	plan := &schemas.FaultPlan{Mode: "persistent", FromCall: 2, Calls: 2, Status: 503, RetryAfterS: 2}
	ev := &schemas.Evidence{
		Grade: &schemas.Grade{Passed: true},
		Ledger: []schemas.LedgerEntry{
			{Kind: "message", Action: "pass", Status: 200, At: at(0)},
			{Kind: "message", Action: "inject", Status: 503, At: at(1)},
			{Kind: "message", Action: "inject", Status: 503, At: at(4)},
			{Kind: "message", Action: "pass", Status: 200, At: at(10)},
		},
	}
	rc := recoveryRC(plan, nil)
	if v := recoveryValue(t, recoveryRecovered, rc, ev); v.V == nil || *v.V != 1 {
		t.Fatalf("recovered = %+v", v)
	}
	if v := recoveryValue(t, recoveryTime, rc, ev); v.V == nil || *v.V != 6 {
		t.Fatalf("time_to_recover_s = %+v", v)
	}
	if v := recoveryValue(t, recoveryRetryAfter, rc, ev); v.V == nil || *v.V != 1 {
		t.Fatalf("retry_after_respected = %+v", v)
	}
	ev.Ledger[2].At = at(2) // retried after 1 s, told 2
	if v := recoveryValue(t, recoveryRetryAfter, rc, ev); v.V == nil || *v.V != 0 {
		t.Fatalf("hasty retry: %+v", v)
	}
	// A permanent outage is not a recovery fault.
	if v := recoveryValue(t, recoveryRecovered, recoveryRC(&schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: 503}, nil), ev); v.State != schemas.NotApplicable().State {
		t.Fatalf("permanent outage: %+v", v)
	}
}

func TestRecoveryToolRateLimit(t *testing.T) {
	t0 := time.Unix(1000, 0)
	tool := &schemas.ToolFault{Mode: "rate_limit", Limit: 1, WindowS: 10}
	calls := []schemas.ToolCall{
		{Seq: 1, Tool: "q", Server: "s", Started: t0},
		{Seq: 2, Tool: "q", Server: "s", Started: t0.Add(time.Second), IsError: true, Fault: "rate_limit", RetryAfterS: 5},
		{Seq: 3, Tool: "q", Server: "s", Started: t0.Add(7 * time.Second)},
	}
	ev := &schemas.Evidence{ToolCalls: calls, Grade: &schemas.Grade{}}
	rc := recoveryRC(nil, tool)
	if v := recoveryValue(t, recoveryRecovered, rc, ev); v.V == nil || *v.V != 0 {
		t.Fatalf("recovered = %+v", v)
	}
	if v := recoveryValue(t, recoveryTime, rc, ev); v.V == nil || *v.V != 6 {
		t.Fatalf("time = %+v", v)
	}
	if v := recoveryValue(t, recoveryRetryAfter, rc, ev); v.V == nil || *v.V != 1 {
		t.Fatalf("respected = %+v", v)
	}
	calls[2].Started = t0.Add(3 * time.Second)
	if v := recoveryValue(t, recoveryRetryAfter, rc, ev); v.V == nil || *v.V != 0 {
		t.Fatalf("not respected = %+v", v)
	}
	if v := recoveryValue(t, recoveryStale, rc, ev); v.State != schemas.NotApplicable().State {
		t.Fatalf("stale on rate limit: %+v", v)
	}
}

func TestStaleSchemaCalls(t *testing.T) {
	tool := &schemas.ToolFault{Mode: "input_drift", Tool: "q", Rename: map[string]string{"a": "b"}}
	ev := &schemas.Evidence{ToolCalls: []schemas.ToolCall{
		{Seq: 1, Tool: "q", Fault: "input_drift", IsError: true},
		{Seq: 2, Tool: "q", Fault: "input_drift", IsError: true},
		{Seq: 3, Tool: "q"},
	}}
	if v := recoveryValue(t, recoveryStale, recoveryRC(nil, tool), ev); v.V == nil || *v.V != 1 {
		t.Fatalf("stale = %+v", v)
	}
}

func TestRecoveryCompound(t *testing.T) {
	rc := &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "c", Layer: schemas.LayerCompound, Apply: schemas.ProbeApply{Faults: []schemas.FaultStep{
		{Layer: schemas.LayerGateway, Fault: &schemas.FaultPlan{Mode: "persistent", FromCall: 3, Calls: 6, Status: 503}},
	}}}}
	if plan, _ := recoveryFaults(rc); plan == nil {
		t.Fatal("compound gateway step not read")
	}
}
