// Package verdictab decides A/B verdicts for evaluation report cards
// (agent-first contract section 4).
//
// This is a temporary stand-in written by lane A04 so the report card can
// call it before lane A05 lands the real package. A05 owns this package and
// replaces this file; the report card only uses Compare and CompareOverall.
package verdictab

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

// Alpha is the significance level of a benchmark verdict.
const Alpha = 0.05

// Tie is the winner of a comparison with no significant difference.
const Tie = "tie"

// Tally is the successes and graded runs of one arm on one task.
type Tally struct {
	Successes int `json:"successes"`
	Runs      int `json:"runs"`
}

// Arm is one arm's graded runs on one benchmark, by task.
type Arm struct {
	Label   string           `json:"label"`
	PerTask map[string]Tally `json:"per_task"`
}

// Verdict is the A/B verdict on one benchmark. Delta is the second arm's
// score minus the first arm's.
type Verdict struct {
	Winner      string  `json:"winner"` // an arm label or "tie"
	Delta       float64 `json:"delta"`
	Significant bool    `json:"significant"`
	Summary     string  `json:"summary"`
}

// Overall is the verdict across every benchmark.
type Overall struct {
	Winner  string         `json:"winner"`
	Wins    map[string]int `json:"wins"`
	Summary string         `json:"summary"`
}

// Compare compares a and b on the tasks both ran: the exact sign-flip test
// on per-task success rates. Not significant at Alpha is a tie.
func Compare(a, b Arm) Verdict {
	var d []float64
	sa, na, sb, nb := 0, 0, 0, 0
	for task, ta := range a.PerTask {
		tb, ok := b.PerTask[task]
		if !ok || ta.Runs == 0 || tb.Runs == 0 {
			continue
		}
		sa, na, sb, nb = sa+ta.Successes, na+ta.Runs, sb+tb.Successes, nb+tb.Runs
		d = append(d, float64(tb.Successes)/float64(tb.Runs)-float64(ta.Successes)/float64(ta.Runs))
	}
	if na == 0 || nb == 0 {
		return Verdict{Winner: Tie, Summary: "no tasks both arms ran"}
	}
	delta := float64(sb)/float64(nb) - float64(sa)/float64(na)
	res := stats.SignFlip(d)
	v := Verdict{Winner: Tie, Delta: round(delta), Significant: res.P < Alpha && delta != 0}
	pts := int(math.Round(math.Abs(delta) * 100))
	switch {
	case v.Significant && delta > 0:
		v.Winner, v.Summary = b.Label, fmt.Sprintf("%s solves %d points more", b.Label, pts)
	case v.Significant:
		v.Winner, v.Summary = a.Label, fmt.Sprintf("%s solves %d points more", a.Label, pts)
	default:
		v.Summary = fmt.Sprintf("no clear difference (%d points, p=%.2f)", pts, res.P)
	}
	return v
}

// CompareOverall counts the benchmark verdicts. The arm with more wins is
// the overall winner; equal wins is a tie.
func CompareOverall(labelA, labelB string, vs []Verdict) Overall {
	o := Overall{Winner: Tie, Wins: map[string]int{labelA: 0, labelB: 0, Tie: 0}}
	for _, v := range vs {
		o.Wins[v.Winner]++
	}
	wa, wb := o.Wins[labelA], o.Wins[labelB]
	labels := []string{labelA, labelB}
	slices.SortFunc(labels, func(x, y string) int { return cmp.Compare(o.Wins[y], o.Wins[x]) })
	best, other := labels[0], labels[1]
	if wa != wb {
		o.Winner = best
		o.Summary = fmt.Sprintf("%s is better on %d of %d benchmarks, worse on %d", best, o.Wins[best], len(vs), o.Wins[other])
	} else {
		o.Summary = fmt.Sprintf("no overall winner: %d wins each over %d benchmarks", wa, len(vs))
	}
	return o
}

func round(x float64) float64 { return math.Round(x*1e4) / 1e4 }
