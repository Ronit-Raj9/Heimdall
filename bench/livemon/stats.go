package livemon

import (
	"math"
	"slices"
)

// FisherLess is the one-sided Fisher exact p that the first group's rate
// (x1 of n1) is below the second's (x2 of n2): the hypergeometric chance of
// x1 or fewer successes in the first group given the totals.
func FisherLess(x1, n1, x2, n2 int) float64 {
	if n1 <= 0 || n2 <= 0 {
		return 1
	}
	k := x1 + x2
	n := n1 + n2
	lo := max(0, k-n2)
	p := 0.0
	for x := lo; x <= x1; x++ {
		p += math.Exp(logChoose(n1, x) + logChoose(n2, k-x) - logChoose(n, k))
	}
	return math.Min(1, p)
}

// BinomialLess is the one-sided p that the true rate is below p0 given x
// successes in n trials: P(X <= x) for X ~ Binomial(n, p0).
func BinomialLess(x, n int, p0 float64) float64 {
	if n <= 0 {
		return 1
	}
	if p0 <= 0 {
		return 1
	}
	if p0 >= 1 {
		if x < n {
			return 0
		}
		return 1
	}
	p := 0.0
	for i := 0; i <= x; i++ {
		p += math.Exp(logChoose(n, i) + float64(i)*math.Log(p0) + float64(n-i)*math.Log1p(-p0))
	}
	return math.Min(1, p)
}

// MannWhitneyGreater is the one-sided p that values in a tend to be larger
// than values in b (Mann-Whitney U, normal approximation with tie and
// continuity corrections). It returns 1 when either side is empty.
func MannWhitneyGreater(a, b []float64) float64 {
	n1, n2 := len(a), len(b)
	if n1 == 0 || n2 == 0 {
		return 1
	}
	type obs struct {
		v float64
		g int
	}
	all := make([]obs, 0, n1+n2)
	for _, v := range a {
		all = append(all, obs{v, 0})
	}
	for _, v := range b {
		all = append(all, obs{v, 1})
	}
	slices.SortFunc(all, func(x, y obs) int {
		switch {
		case x.v < y.v:
			return -1
		case x.v > y.v:
			return 1
		}
		return 0
	})
	n := float64(n1 + n2)
	rankA, ties := 0.0, 0.0
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		rank := float64(i+j+1) / 2 // mean of ranks i+1..j
		t := float64(j - i)
		ties += t*t*t - t
		for k := i; k < j; k++ {
			if all[k].g == 0 {
				rankA += rank
			}
		}
		i = j
	}
	u := rankA - float64(n1*(n1+1))/2
	mean := float64(n1*n2) / 2
	variance := float64(n1*n2) / 12 * ((n + 1) - ties/(n*(n-1)))
	if variance <= 0 {
		return 1
	}
	z := (u - mean - 0.5) / math.Sqrt(variance)
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

// Median is the median of v (0 when empty).
func Median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// Wilson is the 95% Wilson score interval of x successes in n trials.
func Wilson(x, n int) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	const z = 1.959964
	p := float64(x) / float64(n)
	nf := float64(n)
	d := 1 + z*z/nf
	c := p + z*z/(2*nf)
	h := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf))
	return math.Max(0, (c-h)/d), math.Min(1, (c+h)/d)
}

func logChoose(n, k int) float64 {
	if k < 0 || k > n {
		return math.Inf(-1)
	}
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}
