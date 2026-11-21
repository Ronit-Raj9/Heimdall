package verdictab

import (
	"fmt"
	"strings"
	"testing"
)

// arm builds outcomes: tasks t0..t(n-1), trials per task, success when pass(task, trial).
func arm(n, trials int, pass func(task, trial int) bool) []TaskOutcome {
	var out []TaskOutcome
	for t := 0; t < n; t++ {
		for k := 1; k <= trials; k++ {
			s := pass(t, k)
			sc := 0.0
			if s {
				sc = 1
			}
			out = append(out, TaskOutcome{TaskID: fmt.Sprintf("t%d", t), Trial: k, Success: s, Score: sc})
		}
	}
	return out
}

func always(v bool) func(int, int) bool { return func(int, int) bool { return v } }

func TestCompareNoRuns(t *testing.T) {
	v := Compare(nil, nil)
	if v.Winner != WinnerUnclear || v.Significant || v.Tasks != 0 {
		t.Fatalf("got %+v", v)
	}
	if !strings.Contains(v.Summary, "No runs") {
		t.Fatalf("summary %q", v.Summary)
	}
}

func TestCompareOneSideMissing(t *testing.T) {
	b := arm(10, 2, always(true))
	for _, tc := range []struct {
		a, b []TaskOutcome
		who  string
	}{{nil, b, "A has no runs"}, {b, nil, "B has no runs"}} {
		v := Compare(tc.a, tc.b)
		if v.Winner != WinnerUnclear || v.Significant {
			t.Fatalf("got %+v", v)
		}
		if !strings.Contains(v.Summary, tc.who) {
			t.Fatalf("summary %q", v.Summary)
		}
	}
}

func TestCompareNoCommonTasks(t *testing.T) {
	a := []TaskOutcome{{TaskID: "x", Trial: 1, Success: true}}
	b := []TaskOutcome{{TaskID: "y", Trial: 1, Success: false}}
	v := Compare(a, b)
	if v.Winner != WinnerUnclear || v.Tasks != 0 {
		t.Fatalf("got %+v", v)
	}
}

func TestCompareAllTies(t *testing.T) {
	a := arm(12, 2, func(task, _ int) bool { return task%2 == 0 })
	b := arm(12, 2, func(task, _ int) bool { return task%2 == 0 })
	v := Compare(a, b)
	if v.Winner != WinnerTie || v.Significant || v.Delta != 0 || v.Discordant != 0 {
		t.Fatalf("got %+v", v)
	}
	if v.SuccessA != 0.5 || v.SuccessB != 0.5 {
		t.Fatalf("rates %v %v", v.SuccessA, v.SuccessB)
	}
}

func TestCompareTiesTooFewTasksIsUnclear(t *testing.T) {
	v := Compare(arm(3, 2, always(true)), arm(3, 2, always(true)))
	if v.Winner != WinnerUnclear {
		t.Fatalf("got %+v", v)
	}
	if !strings.Contains(v.Summary, "Only 3 tasks") {
		t.Fatalf("summary %q", v.Summary)
	}
}

func TestCompareBWins(t *testing.T) {
	v := Compare(arm(10, 2, always(false)), arm(10, 2, always(true)))
	if v.Winner != WinnerB || !v.Significant || v.Delta != 1 || v.PValue == nil || *v.PValue >= Alpha {
		t.Fatalf("got %+v", v)
	}
	if v.ScoreDelta != 1 || len(v.TaskDiffs) != 10 {
		t.Fatalf("score delta %v diffs %d", v.ScoreDelta, len(v.TaskDiffs))
	}
	if !strings.HasPrefix(v.Summary, "B solves 100 points more") {
		t.Fatalf("summary %q", v.Summary)
	}
}

func TestCompareAWins(t *testing.T) {
	v := Compare(arm(8, 1, always(true)), arm(8, 1, always(false)))
	if v.Winner != WinnerA || !v.Significant || v.Delta != -1 {
		t.Fatalf("got %+v", v)
	}
}

func TestCompareLopsidedButTooFewTasks(t *testing.T) {
	// 5 tasks all for B: the best possible p is 0.0625, never significant.
	v := Compare(arm(5, 3, always(false)), arm(5, 3, always(true)))
	if v.Winner != WinnerUnclear || v.Significant {
		t.Fatalf("got %+v", v)
	}
}

func TestCompareNoisyGapIsUnclear(t *testing.T) {
	// B better on 4 tasks, worse on 2, same on 6.
	a := arm(12, 1, func(task, _ int) bool { return task >= 4 && task < 6 })
	b := arm(12, 1, func(task, _ int) bool { return task < 4 })
	v := Compare(a, b)
	if v.Winner != WinnerUnclear || v.Significant {
		t.Fatalf("got %+v", v)
	}
	if !strings.Contains(v.Summary, "could be chance") {
		t.Fatalf("summary %q", v.Summary)
	}
}

func TestCompareZeroTrialFallsBack(t *testing.T) {
	a := []TaskOutcome{{TaskID: "t", Success: false}, {TaskID: "t", Success: false}}
	b := []TaskOutcome{{TaskID: "t", Success: true}, {TaskID: "t", Success: false}}
	v := Compare(a, b)
	if v.Tasks != 1 || v.Delta != 0.5 || v.RunsA != 2 {
		t.Fatalf("got %+v", v)
	}
}

func TestOverallEmpty(t *testing.T) {
	o := Overall(nil)
	if o.Winner != WinnerUnclear || o.Benchmarks != 0 || o.Wins[WinnerB] != 0 {
		t.Fatalf("got %+v", o)
	}
	o = Overall([]BenchmarkVerdict{Compare(nil, nil)})
	if o.Winner != WinnerUnclear || o.Wins[WinnerUnclear] != 1 {
		t.Fatalf("got %+v", o)
	}
}

func TestOverallPoolsThinBenchmarks(t *testing.T) {
	// Four benchmarks of 3 tasks each, B always solves, A never: each one is
	// unclear alone, but 12 pooled tasks are significant.
	var vs []BenchmarkVerdict
	for i := 0; i < 4; i++ {
		v := Compare(arm(3, 2, always(false)), arm(3, 2, always(true)))
		if v.Winner != WinnerUnclear {
			t.Fatalf("bench %d: %+v", i, v)
		}
		vs = append(vs, v)
	}
	o := Overall(vs)
	if o.Winner != WinnerB || !o.Significant || o.Tasks != 12 || o.Wins[WinnerUnclear] != 4 {
		t.Fatalf("got %+v", o)
	}
	if !strings.Contains(o.Summary, "B is better overall") || !strings.Contains(o.Summary, "unclear on 4") {
		t.Fatalf("summary %q", o.Summary)
	}
}

func TestOverallMixed(t *testing.T) {
	bWin := Compare(arm(10, 1, always(false)), arm(10, 1, always(true)))
	aWin := Compare(arm(10, 1, always(true)), arm(10, 1, always(false)))
	tie := Compare(arm(10, 1, always(true)), arm(10, 1, always(true)))
	o := Overall([]BenchmarkVerdict{bWin, aWin, tie})
	if o.Wins[WinnerA] != 1 || o.Wins[WinnerB] != 1 || o.Wins[WinnerTie] != 1 {
		t.Fatalf("wins %+v", o.Wins)
	}
	if o.Winner == WinnerA || o.Winner == WinnerB || o.Significant || o.Delta != 0 {
		t.Fatalf("got %+v", o)
	}
	if !strings.Contains(o.Summary, "B is better on 1 of 3 benchmarks, worse on 1, tied on 1.") {
		t.Fatalf("summary %q", o.Summary)
	}
}

func TestOverallAllTies(t *testing.T) {
	tie := Compare(arm(8, 2, always(true)), arm(8, 2, always(true)))
	o := Overall([]BenchmarkVerdict{tie, tie})
	if o.Winner != WinnerTie || o.Wins[WinnerTie] != 2 || o.Tasks != 16 {
		t.Fatalf("got %+v", o)
	}
}

func TestSummariesHaveNoEmDash(t *testing.T) {
	vs := []BenchmarkVerdict{
		Compare(nil, nil),
		Compare(arm(10, 1, always(false)), arm(10, 1, always(true))),
		Compare(arm(10, 1, always(true)), arm(10, 1, always(true))),
		Compare(arm(2, 1, always(true)), arm(2, 1, always(false))),
	}
	o := Overall(vs)
	for _, s := range append([]string{o.Summary}, vs[0].Summary, vs[1].Summary, vs[2].Summary, vs[3].Summary) {
		if strings.ContainsAny(s, "—–") {
			t.Fatalf("dash in %q", s)
		}
	}
}

// Many benchmarks are many tests: two benchmarks each just under Alpha on
// their own (6 tasks, p = 0.03125) are not significant after Holm, while a
// benchmark with a clear gap over 10 tasks still is.
func TestHolmAcrossBenchmarks(t *testing.T) {
	thin1 := Compare(arm(6, 1, always(false)), arm(6, 1, always(true)))
	thin2 := Compare(arm(6, 1, always(false)), arm(6, 1, always(true)))
	clear := Compare(arm(10, 1, always(false)), arm(10, 1, always(true)))
	if !thin1.Significant || thin1.Winner != WinnerB || !clear.Significant {
		t.Fatalf("raw verdicts %+v %+v", thin1, clear)
	}
	adj := Holm([]BenchmarkVerdict{thin1, thin2, clear})
	for i, v := range adj[:2] {
		if v.Significant || v.Winner == WinnerB || v.PValueHolm == nil || *v.PValueHolm < Alpha {
			t.Errorf("thin %d after Holm %+v", i, v)
		}
	}
	if !adj[2].Significant || adj[2].Winner != WinnerB || *adj[2].PValueHolm >= Alpha {
		t.Errorf("clear after Holm %+v (p_holm %v)", adj[2], *adj[2].PValueHolm)
	}
	if !thin1.Significant {
		t.Error("Holm changed its input")
	}
	// One benchmark: unchanged.
	if one := Holm([]BenchmarkVerdict{thin1}); !one[0].Significant || *one[0].PValueHolm != *thin1.PValue {
		t.Errorf("single %+v", one[0])
	}
	// The overall tally counts wins after Holm.
	o := Overall([]BenchmarkVerdict{thin1, thin2, clear})
	if o.Wins[WinnerB] != 1 {
		t.Errorf("overall wins %v, want B=1 after Holm", o.Wins)
	}
}

// TestComparePracticeNeverCounts covers X01: practice runs (the scripted
// model) are dropped before anything is computed.
func TestComparePracticeNeverCounts(t *testing.T) {
	var a, b []TaskOutcome
	for _, task := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8"} {
		a = append(a, TaskOutcome{TaskID: task, Trial: 1, Success: false})
		b = append(b, TaskOutcome{TaskID: task, Trial: 1, Success: true, Score: 1, Practice: true})
	}
	v := Compare(a, b)
	if v.RunsB != 0 || v.Winner != WinnerUnclear || v.Tasks != 0 {
		t.Fatalf("practice runs counted: %+v", v)
	}
	for i := range b {
		b[i].Practice = false
	}
	if v := Compare(a, b); v.Winner != WinnerB {
		t.Fatalf("real runs: winner %s", v.Winner)
	}
}
