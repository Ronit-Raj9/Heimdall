package reportcard

import "github.com/maximhq/bifrost/bench/schemas"

// metricFalseCompletion is the run-level false completion metric
// (metrics/falsecompletion.go): 1 when a failed run claimed success.
const metricFalseCompletion = "false_completion"

// Totals is one arm's numbers over the whole evaluation: the clean score
// pooled over every benchmark with its task-clustered interval, cost per
// solved task (only when every clean run's cost is known), false completion
// and the success rate under faults. The release card and the gate read
// them. A pointer is nil when unknown.
type Totals struct {
	Score           float64  `json:"score"`
	CILow           float64  `json:"ci_low"`
	CIHigh          float64  `json:"ci_high"`
	Runs            int      `json:"runs"`
	Solved          int      `json:"solved"`
	CostUSD         *float64 `json:"cost_usd"`                      // clean cost, nil when any clean run's cost is not known (never a partial sum)
	CostRuns        int      `json:"cost_runs"`                     // clean runs whose cost is known
	CostPerSolved   *float64 `json:"cost_per_solved_usd,omitempty"` // clean cost over clean solved tasks; nil with no cost or nothing solved
	FalseCompletion *float64 `json:"false_completion,omitempty"`    // mean false_completion over the clean runs that observed it
	FaultSurvival   *float64 `json:"fault_survival,omitempty"`      // success rate over runs with an evaluation fault injected
	FaultRuns       int      `json:"fault_runs,omitempty"`
	// FaultCostUSD is the cost of the runs with an evaluation fault
	// injected (fix wave M7-89), nil when any of their costs is not known;
	// FaultCostRuns counts those whose cost is known. With CostUSD it splits
	// the evaluation's cost into normal and fault runs.
	FaultCostUSD  *float64 `json:"fault_cost_usd,omitempty"`
	FaultCostRuns int      `json:"fault_cost_runs,omitempty"`
	// JudgeRuns counts the clean runs a model judge graded (judge_cost_usd
	// applies); JudgeCostUSD is what those judge calls cost, apart from
	// CostUSD (the agent's), nil when any of them is not priced;
	// JudgeCostRuns counts the priced ones (round 4, F5, PH3-2).
	JudgeRuns     int      `json:"judge_runs,omitempty"`
	JudgeCostUSD  *float64 `json:"judge_cost_usd,omitempty"`
	JudgeCostRuns int      `json:"judge_cost_runs,omitempty"`
}

// judgeCost sums judge_cost_usd over the runs a judge graded: the judged
// run count, the priced count and the sum (nil unless every one is priced).
func judgeCost(runs []graded) (judged, priced int, cost *float64) {
	var judgedRuns []graded
	for _, r := range runs {
		if v, ok := r.metrics[metricJudgeCostUSD]; ok && v.State != schemas.ValueNotApplicable {
			judgedRuns = append(judgedRuns, r)
		}
	}
	if len(judgedRuns) == 0 {
		return 0, 0, nil
	}
	total, known, _ := sumKnown(judgedRuns, metricJudgeCostUSD)
	if known == len(judgedRuns) {
		c := round(total)
		cost = &c
	}
	return len(judgedRuns), known, cost
}

// totals computes Totals per arm label.
func totals(children []childRuns, labels []string, faultOf func(childRuns, string) string) map[string]Totals {
	out := make(map[string]Totals, len(labels))
	for _, l := range labels {
		var clean []graded
		var cs []cluster
		faultSucc, faultRuns := 0, 0
		var faulted []graded
		for _, cr := range children {
			c := filter(cr.arms[l], schemas.ConditionClean)
			clean = append(clean, c...)
			cs = append(cs, clustersOf(c, cr.child.Benchmark+"\x00")...)
			for _, r := range cr.arms[l] {
				if f := faultOf(cr, r.condition); r.condition == schemas.ConditionClean || f == "" || f == Attack {
					continue
				}
				faultSucc += r.success
				faultRuns++
				faulted = append(faulted, r)
			}
		}
		t := Totals{Runs: len(clean)}
		if len(clean) > 0 {
			t.Solved = successes(clean)
			t.Score = round(float64(t.Solved) / float64(len(clean)))
			lo, hi := taskCI(cs)
			t.CILow, t.CIHigh = round(lo), round(hi)
			total, known, _ := sumKnown(clean, metricCostUSD)
			t.CostRuns = known
			if known == len(clean) {
				c := round(total)
				t.CostUSD = &c
				if t.Solved > 0 {
					v := round(total / float64(t.Solved))
					t.CostPerSolved = &v
				}
			}
			t.JudgeRuns, t.JudgeCostRuns, t.JudgeCostUSD = judgeCost(clean)
			if fc := observed(clean, metricFalseCompletion); len(fc) > 0 {
				s := 0.0
				for _, x := range fc {
					s += x
				}
				v := round(s / float64(len(fc)))
				t.FalseCompletion = &v
			}
		}
		if faultRuns > 0 {
			v := round(float64(faultSucc) / float64(faultRuns))
			t.FaultSurvival, t.FaultRuns = &v, faultRuns
			total, known, _ := sumKnown(faulted, metricCostUSD)
			t.FaultCostRuns = known
			if known == len(faulted) {
				c := round(total)
				t.FaultCostUSD = &c
			}
		}
		out[l] = t
	}
	return out
}
