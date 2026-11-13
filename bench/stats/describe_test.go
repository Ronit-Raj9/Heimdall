package stats

import (
	"math"
	"slices"
	"testing"
)

func TestMean(t *testing.T) {
	tests := []struct {
		name string
		x    []float64
		want float64
	}{
		{"empty", nil, math.NaN()},
		{"one", []float64{2.5}, 2.5},
		{"integers", []float64{1, 2, 3, 4}, 2.5},
		{"constant is exact", []float64{0.1, 0.1, 0.1}, 0.1},
		{"negative", []float64{-1, -3}, -2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Mean(tc.x)
			if !sameFloat(got, tc.want, 1e-15) {
				t.Errorf("Mean = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSD(t *testing.T) {
	tests := []struct {
		name string
		x    []float64
		want float64
	}{
		{"empty", nil, math.NaN()},
		{"one", []float64{1}, math.NaN()},
		{"two", []float64{1, 3}, math.Sqrt2},
		{"one to five", []float64{1, 2, 3, 4, 5}, math.Sqrt(2.5)},
		{"constant is exactly zero", []float64{0.1, 0.1, 0.1}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SD(tc.x)
			if !sameFloat(got, tc.want, 1e-14) {
				t.Errorf("SD = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		name string
		x    []float64
		want float64
	}{
		{"empty", nil, math.NaN()},
		{"one", []float64{7}, 7},
		{"odd unsorted", []float64{5, 1, 3}, 3},
		{"even unsorted", []float64{4, 1, 3, 2}, 2.5},
		{"nan", []float64{1, math.NaN(), 2}, math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := slices.Clone(tc.x)
			got := Median(tc.x)
			if !sameFloat(got, tc.want, 0) {
				t.Errorf("Median = %v, want %v", got, tc.want)
			}
			if !slices.EqualFunc(in, tc.x, func(a, b float64) bool { return sameFloat(a, b, 0) }) {
				t.Errorf("Median reordered its input: %v became %v", in, tc.x)
			}
		})
	}
}

// sameFloat reports whether got and want agree within tol, treating two
// NaNs as equal.
func sameFloat(got, want, tol float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	if math.IsInf(want, 0) {
		return got == want
	}
	return math.Abs(got-want) <= tol
}
