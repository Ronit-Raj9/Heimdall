package stats

import (
	"math"
	"testing"
)

func TestBetaInc(t *testing.T) {
	tests := []struct {
		name    string
		a, b, x float64
		want    float64
		tol     float64
	}{
		{"below range", 2, 3, -0.1, 0, 1e-13},
		{"above range", 2, 3, 1.5, 1, 1e-13},
		{"uniform", 1, 1, 0.37, 0.37, 1e-13},
		{"power a", 3.5, 1, 0.6, math.Pow(0.6, 3.5), 1e-13},
		{"power b", 1, 4, 0.2, 1 - math.Pow(0.8, 4), 1e-13},
		{"symmetric half", 7.5, 7.5, 0.5, 0.5, 1e-13},
		// lgamma(1000) is about 5900, so its rounding error is about 1e-12.
		{"symmetric half large", 500, 500, 0.5, 0.5, 1e-11},
		// Beta(2,3) CDF = 6x^2 - 8x^3 + 3x^4.
		{"beta 2 3", 2, 3, 0.4, 6*0.16 - 8*0.064 + 3*0.0256, 1e-13},
		// Cauchy: I_x(1/2,1/2) = (2/pi) asin(sqrt(x)).
		{"arcsine", 0.5, 0.5, 0.3, 2 / math.Pi * math.Asin(math.Sqrt(0.3)), 1e-13},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := betaInc(tc.a, tc.b, tc.x); math.Abs(got-tc.want) > tc.tol {
				t.Errorf("betaInc(%v, %v, %v) = %v, want %v", tc.a, tc.b, tc.x, got, tc.want)
			}
		})
	}
}

func TestBetaQuantileRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		p, a, b float64
	}{
		{"small", 0.05, 1, 10},
		{"middle", 0.5, 3, 7},
		{"high", 0.975, 4, 7},
		{"skewed", 0.999, 0.5, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			x := betaQuantile(tc.p, tc.a, tc.b)
			if got := betaInc(tc.a, tc.b, x); math.Abs(got-tc.p) > 1e-12 {
				t.Errorf("betaInc(betaQuantile(%v)) = %v", tc.p, got)
			}
		})
	}
}

func TestClopperPearsonUpper(t *testing.T) {
	tests := []struct {
		name string
		x, n int
		conf float64
		want float64
		tol  float64
	}{
		{"zero of ten one-sided 95", 0, 10, 0.95, 1 - math.Pow(0.05, 0.1), 1e-12},
		{"zero of ten two-sided 95", 0, 10, 0.975, 1 - math.Pow(0.025, 0.1), 1e-12},
		{"three of ten two-sided 95", 3, 10, 0.975, 0.6525, 5e-5},
		{"zero of hundred near rule of three", 0, 100, 0.95, 1 - math.Pow(0.05, 0.01), 1e-12},
		{"all pass", 10, 10, 0.95, 1, 0},
		{"no trials", 0, 0, 0.95, math.NaN(), 0},
		{"x above n", 11, 10, 0.95, math.NaN(), 0},
		{"negative x", -1, 10, 0.95, math.NaN(), 0},
		{"conf zero", 1, 10, 0, math.NaN(), 0},
		{"conf one", 1, 10, 1, math.NaN(), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClopperPearsonUpper(tc.x, tc.n, tc.conf)
			if !sameFloat(got, tc.want, tc.tol) {
				t.Errorf("ClopperPearsonUpper(%d, %d, %v) = %v, want %v", tc.x, tc.n, tc.conf, got, tc.want)
			}
		})
	}
}
