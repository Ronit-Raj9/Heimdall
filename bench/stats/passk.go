package stats

import "math"

// PassHatK returns the unbiased estimate of pass^k, the chance that k trials
// drawn without replacement from n all pass, given c passes: C(c,k)/C(n,k).
// The plug-in (c/n)^k is biased upward for small n, so it is never used.
// ok is false, with NaN, when n < k or the counts are impossible (k < 0,
// c < 0, c > n). k = 0 gives 1.
func PassHatK(c, n, k int) (float64, bool) {
	if k < 0 || n < k || c < 0 || c > n {
		return math.NaN(), false
	}
	if c < k {
		return 0, true
	}
	p := 1.0
	for i := range k {
		p *= float64(c-i) / float64(n-i)
	}
	return p, true
}

// OutcomeConsistency returns the mean over tasks of (2*c_i/n_i - 1)^2, given
// successes c_i out of trials n_i. A task scores 1 when its trials all agree
// and 0 when they split evenly, so the mean says how repeatable outcomes are
// independent of how often they pass. Tasks with n_i = 0 are skipped. It
// returns NaN when the slices differ in length, a count is impossible, or no
// task has a trial, because nothing was observed.
func OutcomeConsistency(successes, trials []int) float64 {
	if len(successes) != len(trials) {
		return math.NaN()
	}
	sum, tasks := 0.0, 0
	for i, n := range trials {
		c := successes[i]
		if n < 0 || c < 0 || c > n {
			return math.NaN()
		}
		if n == 0 {
			continue
		}
		v := 2*float64(c)/float64(n) - 1
		sum += v * v
		tasks++
	}
	if tasks == 0 {
		return math.NaN()
	}
	return sum / float64(tasks)
}
