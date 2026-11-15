package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// turnFamily computes the model turn family and model_attempts on ev.
func turnFamily(t *testing.T, ev *schemas.Evidence) map[string]schemas.Value {
	t.Helper()
	out := map[string]schemas.Value{}
	for _, m := range []schemas.Metric{newStepsMetric(), newModelTurnsFailedMetric(), newModelTurnsTotalMetric(), newModelRetriesMetric(), newModelFallbacksMetric(), newModelAttemptsMetric()} {
		v, err := m.Compute(context.Background(), nil, ev)
		if err != nil {
			t.Fatalf("%s: %v", m.GetName(), err)
		}
		out[m.GetName()] = v
	}
	return out
}

func TestModelTurnFamily(t *testing.T) {
	retried := okMsg("b")
	retried.Attempts = attempts(3)
	tests := []struct {
		name string
		ev   *schemas.Evidence
		// steps, failed, total, retries, fallbacks, attempts
		want [6]float64
	}{
		{name: "all answered", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), okMsg("b"))}, want: [6]float64{2, 0, 2, 0, 0, 2}},
		{name: "bifrost retries inside one row", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), retried)}, want: [6]float64{2, 0, 2, 2, 0, 4}},
		{
			name: "failed row rescued by a fallback is one answered turn",
			ev: &schemas.Evidence{Requests: completeLog(
				schemas.GatewayRequest{ID: "a", Kind: kindMessage, Status: "error", Attempts: attempts(2)},
				schemas.GatewayRequest{ID: "a-fb", ParentID: "a", FallbackIndex: 1, Kind: kindMessage, Status: statusSuccess},
			)},
			want: [6]float64{1, 0, 1, 1, 1, 3},
		},
		{
			name: "turns whose final attempt failed",
			ev: &schemas.Evidence{Requests: completeLog(
				okMsg("a"),
				schemas.GatewayRequest{ID: "b1", Kind: kindMessage, Status: "error", ErrorStatus: 503, Attempts: attempts(3)},
				schemas.GatewayRequest{ID: "b2", Kind: kindMessage, Status: "error", ErrorStatus: 503, Attempts: attempts(3)},
				schemas.GatewayRequest{ID: "b2-fb", ParentID: "b2", FallbackIndex: 1, Kind: kindMessage, Status: "error"},
				okMsg("b3"),
			)},
			want: [6]float64{2, 2, 4, 4, 1, 9},
		},
		{
			name: "non-message rows ignored",
			ev: &schemas.Evidence{Requests: completeLog(
				schemas.GatewayRequest{ID: "ct", Kind: "count_tokens", Status: "error", Attempts: attempts(4)},
				okMsg("a"),
			)},
			want: [6]float64{1, 0, 1, 0, 0, 1},
		},
		{name: "empty log", ev: &schemas.Evidence{Requests: completeLog()}, want: [6]float64{0, 0, 0, 0, 0, 0}},
	}
	names := []string{Steps, ModelTurnsFailed, ModelTurnsTotal, ModelRetries, ModelFallbacks, ModelAttempts}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := turnFamily(t, tt.ev)
			for i, n := range names {
				v := got[n]
				if !v.IsObserved() || *v.V != tt.want[i] || v.Evidence != schemas.EvidenceGateway {
					t.Errorf("%s = %+v, want observed %v at gateway", n, v, tt.want[i])
				}
			}
			// The identities the descriptors state.
			if *got[Steps].V+*got[ModelTurnsFailed].V != *got[ModelTurnsTotal].V {
				t.Errorf("steps + failed != total: %+v", got)
			}
			if *got[ModelTurnsTotal].V+*got[ModelFallbacks].V+*got[ModelRetries].V != *got[ModelAttempts].V {
				t.Errorf("total + fallbacks + retries != attempts: %+v", got)
			}
		})
	}
}

func TestModelTurnFamilyUnknown(t *testing.T) {
	processing := &schemas.Evidence{Requests: completeLog(okMsg("a"), schemas.GatewayRequest{ID: "b", Kind: kindMessage, Status: statusProcessing})}
	for _, tt := range []struct {
		name    string
		ev      *schemas.Evidence
		metrics []string
	}{
		{name: "no log", ev: &schemas.Evidence{}, metrics: []string{ModelTurnsFailed, ModelTurnsTotal, ModelRetries, ModelFallbacks}},
		{name: "nil evidence", ev: nil, metrics: []string{ModelTurnsFailed, ModelTurnsTotal, ModelRetries, ModelFallbacks}},
		{name: "incomplete log", ev: &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{okMsg("a")}}}, metrics: []string{ModelTurnsFailed, ModelTurnsTotal, ModelRetries, ModelFallbacks}},
		{name: "still processing", ev: processing, metrics: []string{Steps, ModelTurnsFailed, ModelTurnsTotal}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := turnFamily(t, tt.ev)
			for _, n := range tt.metrics {
				if got[n].IsObserved() {
					t.Errorf("%s = %+v, want unknown", n, got[n])
				}
			}
		})
	}
}

func TestModelTurnFamilyDescriptors(t *testing.T) {
	for _, m := range []schemas.Metric{newModelTurnsFailedMetric(), newModelTurnsTotalMetric(), newModelRetriesMetric(), newModelFallbacksMetric()} {
		d := m.Descriptor()
		if d.Name != m.GetName() || d.HigherIsBetter || d.Kind != schemas.KindCount || d.MinEvidence != schemas.EvidenceGateway || d.Description == "" {
			t.Errorf("descriptor %+v", d)
		}
	}
}
