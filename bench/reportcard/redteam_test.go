package reportcard

import (
	"testing"

	"github.com/maximhq/bifrost/bench/redteam"
)

func TestRedTeamSection(t *testing.T) {
	broke := func(n float64) map[string]float64 {
		return map[string]float64{redteam.MetricAttackSucceeded: 1, redteam.MetricAttemptsToBreak: n}
	}
	held := map[string]float64{redteam.MetricAttackSucceeded: 0}
	children := []childRuns{
		{arms: map[string][]graded{"A": {
			{task: "rt.prompt_leak.pair.chat", runID: "r1", condition: "clean", metrics: obs(broke(2))},
			{task: "rt.prompt_leak.tap.chat", runID: "r2", condition: "clean", metrics: obs(held)},
			{task: "rt.goal_hijack.crescendo.email", runID: "r3", condition: "clean", metrics: obs(broke(5))},
			{task: "rt.policy_bypass.pair.document", runID: "r4", condition: "clean", metrics: obs(held)},
			{task: "reg.prompt_leak.pair.chat.abcdef0123", runID: "r5", condition: "clean", metrics: obs(held)},
		}}},
		{arms: map[string][]graded{"A": {{task: "t1", condition: "clean"}}}},
	}
	children[0].child.Benchmark = "red-team"
	children[1].child.Benchmark = "other"
	rt := redTeam(children, []string{"A"})
	if rt == nil || len(rt.Benchmarks) != 1 || rt.Benchmarks[0] != "red-team" {
		t.Fatalf("section = %+v", rt)
	}
	a := rt.Arms["A"]
	if a.Attacks != 5 || a.Broken != 2 || a.SuccessRate != 0.4 || a.Regressions != 1 || a.CILow <= 0 || a.CIHigh >= 1 {
		t.Fatalf("arm = %+v", a)
	}
	if a.MedianAttemptsToBreak == nil || *a.MedianAttemptsToBreak != 3.5 || *a.FewestAttemptsToBreak != 2 {
		t.Fatalf("attempts = %v %v", a.MedianAttemptsToBreak, a.FewestAttemptsToBreak)
	}
	if a.Hardest == nil || a.Hardest.Run != "r3" || a.Hardest.AttemptsToBreak != 5 {
		t.Fatalf("hardest = %+v", a.Hardest)
	}
	if a.ByCategory[0].ID != redteam.CategoryPromptLeak || a.ByCategory[0].Attacks != 3 || a.ByCategory[0].Broken != 1 {
		t.Fatalf("by category = %+v", a.ByCategory)
	}
	if a.MostEffective == nil || a.MostEffective.SuccessRate != 1 {
		t.Fatalf("most effective = %+v", a.MostEffective)
	}
	if redTeam(children[1:], []string{"A"}) != nil {
		t.Fatal("no red-team runs must give no section")
	}
}
