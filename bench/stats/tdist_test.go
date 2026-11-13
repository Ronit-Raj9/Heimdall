package stats

import (
	"math"
	"testing"
	"time"
)

func TestTQuantile(t *testing.T) {
	tests := []struct {
		name string
		p    float64
		df   int
		want float64
		tol  float64
	}{
		{"t(0.975, 7)", 0.975, 7, 2.3646, 5e-5},
		{"t(0.975, 1)", 0.975, 1, 12.706, 5e-4},
		{"t(0.8, 7)", 0.8, 7, 0.8960, 5e-5},
		{"t(0.975, 30)", 0.975, 30, 2.0423, 5e-5},
		{"t(0.995, 2)", 0.995, 2, 9.9248, 5e-5},
		{"lower tail is symmetric", 0.025, 7, -2.3646, 5e-5},
		{"median", 0.5, 5, 0, 0},
		{"large df approaches normal", 0.975, 1_000_000, 1.959966, 5e-6},
		{"p zero", 0, 5, math.Inf(-1), 0},
		{"p one", 1, 5, math.Inf(1), 0},
		{"df zero", 0.9, 0, math.NaN(), 0},
		{"p nan", math.NaN(), 5, math.NaN(), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TQuantile(tc.p, tc.df)
			if !sameFloat(got, tc.want, tc.tol) {
				t.Errorf("TQuantile(%v, %d) = %v, want %v", tc.p, tc.df, got, tc.want)
			}
		})
	}
}

// TestTQuantileClosedForms checks the two degrees of freedom with a closed
// form inverse: Cauchy (df=1) and df=2.
func TestTQuantileClosedForms(t *testing.T) {
	for _, p := range []float64{0.001, 0.1, 0.3, 0.6, 0.8, 0.95, 0.975, 0.999, 0.999999} {
		tests := []struct {
			name string
			df   int
			want float64
		}{
			{"cauchy", 1, math.Tan(math.Pi * (p - 0.5))},
			{"df 2", 2, (2*p - 1) / math.Sqrt(2*p*(1-p))},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				got := TQuantile(p, tc.df)
				if math.Abs(got-tc.want) > 1e-10*math.Max(1, math.Abs(tc.want)) {
					t.Errorf("TQuantile(%v, %d) = %.12f, want %.12f", p, tc.df, got, tc.want)
				}
			})
		}
	}
}

func TestTQuantileRoundTrip(t *testing.T) {
	start := time.Now()
	for _, df := range []int{1, 3, 7, 29, 100, 5000} {
		for _, p := range []float64{0.6, 0.8, 0.9, 0.975, 0.995} {
			t.Run("", func(t *testing.T) {
				q := TQuantile(p, df)
				if got := 1 - tTail(q, float64(df)); math.Abs(got-p) > 1e-12 {
					t.Errorf("df=%d: CDF(TQuantile(%v)) = %v", df, p, got)
				}
			})
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("round trip took %v", elapsed)
	}
}
