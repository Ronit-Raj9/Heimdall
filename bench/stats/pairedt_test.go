package stats

import (
	"math"
	"testing"
)

func TestPairedT(t *testing.T) {
	worked := diffs([]int{3, 3, 0, 2, 0, 2, 1, 3}, []int{3, 3, 1, 3, 0, 3, 2, 3}, 3)
	tests := []struct {
		name   string
		d      []float64
		conf   float64
		lo, hi float64
		tol    float64
		ok     bool
	}{
		{"design 9.3 worked example", worked, 0.95, 0.0177, 0.3156, 5e-5, true},
		// mean 3, sd sqrt(2.5), t(0.975, 4) = 2.776445.
		{"one to five", []float64{1, 2, 3, 4, 5}, 0.95, 3 - 2.776445*math.Sqrt(0.5), 3 + 2.776445*math.Sqrt(0.5), 1e-6, true},
		{"zero spread", []float64{0.25, 0.25, 0.25}, 0.95, 0.25, 0.25, 0, true},
		{"all zero", []float64{0, 0, 0, 0}, 0.95, 0, 0, 0, true},
		{"empty", nil, 0.95, math.NaN(), math.NaN(), 0, false},
		{"one task", []float64{0.5}, 0.95, math.NaN(), math.NaN(), 0, false},
		{"conf zero", worked, 0, math.NaN(), math.NaN(), 0, false},
		{"conf one", worked, 1, math.NaN(), math.NaN(), 0, false},
		{"nan difference", []float64{0.1, math.NaN()}, 0.95, math.NaN(), math.NaN(), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi, ok := PairedT(tc.d, tc.conf)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !sameFloat(lo, tc.lo, tc.tol) || !sameFloat(hi, tc.hi, tc.tol) {
				t.Errorf("interval = [%v, %v], want [%v, %v]", lo, hi, tc.lo, tc.hi)
			}
		})
	}
}
