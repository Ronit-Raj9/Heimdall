package stats

import "math"

// PairedT returns the paired-t confidence interval at level conf for the mean
// of per-task differences d: d_bar +/- t(1-(1-conf)/2, n-1) * s_d / sqrt(n).
// DESIGN.md 9.3 makes this descriptive only and labelled approximate; it never
// enters the decision, which belongs to SignFlip. When s_d is 0 the interval
// is exactly [d_bar, d_bar]. ok is false, with NaN bounds, when n < 2, conf is
// outside (0, 1), or any difference is NaN or infinite.
func PairedT(d []float64, conf float64) (lo, hi float64, ok bool) {
	n := len(d)
	if n < 2 || !(conf > 0 && conf < 1) {
		return math.NaN(), math.NaN(), false
	}
	for _, v := range d {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return math.NaN(), math.NaN(), false
		}
	}
	mean := Mean(d)
	sd := SD(d)
	if sd == 0 {
		return mean, mean, true
	}
	half := TQuantile(1-(1-conf)/2, n-1) * sd / math.Sqrt(float64(n))
	return mean - half, mean + half, true
}
