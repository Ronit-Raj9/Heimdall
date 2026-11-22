package agentdiff

import (
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	p := 0.0156
	costA, costB, delta := 0.05, 0.03, -0.02
	cases := []struct {
		name string
		r    Report
		want []string
	}{
		{
			name: "better with cost",
			r: Report{Suite: "s@1", Condition: "clean",
				Config: ConfigDiff{Changes: 2, Groups: []ConfigGroup{{ID: GroupModel}, {ID: GroupPrompt}}},
				Outcome: OutcomeDiff{CommonTasks: 8, PassRateA: 0.25, PassRateB: 1, Net: 6, Counts: ClassCounts{FlippedToPass: 6, UnchangedPass: 2},
					Test:    SignTest{Conclusion: ConclusionBetter, P: &p, Alpha: 0.05},
					Metrics: []MetricDelta{{Metric: "cost_usd", A: &costA, B: &costB, Delta: &delta, Direction: DirectionBetter}}}},
			want: []string{"On 8 common tasks of s@1 under clean, B passes 8 of 8 tasks (100%) and A passes 2 of 8 tasks (25%).", "6 tasks flipped to pass and 0 flipped to fail (net +6).",
				"B is better: exact paired sign-flip p = 0.0156.", "The config changed in 2 places (model, prompt and notes).", "B costs $0.0200 less per run"},
		},
		{
			name: "cannot tell, identical, trials",
			r: Report{Suite: "s@1", Condition: "stale-notes", Config: ConfigDiff{Identical: true},
				Outcome: OutcomeDiff{CommonTasks: 1, PassRateA: 0.5, PassRateB: 1.0 / 3, Counts: ClassCounts{UnstableInBoth: 1},
					Test: SignTest{Conclusion: ConclusionCannotTell, NNonzero: 1, Alpha: 0.05}}},
			want: []string{"On 1 common task", "B passes 33% and A passes 50% of the tasks (averaged over trials).", "1 is unstable in both", "Cannot tell whether B is better or worse: 1 task changed, and at least 6 must change",
				"identical, so any difference is run to run variation."},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Summarize(&c.r)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("summary %q lacks %q", got, w)
				}
			}
			if strings.ContainsAny(got, "\u2013\u2014") {
				t.Errorf("summary has a dash: %q", got)
			}
		})
	}
}

// TestSummaryPassTextOneUnit pins N158: both sides of the pass sentence use
// one unit, task counts when both are whole and percents otherwise.
func TestSummaryPassTextOneUnit(t *testing.T) {
	a, b := passText(0.4, 0.5, 5)
	if b != "50%" || a != "40% of the tasks (averaged over trials)" {
		t.Errorf("fractional: a %q b %q", a, b)
	}
	a, b = passText(0.4, 0.6, 5)
	if b != "3 of 5 tasks (60%)" || a != "2 of 5 tasks (40%)" {
		t.Errorf("whole: a %q b %q", a, b)
	}
	r := Report{Suite: "s@1", Condition: "clean", Config: ConfigDiff{Identical: true},
		Outcome: OutcomeDiff{CommonTasks: 5, PassRateA: 0.4, PassRateB: 0.5, Test: SignTest{Conclusion: ConclusionCannotTell, Alpha: 0.05}}}
	if got, want := Summarize(&r), "B passes 50% and A passes 40% of the tasks (averaged over trials)."; !strings.Contains(got, want) {
		t.Errorf("summary %q lacks %q", got, want)
	}
}
