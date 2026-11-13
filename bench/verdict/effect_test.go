package verdict

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCellMean(t *testing.T) {
	var c cell
	c.add(schemas.Observed(1, schemas.EvidenceOutcome))
	c.add(schemas.Observed(0, schemas.EvidenceOutcome))
	c.add(schemas.Unknown("no grade", schemas.EvidenceOutcome))
	c.add(schemas.UnknownWithin("partial", schemas.EvidenceOutcome, 0.25, 0.75))
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
	bad.add(schemas.Observed(math.NaN(), schemas.EvidenceOutcome))
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
