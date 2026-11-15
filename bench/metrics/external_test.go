package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// externalRun is a run context whose task declares one external API.
func externalRun() *schemas.RunContext {
	return &schemas.RunContext{Task: &schemas.Task{External: []schemas.ExternalAPI{{ID: "acme", BaseURL: "https://api.acme.test"}}}}
}

func externalCall(seq int, isError, unmatched bool) schemas.ToolCall {
	return schemas.ToolCall{Seq: seq, Server: "acme", Tool: "GET /rates", Protocol: protocolExternal, IsError: isError, Unmatched: unmatched, Replayed: !unmatched}
}

func TestExternalMetrics(t *testing.T) {
	suiteLevel := &schemas.RunContext{Suite: &schemas.Suite{External: []schemas.ExternalAPI{{ID: "acme"}}}, Task: &schemas.Task{}}
	tests := []struct {
		name  string
		rc    *schemas.RunContext
		calls []schemas.ToolCall // nil means the ledger is missing
		want  map[string]schemas.Value
	}{
		{
			name: "no external api: not applicable",
			rc:   toolRun(), calls: []schemas.ToolCall{externalCall(1, false, false)},
			want: map[string]schemas.Value{ExternalCalls: schemas.NotApplicable(), ExternalUnmatched: schemas.NotApplicable(), ExternalErrorRate: schemas.NotApplicable()},
		},
		{
			name: "nil run context: not applicable",
			rc:   nil, calls: []schemas.ToolCall{},
			want: map[string]schemas.Value{ExternalCalls: schemas.NotApplicable()},
		},
		{
			name: "ledger missing: unknown, never zero",
			rc:   externalRun(), calls: nil,
			want: map[string]schemas.Value{
				ExternalCalls:     schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
				ExternalUnmatched: schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
				ExternalErrorRate: schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary),
			},
		},
		{
			name: "empty ledger: zero calls, rate not applicable",
			rc:   externalRun(), calls: []schemas.ToolCall{},
			want: map[string]schemas.Value{
				ExternalCalls:     schemas.Observed(0, schemas.EvidenceBoundary),
				ExternalUnmatched: schemas.Observed(0, schemas.EvidenceBoundary),
				ExternalErrorRate: schemas.NotApplicable(),
			},
		},
		{
			name: "tool rows skipped, unmatched counted, errors over external rows",
			rc:   suiteLevel,
			calls: []schemas.ToolCall{
				ledgerCall(1, "fs", "read", true),
				externalCall(2, false, false),
				externalCall(3, true, true),
				externalCall(4, true, false),
				externalCall(5, false, false),
			},
			want: map[string]schemas.Value{
				ExternalCalls:     schemas.Observed(4, schemas.EvidenceBoundary),
				ExternalUnmatched: schemas.Observed(1, schemas.EvidenceBoundary),
				ExternalErrorRate: schemas.Observed(0.5, schemas.EvidenceBoundary),
			},
		},
	}
	metrics := map[string]schemas.Metric{
		ExternalCalls:     newExternalCallsMetric(),
		ExternalUnmatched: newExternalUnmatchedMetric(),
		ExternalErrorRate: newExternalErrorRateMetric(),
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

func TestExternalMetricsDescriptors(t *testing.T) {
	for _, m := range []schemas.Metric{newExternalCallsMetric(), newExternalUnmatchedMetric(), newExternalErrorRateMetric()} {
		d := m.Descriptor()
		if d.Name != m.GetName() || d.HigherIsBetter || d.MinEvidence != schemas.EvidenceBoundary {
			t.Errorf("%s descriptor %+v: want lower is better, boundary evidence, name matching", m.GetName(), d)
		}
	}
}
