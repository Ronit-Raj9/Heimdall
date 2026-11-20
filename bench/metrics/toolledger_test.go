package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// toolRun is a run context whose task has one mcp service, proxied by
// default.
func toolRun() *schemas.RunContext {
	return &schemas.RunContext{Task: &schemas.Task{Services: []schemas.ServiceSpec{{ID: "fs", Role: schemas.ServiceRoleMCP}}}}
}

func ledgerCall(seq int, server, tool string, isError bool) schemas.ToolCall {
	return schemas.ToolCall{Seq: seq, Server: server, Tool: tool, Protocol: "mcp", IsError: isError}
}

func TestToolLedgerMetrics(t *testing.T) {
	noProxy := &schemas.RunContext{Task: &schemas.Task{Services: []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleData}}}}
	undelivered := ledgerCall(2, "fs", "read", false)
	undelivered.Undelivered = true
	tests := []struct {
		name  string
		rc    *schemas.RunContext
		calls []schemas.ToolCall // nil means the ledger is missing
		want  map[string]schemas.Value
	}{
		{
			name: "no proxied service: not applicable",
			rc:   noProxy, calls: []schemas.ToolCall{},
			want: map[string]schemas.Value{ToolCallsProxied: schemas.NotApplicable(), ToolErrorRate: schemas.NotApplicable(), ToolRecovery: schemas.NotApplicable(), DistinctTools: schemas.NotApplicable()},
		},
		{
			name: "nil run context: not applicable",
			rc:   nil, calls: []schemas.ToolCall{},
			want: map[string]schemas.Value{ToolCallsProxied: schemas.NotApplicable()},
		},
		{
			name: "ledger missing: unknown, never zero",
			rc:   toolRun(), calls: nil,
			want: map[string]schemas.Value{
				ToolCallsProxied: schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
				ToolErrorRate:    schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
				ToolRecovery:     schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
				DistinctTools:    schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
			},
		},
		{
			name: "empty ledger: zero calls observed, rates not applicable",
			rc:   toolRun(), calls: []schemas.ToolCall{},
			want: map[string]schemas.Value{
				ToolCallsProxied: schemas.Observed(0, schemas.EvidenceBoundary),
				ToolErrorRate:    schemas.NotApplicable(),
				ToolRecovery:     schemas.NotApplicable(),
				DistinctTools:    schemas.Observed(0, schemas.EvidenceBoundary),
			},
		},
		{
			name: "listing skipped, error then success on the same server recovers",
			rc:   toolRun(),
			calls: []schemas.ToolCall{
				ledgerCall(1, "fs", "tools/list", false),
				ledgerCall(2, "fs", "write", true),
				ledgerCall(3, "fs", "write", false),
				ledgerCall(4, "fs", "read", false),
			},
			want: map[string]schemas.Value{
				ToolCallsProxied: schemas.Observed(3, schemas.EvidenceBoundary),
				ToolErrorRate:    schemas.Observed(1.0/3.0, schemas.EvidenceBoundary),
				ToolRecovery:     schemas.Observed(1, schemas.EvidenceBoundary),
				DistinctTools:    schemas.Observed(2, schemas.EvidenceBoundary),
			},
		},
		{
			name: "success on another server is not recovery; undelivered counts as failed",
			rc:   toolRun(),
			calls: []schemas.ToolCall{
				ledgerCall(1, "fs", "write", true),
				undelivered,
				ledgerCall(3, "db", "query", false),
			},
			want: map[string]schemas.Value{
				ToolErrorRate: schemas.Observed(2.0/3.0, schemas.EvidenceBoundary),
				ToolRecovery:  schemas.Observed(0, schemas.EvidenceBoundary),
				DistinctTools: schemas.Observed(3, schemas.EvidenceBoundary),
			},
		},
		{
			name: "all clean: no failure to recover from",
			rc:   toolRun(),
			calls: []schemas.ToolCall{
				ledgerCall(1, "fs", "read", false),
				ledgerCall(2, "fs", "read", false),
			},
			want: map[string]schemas.Value{
				ToolErrorRate: schemas.Observed(0, schemas.EvidenceBoundary),
				ToolRecovery:  schemas.NotApplicable(),
				DistinctTools: schemas.Observed(1, schemas.EvidenceBoundary),
			},
		},
	}
	metrics := map[string]schemas.Metric{
		ToolCallsProxied: newToolCallsProxiedMetric(),
		ToolErrorRate:    newToolErrorRateMetric(),
		ToolRecovery:     newToolRecoveryMetric(),
		DistinctTools:    newDistinctToolsMetric(),
	}
	for _, tt := range tests {
		for name, want := range tt.want {
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				got, err := metrics[name].Compute(context.Background(), tt.rc, &schemas.Evidence{ToolCalls: tt.calls})
				if err != nil {
					t.Fatal(err)
				}
				if got.State != want.State || got.Reason != want.Reason || got.Evidence != want.Evidence {
					t.Fatalf("got %+v, want %+v", got, want)
				}
				gv, gok := got.Float()
				wv, wok := want.Float()
				if gok != wok || (gok && (gv-wv > 1e-9 || wv-gv > 1e-9)) {
					t.Fatalf("got %v, want %v", gv, wv)
				}
			})
		}
	}
}

func TestToolLedgerMetricsNilEvidence(t *testing.T) {
	got, err := newToolCallsProxiedMetric().Compute(context.Background(), toolRun(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != schemas.ValueUnknown {
		t.Fatalf("nil evidence gave %+v, want unknown", got)
	}
}

func TestHasProxiedService(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		svc  schemas.ServiceSpec
		want bool
	}{
		{name: "mcp defaults to proxied", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleMCP}, want: true},
		{name: "tool defaults to proxied", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleTool}, want: true},
		{name: "data is not", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleData}},
		{name: "explicit proxy on data", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleData, Proxy: &yes}, want: true},
		{name: "explicit no proxy on mcp", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleMCP, Proxy: &no}},
		{name: "hidden never", svc: schemas.ServiceSpec{ID: "a", Role: schemas.ServiceRoleMCP, Hidden: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &schemas.RunContext{Suite: &schemas.Suite{Services: []schemas.ServiceSpec{tt.svc}}}
			if got := hasProxiedService(rc); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A torn or capped ledger is a prefix: exact counts must not read a measured
// zero (audit 1-C-04).
func TestToolLedgerMetricsIncompleteLedger(t *testing.T) {
	ev := &schemas.Evidence{ToolCalls: []schemas.ToolCall{}, ToolLedgerProblem: "tool ledger has 1 malformed lines"}
	for _, m := range []schemas.Metric{newToolCallsProxiedMetric(), newToolErrorRateMetric(), newDistinctToolsMetric()} {
		got, err := m.Compute(context.Background(), toolRun(), ev)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != schemas.ValueUnknown {
			t.Fatalf("%s on a torn ledger gave %+v, want unknown", m.GetName(), got)
		}
	}
}
