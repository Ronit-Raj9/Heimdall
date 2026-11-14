package verdict

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCellMean(t *testing.T) {
	var c cell
	c.add(schemas.Observed(1, schemas.EvidenceOutcome), true)
	c.add(schemas.Observed(0, schemas.EvidenceOutcome), true)
	c.add(schemas.Unknown("no grade", schemas.EvidenceOutcome), true)
	c.add(schemas.UnknownWithin("partial", schemas.EvidenceOutcome, 0.25, 0.75), true)
	tests := []struct {
		name string
		f    fill
		want float64
	}{
		{"observed only", fillObserved, 0.5},
		{"unknowns low", fillLow, (1 + 0 + 0 + 0.25) / 4},
		{"unknowns high", fillHigh, (1 + 0 + 1 + 0.75) / 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.mean(tt.f)
			if !ok || !near(got, tt.want, 1e-12) {
				t.Fatalf("got %v %v, want %v", got, ok, tt.want)
			}
		})
	}
	if _, ok := (cell{n: 2}).mean(fillObserved); ok {
		t.Fatal("an all-unknown cell has an observed mean")
	}
	var bad cell
	bad.add(schemas.Observed(math.NaN(), schemas.EvidenceOutcome), true)
	if bad.nObs != 0 || bad.unkHi != 1 {
		t.Fatalf("a NaN value counted as observed: %+v", bad)
	}
}

func TestPairedEffectBounds(t *testing.T) {
	clean := schemas.ConditionClean
	idx := newRunIndex(concat(
		pattern(clean, ArmBaseline, "0u", "11"),
		pattern(clean, ArmCandidate, "1u", "11"),
		pattern(clean, ArmCandidate, "11", "11", "11")[4:], // t03 in B only: never paired
	))
	e := pairedEffect(clean, MetricVerifiedSuccess, ArmBaseline, ArmCandidate,
		idx.cells(clean, ArmBaseline, MetricVerifiedSuccess), idx.cells(clean, ArmCandidate, MetricVerifiedSuccess), false)
	tests := []struct {
		name string
		e    *schemas.Effect
		d    []float64
	}{
		{"observed", e, []float64{1, 0}},
		{"lower bound: unknown A at 1, unknown B at 0", e.LowerBound, []float64{0.5 - 0.5, 0}},
		{"upper bound: unknown A at 0, unknown B at 1", e.UpperBound, []float64{1, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.e.PerTask) != len(tt.d) {
				t.Fatalf("per task %+v, want %d tasks", tt.e.PerTask, len(tt.d))
			}
			for i, td := range tt.e.PerTask {
				if !near(td.D, tt.d[i], 1e-12) {
					t.Fatalf("task %s d = %v, want %v", td.Task, td.D, tt.d[i])
				}
			}
			if tt.e.PValue != nil || !tt.e.Descriptive {
				t.Fatal("a descriptive effect carries a p-value")
			}
		})
	}
	if td := e.PerTask[0]; td.NA != 2 || td.UnknownA != 1 || td.NB != 2 || td.UnknownB != 1 {
		t.Fatalf("task counts %+v", td)
	}
}

func TestCellUnboundedOutsideUnitInterval(t *testing.T) {
	tests := []struct {
		name      string
		v         schemas.Value
		rate      bool
		unbounded int
	}{
		{"rate unknown lies in [0, 1]", schemas.Unknown("no grade", schemas.EvidenceOutcome), true, 0},
		{"cost unknown is unbounded", schemas.Unknown("no ledger", schemas.EvidenceGateway), false, 1},
		{"cost unknown with its own bounds", schemas.UnknownWithin("partial", schemas.EvidenceGateway, 0.1, 0.4), false, 0},
		{"observed cost", schemas.Observed(2.5, schemas.EvidenceGateway), false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c cell
			c.add(tt.v, tt.rate)
			if c.unbounded != tt.unbounded {
				t.Fatalf("unbounded %d, want %d (%+v)", c.unbounded, tt.unbounded, c)
			}
		})
	}
}

// cellsOf builds task -> cell from per-task values of one metric.
func cellsOf(metric string, perTask map[string][]schemas.Value) map[string]cell {
	out := map[string]cell{}
	for task, vals := range perTask {
		var c cell
		for _, v := range vals {
			c.add(v, RateMetric(metric))
		}
		out[task] = c
	}
	return out
}

func TestPairedEffectBoundsFollowDirection(t *testing.T) {
	obs := func(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceOutcome) }
	unk := schemas.Unknown("unknown", schemas.EvidenceOutcome)
	clean := schemas.ConditionClean
	tests := []struct {
		name    string
		metric  string
		a, b    map[string][]schemas.Value
		bounded bool
		against float64 // d_bar with unknowns set against B
		favour  float64 // d_bar with unknowns set in favour of B
	}{
		{"higher is better: against B is A high, B low", MetricVerifiedSuccess,
			map[string][]schemas.Value{"t01": {obs(0), unk}}, map[string][]schemas.Value{"t01": {obs(1), unk}}, true, 0, 1},
		{"lower is better: against B is A low, B high", MetricFalseCompletion,
			map[string][]schemas.Value{"t01": {obs(0), unk}}, map[string][]schemas.Value{"t01": {obs(1), unk}}, true, 1, 0},
		{"cost with every value observed: bounds are the estimate", MetricCostUSD,
			map[string][]schemas.Value{"t01": {obs(2), obs(4)}}, map[string][]schemas.Value{"t01": {obs(1), obs(1)}}, true, -2, -2},
		{"cost with an unbounded unknown: no bounds", MetricCostUSD,
			map[string][]schemas.Value{"t01": {obs(2), unk}}, map[string][]schemas.Value{"t01": {obs(1), obs(1)}}, false, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := pairedEffect(clean, tt.metric, ArmBaseline, ArmCandidate, cellsOf(tt.metric, tt.a), cellsOf(tt.metric, tt.b), true)
			if !tt.bounded {
				if e.LowerBound != nil || e.UpperBound != nil {
					t.Fatalf("bounds %+v / %+v, want none", e.LowerBound, e.UpperBound)
				}
				if pairedTasks(e) != 1 {
					t.Fatalf("paired tasks %d, want 1", pairedTasks(e))
				}
				return
			}
			if e.LowerBound == nil || e.UpperBound == nil {
				t.Fatal("bounds missing")
			}
			if !near(e.LowerBound.DBar, tt.against, 1e-12) || !near(e.UpperBound.DBar, tt.favour, 1e-12) {
				t.Fatalf("d_bar against B %v, for B %v; want %v, %v", e.LowerBound.DBar, e.UpperBound.DBar, tt.against, tt.favour)
			}
		})
	}
}

func TestFillEffectStats(t *testing.T) {
	tests := []struct {
		name        string
		d           []float64
		inferential bool
		nonzero     int
		p           float64 // checked when inferential
		interval    bool
	}{
		{"empty inferential has p 1", nil, true, 0, 1, false},
		{"one task has no interval", []float64{0.5}, true, 1, 1, false},
		{"ties stay in n", []float64{0, 0, 1, 1}, true, 2, 0.5, true},
		{"descriptive has no p", []float64{1, -1, 0}, false, 2, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &schemas.Effect{}
			fillEffectStats(e, tt.d, tt.inferential)
			if e.NTasks != len(tt.d) || e.NNonzero != tt.nonzero {
				t.Fatalf("n %d nonzero %d", e.NTasks, e.NNonzero)
			}
			if tt.inferential != (e.PValue != nil) {
				t.Fatalf("p-value presence %v", e.PValue)
			}
			if tt.inferential && !near(*e.PValue, tt.p, 1e-12) {
				t.Fatalf("p = %v, want %v", *e.PValue, tt.p)
			}
			if tt.interval != (e.Interval != nil) {
				t.Fatalf("interval %v", e.Interval)
			}
		})
	}
}

func TestMetricValueMissingIsUnknown(t *testing.T) {
	r := sealedRun("t01", ArmBaseline, schemas.ConditionClean, 1, schemas.Observed(1, schemas.EvidenceOutcome))
	tests := []struct {
		metric   string
		observed bool
	}{
		{MetricVerifiedSuccess, true},
		{MetricSteps, false},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			v := metricValue(r, tt.metric)
			if v.IsObserved() != tt.observed {
				t.Fatalf("got %+v", v)
			}
			if !tt.observed && v.State != schemas.ValueUnknown {
				t.Fatalf("missing metric state %s, want unknown", v.State)
			}
		})
	}
}
