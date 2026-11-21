package stats

import (
	"math"
	"math/rand/v2"
	"slices"
)

// PassAtK returns the unbiased estimate of pass@k, the chance that at least
// one of k trials drawn without replacement from n passes, given c passes:
// 1 - C(n-c,k)/C(n,k) (Chen et al. 2021). ok is false, with NaN, when n < k
// or the counts are impossible. k = 0 gives 0.
func PassAtK(c, n, k int) (float64, bool) {
	if k < 0 || n < k || c < 0 || c > n {
		return math.NaN(), false
	}
	if k == 0 {
		return 0, true
	}
	if n-c < k {
		return 1, true
	}
	q := 1.0
	for i := range k {
		q *= float64(n-c-i) / float64(n-i)
	}
	return 1 - q, true
}

// CurveBootstrapResamples is how many task resamples PassCurve's bands use.
const CurveBootstrapResamples = 1000

// CurvePoint is pass@k and pass^k at one k: the mean over tasks with at
// least k trials, and the 95% task-cluster bootstrap band of each.
type CurvePoint struct {
	K          int     `json:"k"`
	Tasks      int     `json:"tasks"`
	PassAt     float64 `json:"pass_at_k"`
	PassAtLow  float64 `json:"pass_at_k_low"`
	PassAtHigh float64 `json:"pass_at_k_high"`
	PassHat    float64 `json:"pass_hat_k"`
	PassHatLow float64 `json:"pass_hat_k_low"`
	PassHatHi  float64 `json:"pass_hat_k_high"`
}

// PassCurve returns pass@k and pass^k for k = 1..kmax over tasks with
// successes[i] out of trials[i]. Each point averages the unbiased per-task
// estimates over the tasks with at least k trials; a k no task reaches is
// left out. Bands resample tasks with replacement (each keeping all its
// trials) with a fixed seed, so the same input always gives the same bands.
// Invalid input (length mismatch, impossible counts) returns nil.
func PassCurve(successes, trials []int, kmax int) []CurvePoint {
	if len(successes) != len(trials) || kmax < 1 {
		return nil
	}
	for i, n := range trials {
		if n < 0 || successes[i] < 0 || successes[i] > n {
			return nil
		}
	}
	var out []CurvePoint
	for k := 1; k <= kmax; k++ {
		var at, hat []float64
		for i, n := range trials {
			if n < k {
				continue
			}
			a, _ := PassAtK(successes[i], n, k)
			h, _ := PassHatK(successes[i], n, k)
			at, hat = append(at, a), append(hat, h)
		}
		if len(at) == 0 {
			continue
		}
		p := CurvePoint{K: k, Tasks: len(at), PassAt: Mean(at), PassHat: Mean(hat)}
		p.PassAtLow, p.PassAtHigh, p.PassHatLow, p.PassHatHi = curveBands(at, hat, k)
		out = append(out, p)
	}
	return out
}

func curveBands(at, hat []float64, k int) (aLo, aHi, hLo, hHi float64) {
	n := len(at)
	if n < 2 {
		return at[0], at[0], hat[0], hat[0]
	}
	rng := rand.New(rand.NewPCG(0xc0be, uint64(n)<<8|uint64(k)))
	ea := make([]float64, CurveBootstrapResamples)
	eh := make([]float64, CurveBootstrapResamples)
	for b := range ea {
		sa, sh := 0.0, 0.0
		for range n {
			j := rng.IntN(n)
			sa += at[j]
			sh += hat[j]
		}
		ea[b], eh[b] = sa/float64(n), sh/float64(n)
	}
	slices.Sort(ea)
	slices.Sort(eh)
	return Quantile(ea, 0.025), Quantile(ea, 0.975), Quantile(eh, 0.025), Quantile(eh, 0.975)
}

// Quantile is the type-7 quantile of sorted xs; NaN when xs is empty.
func Quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(sorted)-1)
	i := int(math.Floor(pos))
	if i+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	f := pos - float64(i)
	return sorted[i] + f*(sorted[i+1]-sorted[i])
}

// FlipRate returns the share of tasks with two or more trials whose trials
// disagree (some passed, some failed). NaN when no task has two trials or the
// input is invalid.
func FlipRate(successes, trials []int) float64 {
	if len(successes) != len(trials) {
		return math.NaN()
	}
	flips, tasks := 0, 0
	for i, n := range trials {
		c := successes[i]
		if n < 0 || c < 0 || c > n {
			return math.NaN()
		}
		if n < 2 {
			continue
		}
		tasks++
		if c > 0 && c < n {
			flips++
		}
	}
	if tasks == 0 {
		return math.NaN()
	}
	return float64(flips) / float64(tasks)
}

// BootstrapRatio returns the 95% paired bootstrap interval of
// sum(num)/sum(den), resampling indexes (tasks) with replacement under a
// fixed seed. Resamples with a zero denominator are dropped. ok is false
// when fewer than two pairs exist or no resample has a nonzero denominator.
func BootstrapRatio(num, den []float64, seed uint64) (lo, hi float64, ok bool) {
	n := len(num)
	if n != len(den) || n < 2 {
		return 0, 0, false
	}
	rng := rand.New(rand.NewPCG(seed, uint64(n)))
	est := make([]float64, 0, CurveBootstrapResamples)
	for range CurveBootstrapResamples {
		sn, sd := 0.0, 0.0
		for range n {
			j := rng.IntN(n)
			sn += num[j]
			sd += den[j]
		}
		if sd != 0 {
			est = append(est, sn/sd)
		}
	}
	if len(est) == 0 {
		return 0, 0, false
	}
	slices.Sort(est)
	return Quantile(est, 0.025), Quantile(est, 0.975), true
}
