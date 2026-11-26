package stats

import (
	"math"
	"math/rand/v2"
	"testing"
)

// bruteUpper enumerates every sign vector of d directly (no split) and counts
// sums at least as large as the observed one, with the same tie tolerance.
func bruteUpper(d []float64) float64 {
	var nz []float64
	obs, scale := 0.0, 0.0
	for _, v := range d {
		if v != 0 {
			nz = append(nz, v)
			obs += v
			scale += math.Abs(v)
		}
	}
	if len(nz) == 0 {
		return 1
	}
	hits := 0
	for mask := 0; mask < 1<<len(nz); mask++ {
		s := 0.0
		for i, v := range nz {
			if mask&(1<<i) != 0 {
				s -= v
			} else {
				s += v
			}
		}
		if s >= obs-tieRelTol*scale {
			hits++
		}
	}
	return float64(hits) / float64(int(1)<<len(nz))
}

func TestSignFlipUpperExactMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for n := 1; n <= 12; n++ {
		for rep := 0; rep < 20; rep++ {
			d := make([]float64, n)
			for i := range d {
				// Thirds give many exact ties between sums.
				d[i] = float64(r.IntN(7)-3) / 3
			}
			got := SignFlipUpper(d).P
			want := bruteUpper(d)
			if math.Abs(got-want) > 1e-12 {
				t.Fatalf("n=%d d=%v: got %v want %v", n, d, got, want)
			}
		}
	}
}

func TestSignFlipUpperTiesCount(t *testing.T) {
	// Two equal positive values: sums 2, 0, 0, -2; observed 2 ties with itself only.
	if p := SignFlipUpper([]float64{1, 1}).P; math.Abs(p-0.25) > 1e-12 {
		t.Fatalf("p = %v, want 0.25", p)
	}
	// {1, -1}: observed 0; sums 0, 2, -2, 0, so three of four are at least 0.
	if p := SignFlipUpper([]float64{1, -1}).P; math.Abs(p-0.75) > 1e-12 {
		t.Fatalf("p = %v, want 0.75", p)
	}
}

func TestSignFlipUpperZerosDropped(t *testing.T) {
	a := SignFlipUpper([]float64{0.5, 1, 0.25})
	b := SignFlipUpper([]float64{0, 0.5, 0, 1, 0.25, 0})
	if a.P != b.P || b.NNonzero != 3 {
		t.Fatalf("zeros changed the result: %+v vs %+v", a, b)
	}
	if want := 0.125; math.Abs(a.P-want) > 1e-12 {
		t.Fatalf("p = %v, want %v", a.P, want)
	}
}

func TestSignFlipUpperAllZeroAndEmpty(t *testing.T) {
	if p := SignFlipUpper(nil).P; p != 1 {
		t.Fatalf("empty p = %v", p)
	}
	if p := SignFlipUpper([]float64{0, 0, 0}).P; p != 1 {
		t.Fatalf("all zero p = %v", p)
	}
}

func TestSignFlipUpperNaN(t *testing.T) {
	if p := SignFlipUpper([]float64{1, math.NaN()}).P; !math.IsNaN(p) {
		t.Fatalf("NaN p = %v", p)
	}
	if p := SignFlipUpper([]float64{1, math.Inf(1)}).P; !math.IsNaN(p) {
		t.Fatalf("Inf p = %v", p)
	}
}

func TestSignFlipUpperNegativeObservedComputed(t *testing.T) {
	// Every value negative: the observed sum is the smallest possible, so every
	// sign vector is at least as large and p is 1. The result is still computed.
	if p := SignFlipUpper([]float64{-1, -1, -1}).P; math.Abs(p-1) > 1e-12 {
		t.Fatalf("p = %v, want 1", p)
	}
	if p := SignFlipUpper([]float64{-1, 1, 1}).P; math.Abs(p-0.5) > 1e-12 {
		t.Fatalf("p = %v, want 0.5", p)
	}
}

func TestSignFlipUpperMonteCarloDeterministic(t *testing.T) {
	d := make([]float64, ExactMaxNonzero+8)
	for i := range d {
		d[i] = float64(i%5) - 1.5
	}
	a := SignFlipUpper(d)
	b := SignFlipUpper(d)
	if a.Exact || a.Draws != UpperDraws || a.P != b.P {
		t.Fatalf("Monte Carlo path not deterministic: %+v %+v", a, b)
	}
	x := SignFlipUpperDraws(d, 4000)
	y := SignFlipUpperDraws(d, 4000)
	if x != y || x <= 0 || x > 1 {
		t.Fatalf("draws hook: %v %v", x, y)
	}
	// The small Monte Carlo agrees with the large one roughly.
	if math.Abs(x-a.P) > 0.05 {
		t.Fatalf("draws hook %v far from %v", x, a.P)
	}
}

func TestMinPossiblePUpper(t *testing.T) {
	if got := MinPossiblePUpper(0); got != 1 {
		t.Fatalf("0: %v", got)
	}
	if got := MinPossiblePUpper(5); got != 1.0/32 {
		t.Fatalf("5: %v", got)
	}
	if got := SignFlipUpper([]float64{1, 1, 1, 1, 1}).P; got != MinPossiblePUpper(5) {
		t.Fatalf("all positive p %v", got)
	}
}
