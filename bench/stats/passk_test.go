package stats

import (
	"math"
	"testing"
)

func TestPassHatK(t *testing.T) {
	tests := []struct {
		name    string
		c, n, k int
		want    float64
		ok      bool
	}{
		{"2 of 3, k=2", 2, 3, 2, 1.0 / 3, true},
		{"3 of 3, k=2", 3, 3, 2, 1, true},
		{"1 of 1, k=2", 1, 1, 2, math.NaN(), false},
		{"not the plug-in", 2, 4, 2, 1.0 / 6, true}, // plug-in (1/2)^2 = 0.25
		{"fewer passes than k", 1, 5, 2, 0, true},
		{"k=1 is the rate", 3, 5, 1, 0.6, true},
		{"k=0", 2, 5, 0, 1, true},
		{"c above n", 4, 3, 2, math.NaN(), false},
		{"negative c", -1, 3, 2, math.NaN(), false},
		{"negative k", 1, 3, -1, math.NaN(), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PassHatK(tc.c, tc.n, tc.k)
			if ok != tc.ok || !sameFloat(got, tc.want, 1e-15) {
				t.Errorf("PassHatK(%d, %d, %d) = %v, %v; want %v, %v", tc.c, tc.n, tc.k, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOutcomeConsistency(t *testing.T) {
	tests := []struct {
		name      string
		successes []int
		trials    []int
		want      float64
	}{
		{"all agree", []int{3, 0, 3}, []int{3, 3, 3}, 1},
		{"even splits", []int{1, 2}, []int{2, 4}, 0},
		// (2*2/3-1)^2 = 1/9, then 1: mean 5/9.
		{"mixed", []int{2, 3}, []int{3, 3}, 5.0 / 9},
		{"skips zero trials", []int{3, 0}, []int{3, 0}, 1},
		{"nothing observed", []int{0}, []int{0}, math.NaN()},
		{"empty", nil, nil, math.NaN()},
		{"length mismatch", []int{1}, []int{1, 2}, math.NaN()},
		{"successes above trials", []int{4}, []int{3}, math.NaN()},
		{"negative trials", []int{0}, []int{-1}, math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := OutcomeConsistency(tc.successes, tc.trials)
			if !sameFloat(got, tc.want, 1e-15) {
				t.Errorf("OutcomeConsistency = %v, want %v", got, tc.want)
			}
		})
	}
}
