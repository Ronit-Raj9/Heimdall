package leaderboard

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Tiers.
const (
	TierLeader = 1 // cannot be told apart from the leader
	TierBehind = 2 // significantly behind the leader
)

// rank orders entries and assigns Rank, Tier, PVsLeader and TierReason.
//
// Order: Success descending (unknown last), then cost per success ascending
// (anything not observed last), then more graded runs, then config hash, so
// the order is total and stable across calls.
//
// Tiers: each entry after the leader is compared with it on the tasks both
// have in scope. d_t is the leader's success rate minus the entry's on task
// t; stats.SignFlip gives the two-sided p. The leader is picked from the same
// scores, so its comparisons are not a predetermined family: the Holm
// adjustment runs over every pair of entries with a task in common, and each
// entry keeps the adjusted p of its pair with the leader (audit 1-B-11). An
// entry is tier 2 only when
// its adjusted p is below alpha and the leader is ahead on those tasks.
// Everything else is tier 1, with the reason in plain words: too few
// differing tasks for any split to reach alpha (stats.MinPossibleP), no
// task in common, equal on every common task, or not significant.
func rank(calcs []*entryCalc, alpha float64) {
	slices.SortStableFunc(calcs, compareEntries)
	for i, c := range calcs {
		c.e.Rank = i + 1
	}
	if len(calcs) == 0 {
		return
	}
	leader := calcs[0]
	leader.e.Tier, leader.e.TierReason, leader.e.PVsLeader = TierLeader, "leader", nil
	type test struct {
		c    *entryCalc
		d    []float64
		res  stats.SignFlipResult
		mean float64
	}
	var tests []test
	for _, c := range calcs[1:] {
		c.e.Tier, c.e.PVsLeader = TierLeader, nil
		var d []float64
		for _, task := range sortedKeys(leader.rates) {
			if r, ok := c.rates[task]; ok {
				d = append(d, leader.rates[task]-r)
			}
		}
		if len(d) == 0 {
			c.e.TierReason = "no tasks in common with the leader"
			continue
		}
		tests = append(tests, test{c: c, d: d, res: stats.SignFlip(d), mean: stats.Mean(d)})
	}
	raw := make([]float64, len(tests), len(calcs)*(len(calcs)-1)/2)
	for i, t := range tests {
		raw[i] = t.res.P
	}
	for i, x := range calcs[1:] { // the pairs not involving the leader join the family
		for _, y := range calcs[i+2:] {
			var d []float64
			for _, task := range sortedKeys(x.rates) {
				if r, ok := y.rates[task]; ok {
					d = append(d, x.rates[task]-r)
				}
			}
			if len(d) > 0 {
				raw = append(raw, stats.SignFlip(d).P)
			}
		}
	}
	adjusted := holm(raw)
	for i, t := range tests {
		p := adjusted[i]
		t.c.e.PVsLeader = &p
		n := len(t.d)
		switch {
		case t.res.NNonzero == 0:
			t.c.e.TierReason = fmt.Sprintf("same success as the leader on all %d common tasks", n)
		case stats.MinPossibleP(t.res.NNonzero) > alpha && t.res.NNonzero == n:
			t.c.e.TierReason = fmt.Sprintf("too few common tasks (%d) to separate", n)
		case stats.MinPossibleP(t.res.NNonzero) > alpha:
			t.c.e.TierReason = fmt.Sprintf("too few differing tasks (%d of %d common) to separate", t.res.NNonzero, n)
		case p >= alpha:
			t.c.e.TierReason = fmt.Sprintf("p %s on %d common tasks", formatP(p), n)
		case t.mean < 0:
			t.c.e.TierReason = fmt.Sprintf("p %s on %d common tasks, ahead of the leader on them: the task sets differ", formatP(p), n)
		default:
			t.c.e.Tier = TierBehind
			t.c.e.TierReason = fmt.Sprintf("p %s on %d common tasks: behind the leader", formatP(p), n)
		}
	}
}

// compareEntries is the board order.
func compareEntries(x, y *entryCalc) int {
	xs, xok := x.e.Success.Float()
	ys, yok := y.e.Success.Float()
	xc, xcok := x.e.Metrics[MetricCostPerSuccess].Float()
	yc, ycok := y.e.Metrics[MetricCostPerSuccess].Float()
	c := compareTrueFirst(xok, yok)
	if c == 0 && xok {
		c = cmp.Compare(ys, xs)
	}
	if c == 0 {
		c = compareTrueFirst(xcok, ycok)
	}
	if c == 0 && xcok {
		c = cmp.Compare(xc, yc)
	}
	return cmp.Or(c, cmp.Compare(y.e.Runs, x.e.Runs), cmp.Compare(x.e.ConfigHash, y.e.ConfigHash))
}

// holm returns Holm's step-down adjustment of p, in input order: the i-th
// smallest p is multiplied by (m - i + 1), capped at 1, and made monotone so
// a smaller raw p never gets a larger adjusted one. A NaN p is taken as 1.
func holm(p []float64) []float64 {
	m := len(p)
	order := make([]int, m)
	clean := make([]float64, m)
	for i, v := range p {
		order[i] = i
		clean[i] = v
		if math.IsNaN(v) {
			clean[i] = 1
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(clean[a], clean[b]) })
	out := make([]float64, m)
	running := 0.0
	for rank, i := range order {
		running = math.Max(running, math.Min(1, float64(m-rank)*clean[i]))
		out[i] = running
	}
	return out
}

// formatP writes a p-value with two significant digits below 0.1, so a
// value near alpha keeps its second digit, and two decimals above.
func formatP(p float64) string {
	if p < 0.1 {
		return strconv.FormatFloat(p, 'g', 2, 64)
	}
	return strconv.FormatFloat(p, 'f', 2, 64)
}

// sortedKeys returns a map's keys in order.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// toEntries copies the ranked entries out.
func toEntries(calcs []*entryCalc) []schemas.LeaderboardEntry {
	out := make([]schemas.LeaderboardEntry, 0, len(calcs))
	for _, c := range calcs {
		out = append(out, c.e)
	}
	return out
}
