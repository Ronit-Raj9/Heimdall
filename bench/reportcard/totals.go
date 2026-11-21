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
}

// totals computes Totals per arm label.
func totals(children []childRuns, labels []string, faultOf func(childRuns, string) string) map[string]Totals {
	out := make(map[string]Totals, len(labels))
	for _, l := range labels {
		var clean []graded
		var cs []cluster
		faultSucc, faultRuns := 0, 0
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
		}
		out[l] = t
	}
	return out
}
