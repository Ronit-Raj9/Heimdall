package agentdiff

import (
	"fmt"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/verdict"
)

// groupNames are the plain names of the config groups.
var groupNames = map[string]string{
	GroupModel: "model", GroupPrompt: "prompt and notes", GroupTools: "tools, MCP and skills", GroupBudgets: "budgets",
	GroupGateway: "gateway policy", GroupImage: "image", GroupOther: "other",
}

// Summarize says what the report found in a few plain sentences: pass counts
// and flips, what the sign-flip test can and cannot say, what changed in the
// config, and how cost moved. It is pure.
func Summarize(r *Report) string {
	o := r.Outcome
	var parts []string
	passA, passB := passText(o.PassRateA, o.PassRateB, o.CommonTasks)
	parts = append(parts, fmt.Sprintf("On %d common task%s of %s under %s, B passes %s and A passes %s.",
		o.CommonTasks, plural(o.CommonTasks), r.Suite, r.Condition, passB, passA))
	parts = append(parts, flipsText(o))
	parts = append(parts, testText(o.Test))
	parts = append(parts, configText(r.Config))
	if s := costText(o.Metrics); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// flipsText states the flips and the net change.
func flipsText(o OutcomeDiff) string {
	c := o.Counts
	s := fmt.Sprintf("%d task%s flipped to pass and %d flipped to fail (net %+d)", c.FlippedToPass, plural(c.FlippedToPass), c.FlippedToFail, o.Net)
	var extra []string
	if c.MovedToPass+c.MovedToFail > 0 {
		extra = append(extra, fmt.Sprintf("%d moved without a clean flip", c.MovedToPass+c.MovedToFail))
	}
	if c.UnstableInBoth > 0 {
		extra = append(extra, fmt.Sprintf("%d %s unstable in both", c.UnstableInBoth, isAre(c.UnstableInBoth)))
	}
	if len(extra) > 0 {
		s += "; " + strings.Join(extra, ", ")
	}
	return s + "."
}

// testText says what the exact paired sign-flip test supports.
func testText(t SignTest) string {
	switch t.Conclusion {
	case ConclusionBetter:
		return fmt.Sprintf("B is better: exact paired sign-flip p = %.3g.", *t.P)
	case ConclusionWorse:
		return fmt.Sprintf("B is worse: exact paired sign-flip p = %.3g.", *t.P)
	case ConclusionNoClear:
		return fmt.Sprintf("No clear difference: exact paired sign-flip p = %.3g.", *t.P)
	}
	return fmt.Sprintf("Cannot tell whether B is better or worse: %d task%s changed, and at least %d must change before any result can reach p < %g.",
		t.NNonzero, plural(t.NNonzero), tasksNeeded(t.Alpha), t.Alpha)
}

// tasksNeeded is the fewest changed tasks whose smallest possible p is
// below alpha.
func tasksNeeded(alpha float64) int {
	n := 2
	for stats.MinPossibleP(n) > alpha && n < 64 {
		n++
	}
	return n
}

// configText names the config groups that changed.
func configText(c ConfigDiff) string {
	if c.Identical {
		return "The configs are identical, so any difference is run to run variation."
	}
	names := make([]string, 0, len(c.Groups))
	for _, g := range c.Groups {
		names = append(names, groupNames[g.ID])
	}
	return fmt.Sprintf("The config changed in %d place%s (%s).", c.Changes, plural(c.Changes), strings.Join(names, ", "))
}

// costText states how the mean cost per run moved, when both sides observed
// it.
func costText(ms []MetricDelta) string {
	for _, m := range ms {
		if m.Metric != verdict.MetricCostUSD || m.Delta == nil || m.Direction == DirectionSame {
			continue
		}
		word := "more"
		if *m.Delta < 0 {
			word = "less"
		}
		return fmt.Sprintf("B costs $%.4f %s per run on average ($%.4f against $%.4f).", abs(*m.Delta), word, *m.B, *m.A)
	}
	return ""
}

// passText writes both sides' mean per-task pass rates in one unit: task
// counts with the percent ("3 of 5 tasks (60%)") when both are whole numbers
// of tasks, else percents for both, with A's side saying once that trials are
// averaged ("40% of the tasks (averaged over trials)"). Mixing a bare count on
// one side with a percent on the other read as "B passes 50% and A passes 2".
func passText(rA, rB float64, n int) (a, b string) {
	kA, wholeA := wholeTasks(rA, n)
	kB, wholeB := wholeTasks(rB, n)
	if wholeA && wholeB {
		return fmt.Sprintf("%d of %d task%s (%.0f%%)", kA, n, plural(n), rA*100), fmt.Sprintf("%d of %d task%s (%.0f%%)", kB, n, plural(n), rB*100)
	}
	return fmt.Sprintf("%.0f%% of the tasks (averaged over trials)", rA*100), fmt.Sprintf("%.0f%%", rB*100)
}

// wholeTasks is r*n as a task count when it is a whole number.
func wholeTasks(r float64, n int) (int, bool) {
	passed := r * float64(n)
	whole := math.Round(passed)
	return int(whole), abs(passed-whole) < 1e-9
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
