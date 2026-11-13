package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// withMetric sets metric on run i to v.
func withMetric(runs []schemas.SealedRun, metric string, values ...schemas.Value) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i := range out {
		if i >= len(values) {
			break
		}
		ms := map[string]schemas.Value{}
		for k, v := range out[i].Metrics {
			ms[k] = v
		}
		ms[metric] = values[i]
		out[i].Metrics = ms
	}
	return out
}

func usd(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceGateway) }

func TestCostPerVerifiedSuccess(t *testing.T) {
	clean := schemas.ConditionClean
	fails := passes(clean, ArmBaseline, 3, 0, 0, 0, 0, 0, 0, 0, 0)
	twoOfFour := passes(clean, ArmBaseline, 1, 1, 1, 0, 0)
	tests := []struct {
		name   string
		runs   []schemas.SealedRun
		state  schemas.ValueState
		v      float64 // checked when observed
		reason string  // checked when not empty
		bounds *[2]float64
	}{
		{"zero successes is undefined", withMetric(fails, MetricCostUSD, usd(0.1), usd(0.2)),
			schemas.ValueNotApplicable, 0, "undefined (0 of 24)", nil},
		{"cost of failures counts", withMetric(twoOfFour, MetricCostUSD, usd(1), usd(2), usd(3), usd(4)),
			schemas.ValueObserved, 5, "", nil},
		{"unbounded unknown cost", withMetric(twoOfFour, MetricCostUSD, usd(1), usd(2), usd(3), schemas.Unknown("usage incomplete", schemas.EvidenceGateway)),
			schemas.ValueUnknown, 0, "cost unknown in 1 of 4 runs, verified success unknown in 0", nil},
		{"bounded unknown cost", withMetric(twoOfFour, MetricCostUSD, usd(1), usd(2), usd(3), schemas.UnknownWithin("usage incomplete", schemas.EvidenceGateway, 0, 2)),
			schemas.ValueUnknown, 0, "", &[2]float64{3, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := costPerVerifiedSuccess(tt.runs)
			if got.State != tt.state {
				t.Fatalf("state %s, want %s (%+v)", got.State, tt.state, got)
			}
			if x, ok := got.Float(); tt.state == schemas.ValueObserved && (!ok || !near(x, tt.v, 1e-12)) {
				t.Fatalf("value %v, want %v", x, tt.v)
			}
			if tt.reason != "" && got.Reason != tt.reason {
				t.Fatalf("reason %q, want %q", got.Reason, tt.reason)
			}
			if tt.bounds != nil && (got.Bounds == nil || !near(got.Bounds[0], tt.bounds[0], 1e-12) || !near(got.Bounds[1], tt.bounds[1], 1e-12)) {
				t.Fatalf("bounds %v, want %v", got.Bounds, tt.bounds)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	clean := schemas.ConditionClean
	four := passes(clean, ArmBaseline, 1, 1, 1, 0, 0)
	count := func(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceGateway) }
	unknown := schemas.Unknown("log incomplete", schemas.EvidenceGateway)
	na := schemas.NotApplicable()
	tests := []struct {
		name   string
		runs   []schemas.SealedRun
		metric string
		reduce func([]schemas.SealedRun) schemas.Value
		state  schemas.ValueState
		v      float64
		reason string
	}{
		{"false completion mean over observed", withMetric(four, MetricFalseCompletion, count(1), count(0), count(0), unknown),
			MetricFalseCompletion, meanOf(MetricFalseCompletion), schemas.ValueObserved, 1.0 / 3, "3 observed of 4 runs"},
		{"claim precision skips runs without a claim", withMetric(four, MetricClaimPrecision, count(1), count(0), na, na),
			MetricClaimPrecision, meanOf(MetricClaimPrecision), schemas.ValueObserved, 0.5, ""},
		{"claim precision with no claim at all", withMetric(four, MetricClaimPrecision, na, na, na, na),
			MetricClaimPrecision, meanOf(MetricClaimPrecision), schemas.ValueNotApplicable, 0, "does not apply to any run (0 of 4)"},
		{"median steps", withMetric(four, MetricSteps, count(4), count(10), count(6), count(8)),
			MetricSteps, medianOf(MetricSteps), schemas.ValueObserved, 7, ""},
		{"median tokens never recorded", four,
			MetricTokens, medianOf(MetricTokens), schemas.ValueUnknown, 0, "not observed in any run (0 of 4)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.reduce(tt.runs)
			if got.State != tt.state {
				t.Fatalf("state %s, want %s (%+v)", got.State, tt.state, got)
			}
			if x, ok := got.Float(); tt.state == schemas.ValueObserved && (!ok || !near(x, tt.v, 1e-12)) {
				t.Fatalf("value %v, want %v", x, tt.v)
			}
			if got.Reason != tt.reason {
				t.Fatalf("reason %q, want %q", got.Reason, tt.reason)
			}
		})
	}
}

func TestMeters(t *testing.T) {
	clean := schemas.ConditionClean
	runs := concat(
		withVeto(passes(clean, ArmBaseline, 1, 1, 1, 0, 0), taskID(0), ArmBaseline),
		passes(clean, ArmCandidate, 1, 1, 1, 1, 1),
	)
	ms := meters(newRunIndex(runs))
	byName := map[string]schemas.Meter{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	tests := []struct {
		name  string
		meter string
		arm   string
		state schemas.ValueState
		v     float64
	}{
		{"veto gates per run in A", MeterVetoGatesPerRun, ArmBaseline, schemas.ValueObserved, 0.25},
		{"veto gates per run in B", MeterVetoGatesPerRun, ArmCandidate, schemas.ValueObserved, 0},
		{"cost unknown without a cost metric", MeterCostPerVerifiedSuccess, ArmCandidate, schemas.ValueUnknown, 0},
		{"steps unknown without a steps metric", MeterStepsMedian, ArmBaseline, schemas.ValueUnknown, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := byName[tt.meter]
			if !ok {
				t.Fatalf("meter %s missing", tt.meter)
			}
			got := m.PerArm[tt.arm]
			if got.State != tt.state {
				t.Fatalf("state %s, want %s", got.State, tt.state)
			}
			if x, ok := got.Float(); tt.state == schemas.ValueObserved && (!ok || !near(x, tt.v, 1e-12)) {
				t.Fatalf("value %v, want %v", x, tt.v)
			}
		})
	}
	if len(ms) != 8 {
		t.Fatalf("%d meters, want 8", len(ms))
	}
}

func TestArmMetrics(t *testing.T) {
	clean := schemas.ConditionClean
	count := func(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceGateway) }
	runs := concat(
		withMetric(passes(clean, ArmBaseline, 1, 1, 0, 1), MetricSteps, count(3), count(5), count(9)),
		passes(clean, ArmCandidate, 1, 1, 1, 1),
		passes(condStale, ArmCandidate, 1, 0, 0, 0),
	)
	got := armMetrics(newRunIndex(runs), clean)
	tests := []struct {
		name   string
		arm    string
		metric string
		v      float64
	}{
		{"A steps median", ArmBaseline, MetricSteps, 5},
		{"A success median", ArmBaseline, MetricVerifiedSuccess, 1},
		{"B success median on clean only", ArmCandidate, MetricVerifiedSuccess, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, ok := got[tt.arm][tt.metric].Float()
			if !ok || x != tt.v {
				t.Fatalf("got %+v, want %v", got[tt.arm][tt.metric], tt.v)
			}
		})
	}
}
