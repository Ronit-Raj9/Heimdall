package stats

import (
	"math"
	"testing"
)

func TestMDE(t *testing.T) {
	tests := []struct {
		name  string
		n     int
		sd    float64
		alpha float64
		power float64
		want  float64
		tol   float64
	}{
		{"design n=8 is 34.6 pp", 8, 0.30, 0.05, 0.8, 0.346, 5e-4},
		{"n=10 is 29.8 pp", 10, 0.30, 0.05, 0.8, 0.298, 5e-4},
		{"zero sd", 8, 0, 0.05, 0.8, 0, 0},
		{"one task", 1, 0.30, 0.05, 0.8, math.Inf(1), 0},
		{"no tasks", 0, 0.30, 0.05, 0.8, math.Inf(1), 0},
		{"negative sd", 8, -0.3, 0.05, 0.8, math.NaN(), 0},
		{"alpha out of range", 8, 0.3, 1, 0.8, math.NaN(), 0},
		{"power out of range", 8, 0.3, 0.05, 0, math.NaN(), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MDE(tc.n, tc.sd, tc.alpha, tc.power)
			if !sameFloat(got, tc.want, tc.tol) {
				t.Errorf("MDE(%d, %v, %v, %v) = %v, want %v", tc.n, tc.sd, tc.alpha, tc.power, got, tc.want)
			}
		})
	}
}

func TestTasksNeeded(t *testing.T) {
	tests := []struct {
		name       string
		target     float64
		sd         float64
		power      float64
		minT, maxT int
		want       int
	}{
		// MDE(7) = 0.380, MDE(8) = 0.3458.
		{"design n=8", 0.346, 0.30, 0.8, 2, 200, 8},
		// MDE(9) = 0.3195, MDE(10) = 0.2984.
		{"thirty pp", 0.30, 0.30, 0.8, 2, 200, 10},
		{"negative target uses magnitude", -0.30, 0.30, 0.8, 2, 200, 10},
		{"min above answer", 0.30, 0.30, 0.8, 25, 200, 25},
		{"min below two", 0.30, 0.30, 0.8, 0, 200, 10},
		{"unreachable", 0.01, 0.30, 0.8, 2, 50, 0},
		{"zero target", 0, 0.30, 0.8, 2, 50, 0},
		{"empty range", 0.30, 0.30, 0.8, 50, 10, 0},
		{"zero sd", 0.1, 0, 0.8, 2, 50, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TasksNeeded(tc.target, tc.sd, 0.05, tc.power, tc.minT, tc.maxT)
			if got != tc.want {
				t.Errorf("TasksNeeded(%v) = %d, want %d", tc.target, got, tc.want)
			}
		})
	}
}

// TestTasksNeededMatchesScan checks the binary search against a plain scan,
// and the scan path for power below 0.5.
func TestTasksNeededMatchesScan(t *testing.T) {
	for _, power := range []float64{0.3, 0.5, 0.8, 0.9} {
		for _, target := range []float64{0.05, 0.1, 0.17, 0.25, 0.4, 0.8} {
			t.Run("", func(t *testing.T) {
				want := 0
				for n := 2; n <= 300; n++ {
					if MDE(n, 0.3, 0.05, power) <= target {
						want = n
						break
					}
				}
				if got := TasksNeeded(target, 0.3, 0.05, power, 2, 300); got != want {
					t.Errorf("power=%v target=%v: TasksNeeded = %d, scan %d", power, target, got, want)
				}
			})
		}
	}
}
