package stats

import (
	"math"
	"math/rand/v2"
	"slices"
)

// Cluster is one task's observed values for a task-clustered interval: the
// sum of its runs' values and how many runs it holds. For a 0/1 metric the
// sum is the task's successes.
type Cluster struct {
	Sum float64
	N   int
}

// ClusterBootstrapCI is the conf interval of sum(Sum) / sum(N) by a
// task-clustered percentile bootstrap: clusters are resampled with
// replacement, each keeping all its runs, so repeated trials of one task
// never count as independent evidence. seed fixes the resamples, so the same
// inputs always give the same interval. It returns ok false with fewer than
// two clusters (no between-task spread to resample) or with no runs.
func ClusterBootstrapCI(cs []Cluster, conf float64, resamples int, seed uint64) (lo, hi float64, ok bool) {
	runs := 0
	for _, c := range cs {
		runs += c.N
	}
	if runs == 0 || len(cs) < 2 || resamples < 1 {
		return 0, 0, false
	}
	rng := rand.New(rand.NewPCG(0x5eed, seed))
	est := make([]float64, 0, resamples)
	for range resamples {
		s, n := 0.0, 0
		for range cs {
			c := cs[rng.IntN(len(cs))]
			s += c.Sum
			n += c.N
		}
		if n == 0 {
			continue
		}
		est = append(est, s/float64(n))
	}
	if len(est) == 0 {
		return 0, 0, false
	}
	slices.Sort(est)
	alpha := (1 - conf) / 2
	return quantile7(est, alpha), quantile7(est, 1-alpha), true
}

// quantile7 is the type-7 quantile of sorted xs.
func quantile7(xs []float64, q float64) float64 {
	pos := q * float64(len(xs)-1)
	i := int(math.Floor(pos))
	if i+1 >= len(xs) {
		return xs[len(xs)-1]
	}
	f := pos - float64(i)
	return xs[i] + f*(xs[i+1]-xs[i])
}
