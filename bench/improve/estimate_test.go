package improve

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/stats"
)

// estSpec is a custom-preset spec (held-out curve off) with the counts of
// the A2.8 example: 2 rounds, practice 1 trial, gate 3, held-out 2, merge on,
// the null and retries controls.
func estSpec(k int) *Spec {
	s := &Spec{Preset: PresetCustom, Rounds: 2,
		Improver: ImproverSpec{Candidates: k, Merge: true, BudgetUSD: 0.6},
		Screen:   PhaseSpec{Trials: 1}, Commit: CommitSpec{Trials: 3}, Sealed: PhaseSpec{Trials: 2},
		Budget: BudgetSpec{TotalUSD: 1000}}
	s.Defaults()
	return s
}

var estSplits = SplitCounts{Practice: 24, Gate: 18, HeldOut: 18}

func TestEstimateDefaultIsSet(t *testing.T) {
	if DefaultEstimator == nil {
		t.Fatal("DefaultEstimator is nil")
	}
	if _, ok := DefaultEstimator.(formulaEstimator); !ok {
		t.Fatalf("DefaultEstimator is %T", DefaultEstimator)
	}
}

// TestEstimateRunCounts checks the run counts of A2.8 against hand-computed
// numbers. Common parts: baseline = n_d t_s = 24 x 1 = 24 (curve off);
// gate = 2 (n_a + h) t_c = 2 x 18 x 3 = 108 per round; final =
// (2 + 1 null) n_s t_z = 3 x 18 x 2 = 108; R = 2.
func TestEstimateRunCounts(t *testing.T) {
	cases := []struct {
		k                                  int
		practice, improver, total, perScrn int
	}{
		// K = 1: screen = n_d t_s = 24; improver = 1 + 1 (no merge with one idea) + 2 null = 4;
		// total = 24 + 2 (24 + 108) + 108 = 396.
		{k: 1, perScrn: 24, practice: 48, improver: 4, total: 396},
		// K = 2: 1 level, chunk 24: 2 x 24 = 48; improver = 2 + (2 - 1 merge) + 2 = 5;
		// total = 24 + 2 (48 + 108) + 108 = 444.
		{k: 2, perScrn: 48, practice: 96, improver: 5, total: 444},
		// K = 4: 2 levels, chunks 12 and 12: 4 x 12 + 2 x 12 = 72; improver = 4 + 3 + 2 = 9;
		// total = 24 + 2 (72 + 108) + 108 = 492 (the A2.8 example).
		{k: 4, perScrn: 72, practice: 144, improver: 9, total: 492},
		// K = 8: 3 levels, chunks 8, 8, 8: 8 x 8 + 4 x 8 + 2 x 8 = 112; improver = 8 + 7 + 2 = 17;
		// total = 24 + 2 (112 + 108) + 108 = 572.
		{k: 8, perScrn: 112, practice: 224, improver: 17, total: 572},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("K%d", c.k), func(t *testing.T) {
			if got := screenRuns(c.k, 24, 1); got != c.perScrn {
				t.Fatalf("screen(%d) = %d, want %d", c.k, got, c.perScrn)
			}
			e := formulaEstimator{}.Estimate(EstimateInput{Spec: estSpec(c.k), Pool: 60, Splits: estSplits, PerRunUSD: 0.03})
			want := EstimateRuns{Baseline: 24, Improver: c.improver, Practice: c.practice, Gate: 216, HeldOutCurve: 0, Final: 108, Total: c.total}
			if e.Runs != want {
				t.Fatalf("runs = %+v, want %+v", e.Runs, want)
			}
		})
	}
}

// TestEstimateCurveAndHoneypots: curve on adds n_s t_z = 36 to the baseline
// and to every round; 2 honeypots make the gate 2 x 20 x 3 = 120 per round.
// Total = (24 + 36) + 2 (72 + 120 + 36) + 108 = 624; curve runs 36 + 2 x 36 = 108.
func TestEstimateCurveAndHoneypots(t *testing.T) {
	s := estSpec(4)
	s.Sealed.Curve = true
	sp := estSplits
	sp.Impossible = 2
	e := formulaEstimator{}.Estimate(EstimateInput{Spec: s, Pool: 62, Splits: sp, PerRunUSD: 0.03})
	want := EstimateRuns{Baseline: 60, Improver: 9, Practice: 144, Gate: 240, HeldOutCurve: 108, Final: 108, Total: 624}
	if e.Runs != want {
		t.Fatalf("runs = %+v, want %+v", e.Runs, want)
	}
}

// TestEstimateMoney: c = 0.03, improver cap 0.6.
// runs 492 x 0.03 = 14.76; improver 9 x 0.36 = 3.24; retries loop
// 2 x (72 + 108) x 0.03 = 10.8 plus 7 x 0.36 = 2.52: expected 31.32,
// low 0.7 x 31.32 = 21.92. High uses the full cap: 14.76 + 9 x 0.6 + 10.8 +
// 7 x 0.6 = 35.16, x 1.5 = 52.74.
func TestEstimateMoney(t *testing.T) {
	e := formulaEstimator{}.Estimate(EstimateInput{Spec: estSpec(4), Pool: 60, Splits: estSplits, PerRunUSD: 0.03, RunSource: "forecast", PerRunMin: 0.5})
	want := EstimateUSD{Low: 21.92, Expected: 31.32, High: 52.74, Limit: 1000, PerRun: 0.03, PerRunSource: "forecast"}
	if e.USD != want {
		t.Fatalf("usd = %+v, want %+v", e.USD, want)
	}
	// minutes: 0.5 x 492 = 246; 0.7 x 246 = 172, 1.5 x 246 = 369.
	if e.Minutes != (EstimateMinutes{Low: 172, High: 369}) {
		t.Fatalf("minutes = %+v", e.Minutes)
	}
	if !e.OK || len(e.Refusals) != 0 {
		t.Fatalf("refused: %+v", e.Refusals)
	}
	// Unknown cost: 0.05 assumed.
	e = formulaEstimator{}.Estimate(EstimateInput{Spec: estSpec(4), Pool: 60, Splits: estSplits})
	if e.USD.PerRun != 0.05 || e.USD.PerRunSource != "assumed" || !slices.Contains(e.Assumed, "a run costs $0.05") {
		t.Fatalf("assumed cost: %+v %v", e.USD, e.Assumed)
	}
}

func TestEstimateOverLimit(t *testing.T) {
	s := estSpec(4)
	s.Budget.TotalUSD = 45
	e := formulaEstimator{}.Estimate(EstimateInput{Spec: s, Pool: 60, Splits: estSplits, PerRunUSD: 0.03})
	want := []EstimateNote{{Code: "over_limit", Message: "The estimate ($21.92 to $52.74) is above your spending limit of $45.00. Raise the limit or pick a smaller preset."}}
	if e.OK || !slices.Equal(e.Refusals, want) {
		t.Fatalf("refusals = %+v", e.Refusals)
	}
}

// TestEstimateCodingCoreRefuses: coding-core has 8 tasks; the default
// shares give 3 practice, 2 gate and 3 held-out tasks.
func TestEstimateCodingCoreRefuses(t *testing.T) {
	s := &Spec{Preset: PresetSmall}
	s.Defaults()
	tasks := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"}
	sp, err := Partition(tasks, s)
	if err != nil {
		t.Fatal(err)
	}
	in := EstimateInput{Spec: s, Pool: 8, Splits: SplitCounts{Practice: len(sp.Dev), Gate: len(sp.Audit), HeldOut: len(sp.Sealed)}, PerRunUSD: 0.01}
	e := formulaEstimator{}.Estimate(in)
	if e.OK {
		t.Fatal("estimate ok for 8 tasks")
	}
	want := []EstimateNote{
		{Code: "too_few_held_out", Message: "This benchmark gives 3 held-out tasks. With so few, no result could ever be shown to be better. Use a benchmark with at least 40 tasks, or add tasks."},
		{Code: "gate_never_passes", Message: "With 2 gate tasks no new version could ever be kept. Use at least 40 tasks, or fewer rounds."},
		{Code: "too_few_practice", Message: "The improver needs at least 4 practice tasks to learn from; this benchmark gives 3."},
	}
	if in.Splits != (SplitCounts{Practice: 3, Gate: 2, HeldOut: 3}) || !slices.Equal(e.Refusals, want) {
		t.Fatalf("splits %+v refusals %+v", in.Splits, e.Refusals)
	}
}

// TestEstimateDetectable: no history gives w = 0.5 x 0.5 x 0.5 = 0.125.
// Gate: n = 18, m = 3, a = 0.10 / 2; held out: n = 18, m = 2, a = 0.05 (custom preset: no reserve).
func TestEstimateDetectable(t *testing.T) {
	e := formulaEstimator{}.Estimate(EstimateInput{Spec: estSpec(4), Pool: 60, Splits: estSplits, PerRunUSD: 0.03})
	hand := func(n, m int, a float64) float64 {
		v := 100 * (stats.TQuantile(1-a, n-1) + stats.TQuantile(0.8, n-1)) * math.Sqrt(2*0.125/float64(m)) / math.Sqrt(float64(n))
		return math.Round(v*10) / 10
	}
	if g, c := hand(18, 3, 0.05), hand(18, 2, 0.05); e.Detectable.GatePoints != g || e.Detectable.HeldOutPoints != c {
		t.Fatalf("detectable = %+v, want gate %v held out %v", e.Detectable, g, c)
	}
	wantAssumed := []string{"pass rate 50%", "half of the variation is between tasks", "the gain is the same on every task", "an improver run spends 60% of its cap"}
	if !slices.Equal(e.Assumed, wantAssumed) {
		t.Fatalf("assumed = %v", e.Assumed)
	}
	codes := []string{}
	for _, w := range e.Warnings {
		codes = append(codes, w.Code)
	}
	// held out about 22 points (> 15), gate about 17 points (not above 20).
	if !slices.Equal(codes, []string{"weak_held_out", "assumed_noise"}) {
		t.Fatalf("warnings = %+v", e.Warnings)
	}
	if !math.IsInf(mdeFormula(1, 3, 0.05, 0.125), 1) {
		t.Fatal("MDE with one task is not +Inf")
	}
	// With history the noise items are not assumed and no noise warning shows.
	e = formulaEstimator{}.Estimate(EstimateInput{Spec: estSpec(4), Pool: 60, Splits: estSplits, PerRunUSD: 0.03, P0: 0.6, W: 0.1, HistoryRuns: 40})
	if slices.Contains(e.Assumed, "pass rate 50%") || slices.Contains(e.Assumed, "half of the variation is between tasks") {
		t.Fatalf("assumed with history = %v", e.Assumed)
	}
	for _, w := range e.Warnings {
		if w.Code == "assumed_noise" {
			t.Fatal("assumed_noise with history")
		}
	}
}

func TestEstimateHistoryNoise(t *testing.T) {
	by := map[string][]float64{}
	for i := range 8 {
		by[fmt.Sprintf("t%d", i)] = []float64{1, 0}
	}
	by["single"] = []float64{1}
	p0, w, runs, ok := HistoryNoise(by)
	// every task: mean 0.5, squares 0.25 + 0.25 = 0.5 on 1 df: MSW 0.5.
	if !ok || p0 != 0.5 || w != 0.5 || runs != 16 {
		t.Fatalf("p0 %v w %v runs %v ok %v", p0, w, runs, ok)
	}
	delete(by, "t0")
	if _, _, _, ok := HistoryNoise(by); ok {
		t.Fatal("7 tasks with 2 trials gave history")
	}
}
