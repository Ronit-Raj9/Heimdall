package reportcard

import (
	"cmp"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

// BootstrapResamples is how many task resamples the score interval uses.
const BootstrapResamples = 2000

// cluster is one task's counted runs: successes out of runs. A capability
// rollup keys clusters by benchmark and task, so the same task id in two
// benchmarks stays two clusters.
type cluster struct {
	key        string
	succ, runs int
}

// clustersOf groups runs by task, in task order.
func clustersOf(runs []graded, prefix string) []cluster {
	by := map[string]*cluster{}
	for _, r := range runs {
		k := prefix + r.task
		c := by[k]
		if c == nil {
			c = &cluster{key: k}
			by[k] = c
		}
		c.runs++
		c.succ += r.success
	}
	out := make([]cluster, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b cluster) int { return cmp.Compare(a.key, b.key) })
	return out
}

// taskCI is the Conf interval of the success rate (successes over runs) by a
// task-clustered percentile bootstrap: tasks are resampled with replacement,
// each keeping all its runs, so repeated trials of one task never count as
// independent evidence. The seed is fixed, so a report is reproducible. With
// fewer than two tasks there is no between-task spread to resample and the
// exact Clopper-Pearson interval over runs is used.
func taskCI(cs []cluster) (lo, hi float64) {
	succ, runs := 0, 0
	for _, c := range cs {
		succ += c.succ
		runs += c.runs
	}
	if runs == 0 {
		return 0, 0
	}
	if len(cs) < 2 {
		return stats.ClopperPearson(succ, runs, Conf)
	}
	rng := rand.New(rand.NewPCG(0x5eed, uint64(len(cs))<<32|uint64(runs)))
	est := make([]float64, BootstrapResamples)
	for b := range est {
		s, n := 0, 0
		for range cs {
			c := cs[rng.IntN(len(cs))]
			s += c.succ
			n += c.runs
		}
		est[b] = float64(s) / float64(n)
	}
	slices.Sort(est)
	alpha := (1 - Conf) / 2
	return quantile(est, alpha), quantile(est, 1-alpha)
}

// quantile is the type-7 quantile of sorted xs.
func quantile(xs []float64, q float64) float64 {
	pos := q * float64(len(xs)-1)
	i := int(math.Floor(pos))
	if i+1 >= len(xs) {
		return xs[len(xs)-1]
	}
	f := pos - float64(i)
	return xs[i] + f*(xs[i+1]-xs[i])
}

// TaskTally is one task's verified successes over its counted runs. Key
// names the task; callers pooling benchmarks prefix it with the benchmark so
// the same task id in two benchmarks stays two tasks.
type TaskTally struct {
	Key        string
	Succ, Runs int
}

// TaskInterval is the report card's score interval over tallies: the
// task-clustered percentile bootstrap at level Conf (Clopper-Pearson over
// runs with fewer than two tasks). The agent leaderboard uses it so both
// pages show the same interval for the same runs.
func TaskInterval(ts []TaskTally) (lo, hi float64) {
	cs := make([]cluster, 0, len(ts))
	for _, t := range ts {
		cs = append(cs, cluster{key: t.Key, succ: t.Succ, runs: t.Runs})
	}
	slices.SortFunc(cs, func(a, b cluster) int { return cmp.Compare(a.key, b.key) })
	return taskCI(cs)
}
