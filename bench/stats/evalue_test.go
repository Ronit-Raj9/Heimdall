package stats

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestEProcessZeroAndAllUp(t *testing.T) {
	lt := math.Log(1 / 0.001)
	e := NewEProcess()
	for range 200 {
		e.Add(0, lt, 30)
	}
	if e.E(EUp) != 1 || e.E(EDown) != 1 || e.Crossed[EUp] != 0 {
		t.Errorf("zero differences moved the process: %v %v", e.E(EUp), e.E(EDown))
	}
	e = NewEProcess()
	for range 100 {
		e.Add(1, lt, 30)
	}
	if e.Crossed[EUp] < 30 || e.Crossed[EDown] != 0 {
		t.Errorf("all-up differences: crossed %v", e.Crossed)
	}
}

func TestPairedEValue(t *testing.T) {
	cases := []struct {
		name         string
		d            []float64
		max          int
		better, wors bool
	}{
		{"empty", nil, 0, false, false},
		{"all up stops at crossing", repeat(1, 100), 0, true, false},
		{"all down", repeat(-1, 100), 0, false, true},
		{"max pairs cuts", repeat(1, 100), 3, false, false},
		{"nan skipped", []float64{math.NaN(), math.Inf(1)}, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := PairedEValue(c.d, 0.05, 2, c.max)
			if r.Better != c.better || r.Worse != c.wors {
				t.Fatalf("better %v worse %v: %+v", r.Better, r.Worse, r)
			}
			if r.Better && (r.CrossedAt != r.Pairs || r.Path[len(r.Path)-1] < r.Threshold) {
				t.Errorf("did not stop at the crossing: %+v", r)
			}
			if c.max > 0 && r.Pairs > c.max {
				t.Errorf("read %d pairs over max %d", r.Pairs, c.max)
			}
		})
	}
}

// TestPairedEValueNullRate: under the null (fair coin differences) the
// chance of a false "better" stays under alpha.
func TestPairedEValueNullRate(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	trials, hits := 2000, 0
	for range trials {
		d := make([]float64, 60)
		for i := range d {
			d[i] = float64(r.IntN(3) - 1)
		}
		if PairedEValue(d, 0.05, 4, 0).Better {
			hits++
		}
	}
	if rate := float64(hits) / float64(trials); rate > 0.05+3*math.Sqrt(0.05*0.95/float64(trials)) {
		t.Errorf("false better rate %.4f over alpha", rate)
	}
}
