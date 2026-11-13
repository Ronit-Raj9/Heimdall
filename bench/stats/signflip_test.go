package stats

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

// diffs turns per-task success counts out of trials into d_i = (B_i-A_i)/trials.
func diffs(a, b []int, trials int) []float64 {
	d := make([]float64, len(a))
	for i := range a {
		d[i] = float64(b[i]-a[i]) / float64(trials)
	}
	return d
}

// repeat returns n copies of v.
func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// concat joins slices into a new one.
func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestWorkedExampleDesign93(t *testing.T) {
	a := []int{3, 3, 0, 2, 0, 2, 1, 3}
	b := []int{3, 3, 1, 3, 0, 3, 2, 3}
	d := diffs(a, b, 3)

	dBar := Mean(d)
	lo, hi, ok := PairedT(d, 0.95)
	res := SignFlip(d)
	t.Logf("worked example: d_bar=%.4f interval=[%.3f, %.3f] n_nonzero=%d p=%v exact=%v",
		dBar, lo, hi, res.NNonzero, res.P, res.Exact)

	if got := math.Round(dBar*1e4) / 1e4; got != 0.1667 {
		t.Errorf("d_bar = %v, want 0.1667", dBar)
	}
	if !ok {
		t.Fatal("PairedT not ok")
	}
	if got := math.Round(lo*1e3) / 1e3; got != 0.018 {
		t.Errorf("interval lo = %v, want 0.018", lo)
	}
	if got := math.Round(hi*1e3) / 1e3; got != 0.316 {
		t.Errorf("interval hi = %v, want 0.316", hi)
	}
	if res.NNonzero != 4 || res.NPositive != 4 || res.NNegative != 0 {
		t.Errorf("counts = %+v, want 4 nonzero, 4 positive", res)
	}
	if res.P != 0.125 || !res.Exact || res.Draws != 0 {
		t.Errorf("P = %v exact=%v draws=%d, want exactly 0.125, exact, 0 draws", res.P, res.Exact, res.Draws)
	}
}

func TestSignFlip(t *testing.T) {
	tests := []struct {
		name     string
		d        []float64
		wantP    float64
		nonzero  int
		positive int
		negative int
	}{
		{"empty", nil, 1, 0, 0, 0},
		{"all zeros", repeat(0, 10), 1, 0, 0, 0},
		{"routing study 70-4-3", concat(repeat(0, 70), repeat(-1, 4), repeat(1, 3)), 1, 7, 3, 4},
		{"split 5-0", repeat(1, 5), 0.0625, 5, 5, 0},
		{"split 6-0", repeat(1, 6), 0.03125, 6, 6, 0},
		{"split 0-6 is symmetric", repeat(-1, 6), 0.03125, 6, 0, 6},
		{"split 6-0 with zeros", concat(repeat(0, 20), repeat(1, 6)), 0.03125, 6, 6, 0},
		{"one nonzero", []float64{0, 0, 0.5}, 1, 1, 1, 0},
		{"balanced sum zero", []float64{1, -1, 0.5, -0.5}, 1, 4, 2, 2},
		// 5 of 6 agree: |sum| >= 4 for sign vectors with at most one minus,
		// or at most one plus: 2*(1+6) = 14 of 64.
		{"split 5-1", concat(repeat(1, 5), []float64{-1}), 14.0 / 64, 6, 5, 1},
		// Units of 1/3: magnitudes {1,1,1,1,2}, sum 4 of 6; extreme when the
		// flipped magnitude is at most 1 or at least 5: 10 of 32.
		{"thirds tie", []float64{1.0 / 3, 1.0 / 3, 1.0 / 3, -1.0 / 3, 2.0 / 3}, 10.0 / 32, 5, 4, 1},
		{"float noise is a tie", concat(repeat(1, 6), []float64{1e-17}), 0.03125, 6, 6, 0},
		{"22 nonzero still exact", repeat(1, 22), math.Ldexp(1, -21), 22, 22, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SignFlip(tc.d)
			if math.Abs(got.P-tc.wantP) > 1e-15 {
				t.Errorf("P = %v, want %v", got.P, tc.wantP)
			}
			if got.NNonzero != tc.nonzero || got.NPositive != tc.positive || got.NNegative != tc.negative {
				t.Errorf("counts = %+v, want nonzero=%d positive=%d negative=%d", got, tc.nonzero, tc.positive, tc.negative)
			}
			if !got.Exact || got.Draws != 0 {
				t.Errorf("exact=%v draws=%d, want exact with 0 draws", got.Exact, got.Draws)
			}
		})
	}
}

func TestSignFlipNonFinite(t *testing.T) {
	tests := []struct {
		name string
		d    []float64
	}{
		{"nan", []float64{1, math.NaN(), 1}},
		{"inf", []float64{1, math.Inf(1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignFlip(tc.d); !math.IsNaN(got.P) {
				t.Errorf("P = %v, want NaN", got.P)
			}
		})
	}
}

// TestSignFlipMatchesBruteForce checks the split enumeration, the zero rule
// and the tie tolerance against a direct loop over all 2^n sign vectors of
// the full vector, zeros included.
func TestSignFlipMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	tests := []struct {
		name  string
		n     int
		cases int
	}{
		{"n=3", 3, 50},
		{"n=8", 8, 50},
		{"n=12", 12, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for range tc.cases {
				d := make([]float64, tc.n)
				for i := range d {
					d[i] = float64(r.IntN(7)-3) / 3 // thirds, with zeros and ties
				}
				want := bruteForceP(d)
				if got := SignFlip(d).P; got != want {
					t.Fatalf("d=%v: P = %v, brute force %v", d, got, want)
				}
			}
		})
	}
}

// bruteForceP is the textbook definition over the full vector.
func bruteForceP(d []float64) float64 {
	observed, scale := 0.0, 0.0
	for _, v := range d {
		observed += v
		scale += math.Abs(v)
	}
	threshold := math.Abs(observed) - 1e-9*scale
	hits := 0
	for mask := range 1 << len(d) {
		s := 0.0
		for i, v := range d {
			if mask&(1<<i) != 0 {
				s -= v
			} else {
				s += v
			}
		}
		if math.Abs(s) >= threshold {
			hits++
		}
	}
	return float64(hits) / float64(int(1)<<len(d))
}

func TestSignFlipMonteCarlo(t *testing.T) {
	varied := make([]float64, 25)
	for i := range varied {
		varied[i] = 0.2 + 0.05*float64(i)
		if i%3 == 2 {
			varied[i] = -varied[i]
		}
	}
	tests := []struct {
		name  string
		d     []float64
		exact float64 // exact p when known in closed form, else NaN
	}{
		// 17 of 25 agree: exact p = 2 * P(Bin(25, 0.5) >= 17) = 2*1807781/2^25.
		{"25 unit differences 17-8 with zeros", concat(repeat(0, 5), repeat(1, 17), repeat(-1, 8)), 2 * 1807781.0 / 33554432},
		{"25 varied magnitudes", varied, math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SignFlip(tc.d)
			if got.Exact || got.Draws != MonteCarloDraws || got.NNonzero != 25 {
				t.Fatalf("exact=%v draws=%d nonzero=%d, want Monte Carlo with %d draws and 25 nonzero",
					got.Exact, got.Draws, got.NNonzero, MonteCarloDraws)
			}
			normal := normalApproxP(tc.d)
			t.Logf("monte carlo P=%.5f normal approximation=%.5f exact=%.5f", got.P, normal, tc.exact)
			if math.Abs(got.P-normal) > 0.01 {
				t.Errorf("P = %v, normal approximation %v, differ by more than 0.01", got.P, normal)
			}
			if !math.IsNaN(tc.exact) && math.Abs(got.P-tc.exact) > 0.003 {
				t.Errorf("P = %v, exact %v, differ by more than 0.003 (6 standard errors)", got.P, tc.exact)
			}
			if again := SignFlip(tc.d); again.P != got.P {
				t.Errorf("fixed seed not deterministic: %v then %v", got.P, again.P)
			}
		})
	}
}

// normalApproxP approximates the sign-flip p-value: under the null the sum
// has mean 0 and variance sum d_i^2. When every nonzero |d_i| is equal the
// sum moves in steps of 2|d|, so a continuity correction of |d| applies.
func normalApproxP(d []float64) float64 {
	sum, sumSq, unit := 0.0, 0.0, -1.0
	for _, v := range d {
		if v == 0 {
			continue
		}
		sum += v
		sumSq += v * v
		switch {
		case unit < 0:
			unit = math.Abs(v)
		case unit != math.Abs(v):
			unit = 0
		}
	}
	z := (math.Abs(sum) - math.Max(unit, 0)) / math.Sqrt(sumSq)
	return math.Erfc(z / math.Sqrt2)
}

// binomial draws Bin(n, p).
func binomial(r *rand.Rand, n int, p float64) int {
	k := 0
	for range n {
		if r.Float64() < p {
			k++
		}
	}
	return k
}

// TestSignFlipCalibration simulates 8 tasks x 3 trials. With no difference
// between arms the test must reject at most at its nominal rate; with a real
// +0.4 per-task effect it must reject more often.
func TestSignFlipCalibration(t *testing.T) {
	const (
		tasks  = 8
		trials = 3
		alpha  = 0.05
	)
	start := time.Now()
	r := rand.New(rand.NewPCG(20261114, 42))
	tests := []struct {
		name        string
		experiments int
		effect      float64
	}{
		{"null", 2000, 0},
		{"effect +0.4", 1000, 0.4},
	}
	rates := make(map[string]float64, len(tests))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rejected := 0
			for range tc.experiments {
				d := make([]float64, tasks)
				for i := range d {
					pA := r.Float64() * (1 - tc.effect)
					pB := pA + tc.effect
					d[i] = float64(binomial(r, trials, pB)-binomial(r, trials, pA)) / trials
				}
				if SignFlip(d).P < alpha {
					rejected++
				}
			}
			rates[tc.name] = float64(rejected) / float64(tc.experiments)
			t.Logf("%s: rejection rate %.4f (%d of %d)", tc.name, rates[tc.name], rejected, tc.experiments)
		})
	}
	if rates["null"] > 0.06 {
		t.Errorf("null rejection rate %v, want at most 0.06", rates["null"])
	}
	if rates["effect +0.4"] <= rates["null"] {
		t.Errorf("effect rejection rate %v not above null %v", rates["effect +0.4"], rates["null"])
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("calibration took %v, want under 10s", elapsed)
	}
}

func TestMinPossibleP(t *testing.T) {
	tests := []struct {
		n    int
		want float64
	}{
		{-1, 1},
		{0, 1},
		{1, 1},
		{2, 0.5},
		{4, 0.125},
		{5, 0.0625},
		{6, 0.03125},
		{10, 2.0 / 1024},
	}
	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			if got := MinPossibleP(tc.n); got != tc.want {
				t.Errorf("MinPossibleP(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}
