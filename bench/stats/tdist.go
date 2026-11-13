package stats

import "math"

const tQuantileTol = 1e-12 // bisection width, relative to max(1, t)

// TQuantile returns the p quantile of Student's t distribution with df
// degrees of freedom (the inverse CDF). It computes the t tail through the
// regularized incomplete beta function, P(T > t) = I_x(df/2, 1/2) / 2 with
// x = df/(df+t^2), and inverts it by bisection to 1e-12, so no tables are
// involved. It works on the smaller tail so a p near 1 keeps its precision
// where it can. p <= 0 gives -Inf, p >= 1 gives +Inf, df < 1 or NaN p gives
// NaN.
func TQuantile(p float64, df int) float64 {
	switch {
	case df < 1 || math.IsNaN(p):
		return math.NaN()
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p == 0.5:
		return 0
	}
	q, sign := p, -1.0
	if p > 0.5 {
		q, sign = 1-p, 1.0
	}
	nu := float64(df)
	lo, hi := 0.0, 1.0
	for tTail(hi, nu) > q {
		lo, hi = hi, hi*2
	}
	for range 400 {
		if hi-lo <= tQuantileTol*math.Max(1, lo) {
			break
		}
		mid := lo + (hi-lo)/2
		if tTail(mid, nu) > q {
			lo = mid
		} else {
			hi = mid
		}
	}
	return sign * (lo + (hi-lo)/2)
}

// tTail returns P(T > t) for t >= 0 and nu degrees of freedom. An infinite
// t makes x zero and the tail zero, which also ends TQuantile's bracketing.
func tTail(t, nu float64) float64 {
	return 0.5 * betaInc(nu/2, 0.5, nu/(nu+t*t))
}
