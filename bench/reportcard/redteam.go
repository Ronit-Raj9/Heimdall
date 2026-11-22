package reportcard

import (
	"cmp"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/redteam"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Red-team section (Z04, docs/red-team.md): how often an adaptive attacker
// broke each arm, by category, strategy and channel, how many attempts the
// first break took, and the hardest attack that still got through. It
// reads only the verifier's reward dimensions of red-team tasks (task ids
// redteam.ParseTaskID knows), so a run without them is left out.

// RedTeam is the red-team section of a report.
type RedTeam struct {
	Benchmarks []string              `json:"benchmarks"`
	Arms       map[string]RedTeamArm `json:"arms"`
}

// RedTeamArm is one arm's red-team numbers. SuccessRate is the attack
// success rate (lower is better) with a 95% Wilson interval.
type RedTeamArm struct {
	Attacks     int     `json:"attacks"` // graded red-team runs
	Broken      int     `json:"broken"`
	SuccessRate float64 `json:"success_rate"`
	CILow       float64 `json:"ci_low"`
	CIHigh      float64 `json:"ci_high"`
	// MedianAttemptsToBreak is the median attempts the successful attacks
	// needed; FewestAttemptsToBreak the quickest break. Nil when none broke.
	MedianAttemptsToBreak *float64       `json:"median_attempts_to_break"`
	FewestAttemptsToBreak *int           `json:"fewest_attempts_to_break"`
	ByCategory            []RedTeamRow   `json:"by_category"`
	ByStrategy            []RedTeamRow   `json:"by_strategy"`
	ByChannel             []RedTeamRow   `json:"by_channel"`
	Hardest               *RedTeamAttack `json:"hardest,omitempty"`        // the successful attack that needed the most attempts
	MostEffective         *RedTeamRow    `json:"most_effective,omitempty"` // the category and strategy with the highest success rate
	Regressions           int            `json:"regressions"`              // runs of replayed regression tasks among Attacks
}

// RedTeamRow is the attack success of one group.
type RedTeamRow struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Attacks     int     `json:"attacks"`
	Broken      int     `json:"broken"`
	SuccessRate float64 `json:"success_rate"`
	CILow       float64 `json:"ci_low"`
	CIHigh      float64 `json:"ci_high"`
}

// RedTeamAttack names one attack run.
type RedTeamAttack struct {
	Task            string `json:"task"`
	Run             string `json:"run"`
	Category        string `json:"category"`
	Strategy        string `json:"strategy"`
	Channel         string `json:"channel"`
	Summary         string `json:"summary"`
	AttemptsToBreak int    `json:"attempts_to_break"`
}

type rtRun struct {
	cell     redteam.Cell
	task     string
	run      string
	broken   bool
	attempts int
	reg      bool
}

func redTeam(children []childRuns, labels []string) *RedTeam {
	out := &RedTeam{Benchmarks: []string{}, Arms: map[string]RedTeamArm{}}
	byArm := map[string][]rtRun{}
	for _, cr := range children {
		seen := false
		for _, l := range labels {
			for _, r := range cr.arms[l] {
				c, ok := redteam.ParseTaskID(r.task)
				if !ok || r.condition != schemas.ConditionClean {
					continue
				}
				v, ok := r.metrics[redteam.MetricAttackSucceeded].Float()
				if !ok {
					continue
				}
				seen = true
				x := rtRun{cell: c, task: r.task, run: r.runID, broken: v == 1, reg: redteam.IsRegressionTask(r.task)}
				if a, ok := r.metrics[redteam.MetricAttemptsToBreak].Float(); ok {
					x.attempts = int(a)
				}
				byArm[l] = append(byArm[l], x)
			}
		}
		if seen {
			out.Benchmarks = append(out.Benchmarks, cr.child.Benchmark)
		}
	}
	if len(out.Benchmarks) == 0 {
		return nil
	}
	for _, l := range labels {
		if runs := byArm[l]; len(runs) > 0 {
			out.Arms[l] = redTeamArm(runs)
		}
	}
	return out
}

func redTeamArm(runs []rtRun) RedTeamArm {
	a := RedTeamArm{Attacks: len(runs)}
	var attempts []float64
	for _, r := range runs {
		if r.reg {
			a.Regressions++
		}
		if !r.broken {
			continue
		}
		a.Broken++
		if r.attempts > 0 {
			attempts = append(attempts, float64(r.attempts))
			if a.FewestAttemptsToBreak == nil || r.attempts < *a.FewestAttemptsToBreak {
				n := r.attempts
				a.FewestAttemptsToBreak = &n
			}
		}
		if a.Hardest == nil || r.attempts > a.Hardest.AttemptsToBreak {
			a.Hardest = &RedTeamAttack{Task: r.task, Run: r.run, Category: r.cell.Category, Strategy: r.cell.Strategy, Channel: r.cell.Channel, AttemptsToBreak: r.attempts,
				Summary: redteam.CategoryLabel(r.cell.Category) + " by " + redteam.StrategyLabel(r.cell.Strategy) + " through " + redteam.ChannelLabel(r.cell.Channel)}
		}
	}
	a.SuccessRate, a.CILow, a.CIHigh = rate(a.Broken, a.Attacks)
	if len(attempts) > 0 {
		m := stats.Median(attempts)
		a.MedianAttemptsToBreak = &m
	}
	a.ByCategory = groupRows(runs, func(r rtRun) string { return r.cell.Category }, redteam.CategoryLabel, catalogOrder(redteam.Categories(), func(c redteam.Category) string { return c.ID }))
	a.ByStrategy = groupRows(runs, func(r rtRun) string { return r.cell.Strategy }, redteam.StrategyLabel, catalogOrder(redteam.Strategies(), func(s redteam.Strategy) string { return s.ID }))
	a.ByChannel = groupRows(runs, func(r rtRun) string { return r.cell.Channel }, redteam.ChannelLabel, catalogOrder(redteam.Channels(), func(c redteam.Channel) string { return c.ID }))
	cells := groupRows(runs, func(r rtRun) string { return r.cell.Category + "/" + r.cell.Strategy }, func(id string) string {
		c, s, _ := cutSlash(id)
		return redteam.CategoryLabel(c) + " by " + redteam.StrategyLabel(s)
	}, nil)
	for i := range cells {
		if cells[i].Broken == 0 {
			continue
		}
		if a.MostEffective == nil || cells[i].SuccessRate > a.MostEffective.SuccessRate {
			a.MostEffective = &cells[i]
		}
	}
	return a
}

func cutSlash(s string) (string, string, bool) {
	for i := range s {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func catalogOrder[T any](xs []T, id func(T) string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, id(x))
	}
	return out
}

// groupRows groups runs by key, in order (catalog order first, then by id).
func groupRows(runs []rtRun, key func(rtRun) string, label func(string) string, order []string) []RedTeamRow {
	idx := map[string]*RedTeamRow{}
	for _, r := range runs {
		k := key(r)
		row := idx[k]
		if row == nil {
			row = &RedTeamRow{ID: k, Label: label(k)}
			idx[k] = row
		}
		row.Attacks++
		if r.broken {
			row.Broken++
		}
	}
	out := make([]RedTeamRow, 0, len(idx))
	for _, row := range idx {
		row.SuccessRate, row.CILow, row.CIHigh = rate(row.Broken, row.Attacks)
		out = append(out, *row)
	}
	pos := func(id string) int {
		if i := slices.Index(order, id); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortFunc(out, func(x, y RedTeamRow) int { return cmp.Or(cmp.Compare(pos(x.ID), pos(y.ID)), cmp.Compare(x.ID, y.ID)) })
	return out
}

// rate is k of n with a 95% Wilson interval, rounded.
func rate(k, n int) (p, lo, hi float64) {
	if n == 0 {
		return 0, 0, 0
	}
	const z = 1.959963984540054
	fn, ph := float64(n), float64(k)/float64(n)
	den := 1 + z*z/fn
	mid := (ph + z*z/(2*fn)) / den
	half := z * math.Sqrt(ph*(1-ph)/fn+z*z/(4*fn*fn)) / den
	return round(ph), round(math.Max(0, mid-half)), round(math.Min(1, mid+half))
}
