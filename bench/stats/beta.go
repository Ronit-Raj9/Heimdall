package stats

import "math"

const (
	betaCFEps   = 1e-15  // relative change at which the continued fraction has converged
	betaCFFloor = 1e-300 // keeps Lentz's denominators away from zero
	betaQTol    = 1e-15  // bisection width for the beta quantile
)

// ClopperPearsonUpper returns the exact one-sided upper confidence bound at
// level conf for a binomial rate after x successes in n trials: the p at
// which seeing x or fewer successes has probability 1-conf. That is the
// conf quantile of Beta(x+1, n-x), and 1 when x == n. For the upper end of a
// two-sided interval at level c, pass conf = 1-(1-c)/2. For x = 0 and
// conf = 0.95 it is close to the rule of three, 3/n. Invalid input (n < 1,
// x outside [0, n], conf outside (0, 1)) returns NaN.
func ClopperPearsonUpper(x, n int, conf float64) float64 {
	if n < 1 || x < 0 || x > n || !(conf > 0 && conf < 1) {
		return math.NaN()
	}
	if x == n {
		return 1
	}
	return betaQuantile(conf, float64(x+1), float64(n-x))
}

// ClopperPearson returns the exact two-sided Clopper-Pearson interval at
// level conf for a binomial rate after x successes in n trials: the
// (1-conf)/2 quantile of Beta(x, n-x+1) and the 1-(1-conf)/2 quantile of
// Beta(x+1, n-x), with lo = 0 when x == 0 and hi = 1 when x == n. Invalid
// input (n < 1, x outside [0, n], conf outside (0, 1)) returns NaN, NaN.
// Eval integrity area (S2): monitor certification.
func ClopperPearson(x, n int, conf float64) (lo, hi float64) {
	if n < 1 || x < 0 || x > n || !(conf > 0 && conf < 1) {
		return math.NaN(), math.NaN()
	}
	tail := (1 - conf) / 2
	if x > 0 {
		lo = betaQuantile(tail, float64(x), float64(n-x+1))
	}
	hi = 1
	if x < n {
		hi = betaQuantile(1-tail, float64(x+1), float64(n-x))
	}
	return lo, hi
}

// betaQuantile inverts the regularized incomplete beta function in x by
// bisection. The CDF is monotone on [0, 1], so bisection cannot fail.
func betaQuantile(p, a, b float64) float64 {
	lo, hi := 0.0, 1.0
	for range 200 {
		if hi-lo <= betaQTol {
			break
		}
		mid := lo + (hi-lo)/2
		if betaInc(a, b, mid) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + (hi-lo)/2
}

// betaInc is the regularized incomplete beta function I_x(a, b) for a, b > 0,
// as in Numerical Recipes betai. The continued fraction converges fast only
// for x < (a+1)/(a+b+2), so above that it uses I_x(a, b) = 1 - I_{1-x}(b, a).
func betaInc(a, b, x float64) float64 {
	switch {
	case math.IsNaN(x):
		return math.NaN()
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}
	front := math.Exp(lnGamma(a+b) - lnGamma(a) - lnGamma(b) + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaCF(a, b, x) / a
	}
	return 1 - front*betaCF(b, a, 1-x)/b
}

// betaCF evaluates the continued fraction for the incomplete beta function
// by the modified Lentz method (Numerical Recipes betacf). It needs on the
// order of sqrt(max(a, b)) iterations, so the cap scales with that.
func betaCF(a, b, x float64) float64 {
	maxIter := 300 + int(10*math.Sqrt(math.Max(a, b)))
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 / awayFromZero(1-qab*x/qap)
	h := d
	for m := 1; m <= maxIter; m++ {
		fm := float64(m)
		m2 := 2 * fm
		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 / awayFromZero(1+aa*d)
		c = awayFromZero(1 + aa/c)
		h *= d * c
		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 / awayFromZero(1+aa*d)
		c = awayFromZero(1 + aa/c)
		step := d * c
		h *= step
		if math.Abs(step-1) < betaCFEps {
			break
		}
	}
	return h
}

// awayFromZero replaces a near-zero Lentz denominator with a tiny one.
func awayFromZero(v float64) float64 {
	if math.Abs(v) < betaCFFloor {
		return betaCFFloor
	}
	return v
}

// lnGamma is log|Gamma(x)|. Callers pass x > 0, where Gamma is positive, so
// the sign math.Lgamma also returns carries no information.
func lnGamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}
