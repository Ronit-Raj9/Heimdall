package stats

import "math"

// MDE returns the minimal detectable effect for n tasks, as a fraction (the
// caller multiplies by 100 for percentage points): (t(1-alpha/2, n-1) +
// t(power, n-1)) * sd / sqrt(n) (DESIGN.md 9.3 rule 7). With sd = 0.30,
// alpha = 0.05 and power = 0.8 it is 0.346 at n = 8. n < 2 returns +Inf,
// since no effect is detectable. alpha or power outside (0, 1), or a negative
// or NaN sd, returns NaN.
func MDE(n int, sd, alpha, power float64) float64 {
	if n < 2 {
		return math.Inf(1)
	}
	if !(alpha > 0 && alpha < 1) || !(power > 0 && power < 1) || !(sd >= 0) {
		return math.NaN()
	}
	df := n - 1
	return (TQuantile(1-alpha/2, df) + TQuantile(power, df)) * sd / math.Sqrt(float64(n))
}

// TasksNeeded returns the smallest task count n in [minTasks, maxTasks] with
// MDE(n) <= |target|, the size a new experiment needs to detect the observed
// effect (DESIGN.md 9.4, "no decision"). It returns 0 when no n in range is
// enough or the input is invalid. For power >= 0.5 both t quantiles are
// non-negative and fall with n, so MDE falls with n and a binary search is
// exact; below that the sum can rise, so it scans.
func TasksNeeded(target, sd, alpha, power float64, minTasks, maxTasks int) int {
	goal := math.Abs(target)
	lo := max(minTasks, 2)
	if lo > maxTasks || math.IsNaN(goal) {
		return 0
	}
	fits := func(n int) bool { return MDE(n, sd, alpha, power) <= goal }
	if !(power >= 0.5) {
		for n := lo; n <= maxTasks; n++ {
			if fits(n) {
				return n
			}
		}
		return 0
	}
	hi := maxTasks
	if !fits(hi) {
		return 0
	}
	for lo < hi {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}
