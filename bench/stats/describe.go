// Package stats holds the measurement math behind the release verdict
// (DESIGN.md section 9): the exact sign-flip test on per-task differences, the
// descriptive paired-t interval, the minimal detectable effect, and small
// helpers for pass^k and binomial bounds. It is pure: standard library only,
// no I/O, no logging. Results that cannot be computed are NaN (or +Inf where
// the documented meaning is "never"), never a silent zero.
package stats

import (
	"math"
	"slices"
)

// Mean returns the arithmetic mean of x, or NaN for empty input. It sums
// deviations from x[0] so that a constant input returns that constant
// exactly, which lets SD and PairedT report a zero spread as exactly zero.
func Mean(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	shift := x[0]
	sum := 0.0
	for _, v := range x {
		sum += v - shift
	}
	return shift + sum/float64(len(x))
}

// SD returns the sample standard deviation of x (denominator n-1), or NaN
// when there are fewer than two values.
func SD(x []float64) float64 {
	if len(x) < 2 {
		return math.NaN()
	}
	m := Mean(x)
	ss := 0.0
	for _, v := range x {
		dev := v - m
		ss += dev * dev
	}
	return math.Sqrt(ss / float64(len(x)-1))
}

// Median returns the median of x, averaging the two middle values for an even
// count. It returns NaN for empty input or when any value is NaN, because NaN
// has no place in an ordering. The input is never reordered.
func Median(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	sorted := make([]float64, len(x))
	copy(sorted, x)
	for _, v := range sorted {
		if math.IsNaN(v) {
			return math.NaN()
		}
	}
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return sorted[mid-1] + (sorted[mid]-sorted[mid-1])/2
}
