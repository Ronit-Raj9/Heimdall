package livemon

import (
	"math"
	"testing"
)

func TestFisherLess(t *testing.T) {
	tests := []struct {
		name           string
		x1, n1, x2, n2 int
		lo, hi         float64
	}{
		{"same rate", 5, 10, 50, 100, 0.4, 1},
		{"clear drop", 2, 10, 90, 100, 0, 0.001},
		{"small drop", 7, 10, 85, 100, 0.05, 0.3},
		{"empty", 0, 0, 5, 10, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := FisherLess(tc.x1, tc.n1, tc.x2, tc.n2)
			if p < tc.lo || p > tc.hi {
				t.Fatalf("p = %v, want in [%v, %v]", p, tc.lo, tc.hi)
			}
		})
	}
	// Known value: table [[1,9],[11,3]] one-sided p = 0.001346.
	if p := FisherLess(1, 10, 11, 14); math.Abs(p-0.001346) > 1e-4 {
		t.Fatalf("fisher known value = %v", p)
	}
}

func TestBinomialLess(t *testing.T) {
	if p := BinomialLess(0, 10, 0.5); math.Abs(p-1.0/1024) > 1e-9 {
		t.Fatalf("p = %v", p)
	}
	if p := BinomialLess(10, 10, 0.5); math.Abs(p-1) > 1e-9 {
		t.Fatalf("p = %v", p)
	}
	if p := BinomialLess(3, 10, 0); p != 1 {
		t.Fatalf("p0 0: %v", p)
	}
}

func TestMannWhitneyGreater(t *testing.T) {
	a := []float64{900, 950, 1000, 1100, 1200, 1300}
	b := []float64{100, 120, 110, 130, 140, 150, 160, 105, 115, 125}
	if p := MannWhitneyGreater(a, b); p > 0.01 {
		t.Fatalf("clearly greater: p = %v", p)
	}
	if p := MannWhitneyGreater(b, a); p < 0.9 {
		t.Fatalf("clearly smaller: p = %v", p)
	}
	same := []float64{5, 5, 5, 5}
	if p := MannWhitneyGreater(same, same); p != 1 && p < 0.4 {
		t.Fatalf("ties: p = %v", p)
	}
	if p := MannWhitneyGreater(nil, b); p != 1 {
		t.Fatalf("empty: %v", p)
	}
}

func TestMedianWilson(t *testing.T) {
	if m := Median([]float64{3, 1, 2}); m != 2 {
		t.Fatal(m)
	}
	if m := Median([]float64{4, 1, 2, 3}); m != 2.5 {
		t.Fatal(m)
	}
	lo, hi := Wilson(5, 10)
	if lo > 0.25 || hi < 0.75 || lo < 0.2 || hi > 0.8 {
		t.Fatalf("wilson = %v %v", lo, hi)
	}
	if lo, hi := Wilson(0, 0); lo != 0 || hi != 1 {
		t.Fatal(lo, hi)
	}
}
