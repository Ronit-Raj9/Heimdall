package forecast

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// plannedManifest is a planned two-arm experiment on suite s@1: tasks t1..tn,
// trials per task, both arms.
func plannedManifest(tasks, trials int, budget float64) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: "e_new", SuiteID: "s", SuiteVersion: "1",
		Experiment: schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: budget}},
		Arms: map[string]schemas.ArmManifest{
			"A": {Label: "base", ConfigHash: "hashA", Config: schemas.AgentConfig{ID: "codex", Model: "m-a", Budgets: schemas.Budgets{WallS: 600}}},
			"B": {Label: "new", ConfigHash: "hashB", Config: schemas.AgentConfig{ID: "codex", Model: "m-b", Budgets: schemas.Budgets{WallS: 600, USD: 0.5}}},
		}}
	for t := 1; t <= tasks; t++ {
		for k := 1; k <= trials; k++ {
			for _, a := range []string{"A", "B"} {
				m.Slots = append(m.Slots, schemas.Slot{Arm: a, Task: taskID(t), Trial: k, Counted: true})
			}
		}
	}
	return m
}

func taskID(i int) string { return "t" + string(rune('0'+i)) }

// hist is one history run with an observed cost, tokens and wall time.
func hist(exp, hash, agent, model, task string, cost float64) HistoryRun {
	return HistoryRun{ExpID: exp, Suite: "s@1", ConfigHash: hash, Agent: agent, Model: model, Task: task,
		CostUSD: schemas.Observed(cost, schemas.EvidenceGateway), Tokens: schemas.Observed(cost*1e6, schemas.EvidenceGateway),
		WallS: schemas.Observed(cost*1000, schemas.EvidenceOutcome)}
}

func TestQuantile(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	tests := []struct{ p, want float64 }{{0, 1}, {0.5, 3}, {1, 5}, {0.1, 1.4}, {0.9, 4.6}}
	for _, tt := range tests {
		if got := Quantile(xs, tt.p); math.Abs(got-tt.want) > 1e-12 {
			t.Errorf("Quantile(%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
	if xs[0] != 5 {
		t.Fatal("Quantile sorted its input")
	}
	if !math.IsNaN(Quantile(nil, 0.5)) {
		t.Fatal("empty quantile not NaN")
	}
}

func TestEstimateBases(t *testing.T) {
	tests := []struct {
		name      string
		history   []HistoryRun
		priced    func(string) bool
		basisA    string
		basisB    string
		kindTotal string
		fit       string
	}{
		{
			name: "config hash beats agent and model",
			history: []HistoryRun{
				hist("e1", "hashA", "codex", "m-a", "t1", 0.10), hist("e1", "hashA", "codex", "m-a", "t2", 0.20),
				hist("e2", "other", "codex", "m-a", "t1", 9), hist("e2", "other", "codex", "m-b", "t1", 0.30),
			},
			basisA: schemas.BasisConfigHash, basisB: schemas.BasisAgentModel, kindTotal: schemas.RangeHistory, fit: schemas.FitFits,
		},
		{
			name:    "no history, budget bound",
			history: []HistoryRun{hist("e1", "hashA", "codex", "m-a", "t1", 0.10)},
			priced:  func(m string) bool { return m == "m-b" },
			basisA:  schemas.BasisConfigHash, basisB: schemas.BasisBudgetBound, kindTotal: schemas.RangeUpperBound, fit: schemas.FitFits, // 4 x 0.10 + 4 x 0.50 = 2.40 <= 3
		},
		{
			name:    "no history, unpriced: cannot estimate",
			history: []HistoryRun{hist("e1", "hashA", "codex", "m-a", "t1", 0.10)},
			basisA:  schemas.BasisConfigHash, basisB: schemas.BasisNone, kindTotal: schemas.RangeUnknown, fit: schemas.FitUnknown,
		},
		{
			name:    "other suite does not count",
			history: []HistoryRun{func() HistoryRun { h := hist("e1", "hashA", "codex", "m-a", "t1", 0.1); h.Suite = "s@2"; return h }()},
			basisA:  schemas.BasisNone, basisB: schemas.BasisNone, kindTotal: schemas.RangeUnknown, fit: schemas.FitUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Estimate(Input{Manifest: plannedManifest(2, 2, 3), History: tt.history, Priced: tt.priced, Concurrency: 2, Now: time.Unix(0, 0)})
			if len(f.Arms) != 2 || f.Arms[0].Basis != tt.basisA || f.Arms[1].Basis != tt.basisB {
				t.Fatalf("bases %s %s, want %s %s", f.Arms[0].Basis, f.Arms[1].Basis, tt.basisA, tt.basisB)
			}
			if f.CostUSD.Kind != tt.kindTotal || f.Fit != tt.fit {
				t.Fatalf("total %+v fit %s (%s), want %s %s", f.CostUSD, f.Fit, f.FitReason, tt.kindTotal, tt.fit)
			}
			for _, a := range f.Arms {
				if a.Runs != 4 {
					t.Errorf("arm %s runs %d", a.Arm, a.Runs)
				}
				if a.Basis == schemas.BasisNone && (a.CostUSD.Low != nil || a.CostUSD.High != nil || !strings.Contains(a.BasisText, "cannot estimate")) {
					t.Errorf("arm %s invents a number: %+v %q", a.Arm, a.CostUSD, a.BasisText)
				}
			}
		})
	}
}

func TestEstimateNumbers(t *testing.T) {
	// Arm A: t1 has 3 runs of its own (task level), t2 has one, so it uses
	// the suite-wide 4 runs. Arm B: one run.
	history := []HistoryRun{
		hist("e1", "hashA", "codex", "m-a", "t1", 0.10), hist("e1", "hashA", "codex", "m-a", "t1", 0.20),
		hist("e2", "hashA", "codex", "m-a", "t1", 0.30), hist("e2", "hashA", "codex", "m-a", "t2", 1.00),
		hist("e3", "hashB", "codex", "m-b", "t1", 0.05),
	}
	unknownCost := hist("e3", "hashB", "codex", "m-b", "t2", 0)
	unknownCost.CostUSD = schemas.Unknown("no gateway rows", schemas.EvidenceGateway)
	history = append(history, unknownCost)
	f := Estimate(Input{Manifest: plannedManifest(2, 2, 1.5), History: history, Concurrency: 4, Now: time.Unix(0, 0)})
	a := f.Arms[0]
	t1 := []float64{0.1, 0.2, 0.3}
	all := []float64{0.1, 0.2, 0.3, 1.0}
	wantLow := 2*Quantile(t1, 0.1) + 2*Quantile(all, 0.1)
	wantMid := 2*Quantile(t1, 0.5) + 2*Quantile(all, 0.5)
	wantHigh := 2*Quantile(t1, 0.9) + 2*Quantile(all, 0.9)
	if math.Abs(*a.CostUSD.Low-wantLow) > 1e-9 || math.Abs(*a.CostUSD.Median-wantMid) > 1e-9 || math.Abs(*a.CostUSD.High-wantHigh) > 1e-9 {
		t.Fatalf("arm A cost %v %v %v, want %v %v %v", *a.CostUSD.Low, *a.CostUSD.Median, *a.CostUSD.High, wantLow, wantMid, wantHigh)
	}
	if a.TaskLevel != 1 || a.HistoryRuns != 4 || strings.Join(a.Experiments, ",") != "e1,e2" || !strings.Contains(a.BasisText, "thin history") {
		t.Fatalf("arm A %+v", a)
	}
	if math.Abs(*a.PerRun.CostUSD.Median-Quantile(all, 0.5)) > 1e-12 {
		t.Fatalf("per-run median %v", *a.PerRun.CostUSD.Median)
	}
	b := f.Arms[1]
	if *b.CostUSD.Median != 4*0.05 || !strings.Contains(b.CostUSD.Reason, "1 of 2 history runs") {
		t.Fatalf("arm B cost %+v", b.CostUSD)
	}
	total := *a.CostUSD.Median + *b.CostUSD.Median
	if math.Abs(*f.CostUSD.Median-total) > 1e-12 {
		t.Fatalf("total %v, want %v", *f.CostUSD.Median, total)
	}
	if math.Abs(*f.WallClockS.Median-*f.AgentTimeS.Median/4) > 1e-9 || f.Concurrency != 4 {
		t.Fatalf("wall clock %v agent %v", *f.WallClockS.Median, *f.AgentTimeS.Median)
	}
	// Total median about 0.2*2 + 0.25*2 + 0.2 = 1.1, high over 1.5: may exceed.
	if f.Fit != schemas.FitMayExceed {
		t.Fatalf("fit %s: %s (low %v mid %v high %v)", f.Fit, f.FitReason, *f.CostUSD.Low, *f.CostUSD.Median, *f.CostUSD.High)
	}
	if !strings.Contains(f.Basis, "2 arms from sealed runs of the same config") {
		t.Fatalf("basis %q", f.Basis)
	}
}

func TestFit(t *testing.T) {
	r := historyRange(1, 2, 3)
	tests := []struct {
		r      schemas.ForecastRange
		budget float64
		want   string
	}{
		{r, 0, schemas.FitNoBudget},
		{r, 3, schemas.FitFits},
		{r, 2.5, schemas.FitMayExceed},
		{r, 1.5, schemas.FitLikelyExceeds},
		{r, 0.5, schemas.FitExceeds},
		{upperRange(2, ""), 2, schemas.FitFits},
		{upperRange(2, ""), 1, schemas.FitUnknown},
		{unknownRange("x"), 1, schemas.FitUnknown},
	}
	for _, tt := range tests {
		if got, reason := fit(tt.r, tt.budget); got != tt.want || reason == "" {
			t.Errorf("fit(%+v, %v) = %s %q, want %s", tt.r, tt.budget, got, reason, tt.want)
		}
	}
}

func TestEstimateJudge(t *testing.T) {
	m := plannedManifest(1, 1, 0)
	m.Scenario = map[string]string{"suite.judge": "jh"}
	judged := func(usd *schemas.Value, judge string) HistoryRun {
		h := hist("e1", "x", "y", "z", "t1", 0.1)
		h.JudgeUSD, h.JudgeHash = usd, judge
		return h
	}
	v := func(x float64) *schemas.Value { o := schemas.Observed(x, schemas.EvidenceGateway); return &o }
	f := Estimate(Input{Manifest: m, History: []HistoryRun{judged(v(0.01), "jh"), judged(v(0.03), "jh"), judged(v(5), "other"), judged(nil, "jh")}})
	if f.Judge == nil || f.Judge.HistoryRuns != 2 || f.Judge.Runs != 2 || math.Abs(*f.Judge.CostUSD.Median-2*0.02) > 1e-12 {
		t.Fatalf("judge %+v", f.Judge)
	}
	if f.Fit != schemas.FitNoBudget {
		t.Fatalf("fit %s", f.Fit)
	}
	none := Estimate(Input{Manifest: m})
	if none.Judge == nil || none.Judge.CostUSD.Kind != schemas.RangeUnknown {
		t.Fatalf("judge without records %+v", none.Judge)
	}
	if Estimate(Input{Manifest: plannedManifest(1, 1, 0)}).Judge != nil {
		t.Fatal("unjudged suite has a judge forecast")
	}
}
