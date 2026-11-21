package stats

import (
	"math"
	"testing"
)

func TestPassAtK(t *testing.T) {
	cases := []struct {
		c, n, k int
		want    float64
		ok      bool
	}{
		{0, 5, 1, 0, true},
		{5, 5, 3, 1, true},
		{1, 4, 1, 0.25, true},
		{1, 4, 2, 0.5, true}, // 1 - C(3,2)/C(4,2) = 1 - 3/6
		{2, 4, 3, 1, true},   // n-c < k
		{1, 2, 3, 0, false},  // n < k
		{3, 2, 1, 0, false},  // c > n
		{2, 4, 0, 0, true},
	}
	for _, tc := range cases {
		got, ok := PassAtK(tc.c, tc.n, tc.k)
		if ok != tc.ok || (ok && math.Abs(got-tc.want) > 1e-12) {
			t.Errorf("PassAtK(%d,%d,%d) = %v,%v want %v,%v", tc.c, tc.n, tc.k, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPassCurve(t *testing.T) {
	succ := []int{4, 2, 0, 1}
	trials := []int{4, 4, 4, 4}
	pts := PassCurve(succ, trials, 4)
	if len(pts) != 4 {
		t.Fatalf("points = %d", len(pts))
	}
	if math.Abs(pts[0].PassAt-pts[0].PassHat) > 1e-12 || math.Abs(pts[0].PassAt-7.0/16) > 1e-12 {
		t.Fatalf("k=1 %+v", pts[0])
	}
	for i := 1; i < len(pts); i++ {
		if pts[i].PassAt < pts[i-1].PassAt || pts[i].PassHat > pts[i-1].PassHat {
			t.Fatalf("curve not monotone at k=%d: %+v", pts[i].K, pts[i])
		}
	}
	for _, p := range pts {
		if p.PassAtLow > p.PassAt || p.PassAtHigh < p.PassAt || p.PassHatLow > p.PassHat || p.PassHatHi < p.PassHat {
			t.Fatalf("band misses estimate: %+v", p)
		}
	}
	again := PassCurve(succ, trials, 4)
	for i := range pts {
		if pts[i] != again[i] {
			t.Fatal("bands not deterministic")
		}
	}
	if got := PassCurve([]int{1}, []int{2}, 3); len(got) != 2 {
		t.Fatalf("k beyond trials should be left out, got %d", len(got))
	}
	if PassCurve([]int{3}, []int{2}, 2) != nil {
		t.Fatal("invalid input should be nil")
	}
}

func TestFlipRate(t *testing.T) {
	if got := FlipRate([]int{0, 2, 1, 1}, []int{2, 2, 2, 1}); math.Abs(got-1.0/3) > 1e-12 {
		t.Fatalf("FlipRate = %v", got)
	}
	if !math.IsNaN(FlipRate([]int{1}, []int{1})) {
		t.Fatal("single trials should be NaN")
	}
}

func TestBootstrapRatio(t *testing.T) {
	lo, hi, ok := BootstrapRatio([]float64{1, 0, 1, 1}, []float64{1, 1, 1, 1}, 7)
	if !ok || lo > 0.75 || hi < 0.75 || lo < 0 || hi > 1 {
		t.Fatalf("got %v %v %v", lo, hi, ok)
	}
	if _, _, ok := BootstrapRatio([]float64{1}, []float64{1}, 7); ok {
		t.Fatal("one pair should not be ok")
	}
}
