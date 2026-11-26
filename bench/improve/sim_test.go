package improve

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/stats"
)

// The simulation suite of A2.13: the proof that the commit gate, the claim
// and the estimator keep their error rates and reach their power. Short mode
// runs simReps replications per cell; the improvesim build tag (sim_full_test.go)
// raises it and switches to the full-mode bounds.
var (
	simReps = 1000
	simFull = false
)

// simDraws is the Monte Carlo size of every sign-flip test in the suite.
const simDraws = 4000

// simUpper is the one-sided sign-flip p-value the suite uses: the Monte
// Carlo path at simDraws so the suite runs in seconds.
var simUpper = func(d []float64) float64 { return stats.SignFlipUpperDraws(d, simDraws) }

// bound picks the short or full mode bound.
func bound(short, full float64) float64 {
	if simFull {
		return full
	}
	return short
}

const simRho = 0.5

// gammaDraw is a Gamma(shape, 1) draw (Marsaglia and Tsang, with the
// boost for shape < 1).
func gammaDraw(r *rand.Rand, shape float64) float64 {
	if shape < 1 {
		return gammaDraw(r, shape+1) * math.Pow(r.Float64(), 1/shape)
	}
	d := shape - 1.0/3
	c := 1 / math.Sqrt(9*d)
	for {
		x := r.NormFloat64()
		v := 1 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := r.Float64()
		if math.Log(u) < 0.5*x*x+d-d*v+d*math.Log(v) {
			return d * v
		}
	}
}

// baseRates draws n task base rates from Beta(p0 (1/rho - 1), (1 - p0)(1/rho - 1)),
// whose intra-class correlation is rho.
func baseRates(r *rand.Rand, n int, p0 float64) []float64 {
	k := 1/simRho - 1
	out := make([]float64, n)
	for i := range out {
		x := gammaDraw(r, p0*k)
		y := gammaDraw(r, (1-p0)*k)
		out[i] = x / (x + y)
	}
	return out
}

// trialMean is the mean of m Bernoulli(p) trials.
func trialMean(r *rand.Rand, p float64, m int) float64 {
	s := 0
	for range m {
		if r.Float64() < p {
			s++
		}
	}
	return float64(s) / float64(m)
}

// diffs runs m trials per arm per task and returns per-task mean
// differences B minus A.
func diffs(r *rand.Rand, pa, pb []float64, m int) []float64 {
	d := make([]float64, len(pa))
	for i := range pa {
		d[i] = trialMean(r, pb[i], m) - trialMean(r, pa[i], m)
	}
	return d
}

// heteroNull moves a random half of the tasks up by h min(p, 1-p) and the
// other half down by the same amount: a null whose gain varies by task.
func heteroNull(r *rand.Rand, p []float64, h float64) []float64 {
	idx := r.Perm(len(p))
	out := make([]float64, len(p))
	for j, i := range idx {
		step := h * math.Min(p[i], 1-p[i])
		if j < len(p)/2 {
			out[i] = p[i] + step
		} else {
			out[i] = p[i] - step
		}
	}
	return out
}

// gateCommits is the decision of fallbackSignFlip.Decide with the suite's
// sign-flip p-value.
func gateCommits(d []float64, level float64) bool {
	if len(d) == 0 || stats.MinPossiblePUpper(nonzero(d)) > level {
		return false
	}
	return mean(d) > 0 && simUpper(d) <= level
}

func logit(p float64) float64 {
	p = math.Min(math.Max(p, 1e-9), 1-1e-9)
	return math.Log(p / (1 - p))
}
func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

func shifted(p []float64, s float64) []float64 {
	out := make([]float64, len(p))
	for i, v := range p {
		out[i] = sigmoid(logit(v) + s)
	}
	return out
}

// logitShift finds the constant logit shift whose mean gain over the base
// rate distribution of p0 is delta (from a large fixed sample).
func logitShift(p0, delta float64) float64 {
	pop := baseRates(rand.New(rand.NewPCG(7, 7)), 40000, p0)
	gain := func(s float64) float64 {
		t := 0.0
		for i, v := range shifted(pop, s) {
			t += v - pop[i]
		}
		return t / float64(len(pop))
	}
	lo, hi := 0.0, 10.0
	for range 60 {
		mid := (lo + hi) / 2
		if gain(mid) < delta {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func cellRand(name string) *rand.Rand {
	h := uint64(14695981039346656037)
	for _, c := range name {
		h = (h ^ uint64(c)) * 1099511628211
	}
	return rand.New(rand.NewPCG(h, 0x51e))
}

var (
	simNs  = []int{20, 50, 200}
	simMs  = []int{1, 3}
	simP0s = []float64{0.4, 0.7}
)

// rejectRate is the share of reps in which one gate test at level commits.
func rejectRate(r *rand.Rand, n, m int, p0, level float64, armB func(r *rand.Rand, p []float64) []float64) float64 {
	hits := 0
	for range simReps {
		pa := baseRates(r, n, p0)
		if gateCommits(diffs(r, pa, armB(r, pa), m), level) {
			hits++
		}
	}
	return float64(hits) / float64(simReps)
}

func TestSimGateSharpNull(t *testing.T) {
	limit := bound(0.071, 0.058)
	for _, n := range simNs {
		for _, m := range simMs {
			for _, p0 := range simP0s {
				name := fmt.Sprintf("n%d_m%d_p%.1f", n, m, p0)
				t.Run(name, func(t *testing.T) {
					rate := rejectRate(cellRand(t.Name()), n, m, p0, 0.05, func(_ *rand.Rand, p []float64) []float64 { return p })
					t.Logf("%-16s rejection %.4f (at most %.3f)", name, rate, limit)
					if rate > limit {
						t.Errorf("rejection rate %.4f above %.3f", rate, limit)
					}
				})
			}
		}
	}
}

func TestSimGateHeterogeneousNull(t *testing.T) {
	limit := bound(0.071, 0.058)
	for _, h := range []float64{0.5, 1.0} {
		for _, n := range simNs {
			for _, m := range simMs {
				for _, p0 := range simP0s {
					name := fmt.Sprintf("h%.1f_n%d_m%d_p%.1f", h, n, m, p0)
					t.Run(name, func(t *testing.T) {
						rate := rejectRate(cellRand(t.Name()), n, m, p0, 0.05, func(r *rand.Rand, p []float64) []float64 { return heteroNull(r, p, h) })
						t.Logf("%-20s rejection %.4f (at most %.3f)", name, rate, limit)
						if rate > limit {
							t.Errorf("rejection rate %.4f above %.3f", rate, limit)
						}
					})
				}
			}
		}
	}
}

// TestSimLoopFWER runs the whole gate over 3 looks with the online fallback
// budget (alpha_g 0.10); every candidate is a heterogeneous null of the
// incumbent, and the incumbent moves when a look commits.
func TestSimLoopFWER(t *testing.T) {
	limit := bound(0.12, 0.112)
	spec := &Spec{Rounds: 3, Commit: CommitSpec{Alpha: 0.10, AuditLooks: 3}}
	rule := fallbackSignFlip{}
	for _, n := range simNs {
		for _, m := range simMs {
			for _, p0 := range simP0s {
				name := fmt.Sprintf("n%d_m%d_p%.1f", n, m, p0)
				t.Run(name, func(t *testing.T) {
					r := cellRand(t.Name())
					any := 0
					for range simReps {
						inc := baseRates(r, n, p0)
						var looks []LookRecord
						committed := false
						for round := 1; round <= 3; round++ {
							level := rule.Level(spec, looks)
							cand := heteroNull(r, inc, 0.5)
							c := gateCommits(diffs(r, inc, cand, m), level)
							looks = append(looks, LookRecord{Round: round, Level: level, Committed: c})
							if c {
								committed = true
								inc = cand
							}
						}
						if committed {
							any++
						}
					}
					rate := float64(any) / float64(simReps)
					t.Logf("%-16s any commit %.4f (at most %.3f)", name, rate, limit)
					if rate > limit {
						t.Errorf("chance of a commit %.4f above %.3f", rate, limit)
					}
				})
			}
		}
	}
}

// powerAt is the share of reps a gate test at level commits for a logit
// shift giving mean gain delta.
func powerAt(r *rand.Rand, n, m int, p0, delta, level float64) float64 {
	s := logitShift(p0, delta)
	return rejectRate(r, n, m, p0, level, func(_ *rand.Rand, p []float64) []float64 { return shifted(p, s) })
}

func TestSimGatePower(t *testing.T) {
	cases := []struct {
		level       float64
		n           int
		delta       float64
		short, full float64
		name        string
	}{
		{0.05 / 3, 50, 0.10, 0.50, 0.55, "level0.0167_n50_plus10"},
		{0.05 / 3, 200, 0.05, 0.53, 0.58, "level0.0167_n200_plus5"},
		{0.10 / 3, 50, 0.10, 0.55, 0.60, "level0.0333_n50_plus10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := bound(c.short, c.full)
			pw := powerAt(cellRand(t.Name()), c.n, 3, 0.4, c.delta, c.level)
			t.Logf("%-24s power %.4f (at least %.2f)", c.name, pw, want)
			if pw < want {
				t.Errorf("power %.4f below %.2f", pw, want)
			}
		})
	}
}

// TestSimNewBeatsOldGate compares the new gate with the old e-value gate
// (GateTest on per task and trial pairs, at most 60 pairs) on the same data.
func TestSimNewBeatsOldGate(t *testing.T) {
	const m, p0, delta, level = 3, 0.4, 0.10, 0.05 / 3
	for _, n := range []int{50, 200} {
		name := fmt.Sprintf("n%d_plus10", n)
		t.Run(name, func(t *testing.T) {
			r := cellRand(t.Name())
			s := logitShift(p0, delta)
			tasks := make([]string, n)
			for i := range tasks {
				tasks[i] = fmt.Sprintf("t%03d", i)
			}
			newHits, oldHits := 0, 0
			for range simReps {
				pa := baseRates(r, n, p0)
				pb := shifted(pa, s)
				runs := make([]RunScore, 0, 2*n*m)
				d := make([]float64, n)
				for i, task := range tasks {
					sa, sb := 0.0, 0.0
					for trial := 1; trial <= m; trial++ {
						ya, yb := 0.0, 0.0
						if r.Float64() < pa[i] {
							ya = 1
						}
						if r.Float64() < pb[i] {
							yb = 1
						}
						sa += ya
						sb += yb
						runs = append(runs, RunScore{Arm: 0, Task: task, Trial: trial, Success: ya, Known: true},
							RunScore{Arm: 1, Task: task, Trial: trial, Success: yb, Known: true})
					}
					d[i] = (sb - sa) / m
				}
				if gateCommits(d, level) {
					newHits++
				}
				if GateTest(runs, tasks, level, DefaultMinPairs, DefaultMaxPairs).Better {
					oldHits++
				}
			}
			pn, po := float64(newHits)/float64(simReps), float64(oldHits)/float64(simReps)
			t.Logf("%-12s new %.4f old %.4f difference %.4f (at least 0.30)", name, pn, po, pn-po)
			if pn-po < 0.3 {
				t.Errorf("new gate power %.4f minus old %.4f below 0.30", pn, po)
			}
		})
	}
}

// TestSimEstimatorCalibrated runs the gate at the estimator's detectable
// gain: its power must sit near the formula's 80%.
func TestSimEstimatorCalibrated(t *testing.T) {
	const m, level = 3, 0.0167
	for _, n := range []int{50, 200} {
		for _, p0 := range simP0s {
			name := fmt.Sprintf("n%d_p%.1f", n, p0)
			t.Run(name, func(t *testing.T) {
				delta := mdeFormula(n, m, level, p0*(1-p0)*(1-simRho))
				pw := powerAt(cellRand(t.Name()), n, m, p0, delta, level)
				t.Logf("%-12s MDE %.1f points power %.4f (in 0.55 to 0.95)", name, 100*delta, pw)
				if pw < 0.55 || pw > 0.95 {
					t.Errorf("power %.4f at the estimated detectable gain is outside 0.55 to 0.95", pw)
				}
			})
		}
	}
}

// simHalvingWinner runs Sequential Halving (A2.3) on dev for K candidates
// with the given rates and returns the winner's index.
func simHalvingWinner(r *rand.Rand, inc []float64, cands [][]float64, ts int) int {
	k, nd := len(cands), len(inc)
	levels := int(math.Ceil(math.Log2(float64(k))))
	order := r.Perm(nd)
	alive := make([]int, k)
	for i := range alive {
		alive[i] = i
	}
	acc := make([][]float64, k)
	start := 0
	for j := range levels {
		size := nd / levels
		if j < nd%levels {
			size++
		}
		chunk := order[start : start+size]
		start += size
		for _, ti := range chunk {
			a := trialMean(r, inc[ti], ts)
			for _, c := range alive {
				acc[c] = append(acc[c], trialMean(r, cands[c][ti], ts)-a)
			}
		}
		// Cut: drop the clearly worse, rank by mean difference, ties by slot.
		kept := []int{}
		for _, c := range alive {
			neg := make([]float64, len(acc[c]))
			for i, x := range acc[c] {
				neg[i] = -x
			}
			if simUpper(neg) < 0.05 {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) == 0 {
			kept = slices.Clone(alive)
		}
		slices.SortStableFunc(kept, func(x, y int) int {
			mx, my := mean(acc[x]), mean(acc[y])
			if math.Abs(mx-my) < 1e-9 {
				return x - y
			}
			if mx > my {
				return -1
			}
			return 1
		})
		alive = kept[:min(len(kept), (len(alive)+1)/2)]
	}
	return alive[0]
}

func TestSimHalvingPicksBest(t *testing.T) {
	want := bound(0.45, 0.50)
	const k, nd, ts = 4, 40, 1
	for _, p0 := range simP0s {
		name := fmt.Sprintf("p%.1f", p0)
		t.Run(name, func(t *testing.T) {
			r := cellRand(t.Name())
			s := logitShift(p0, 0.10)
			wins := 0
			for range simReps {
				inc := baseRates(r, nd, p0)
				cands := make([][]float64, k)
				good := r.IntN(k)
				for c := range cands {
					cands[c] = inc
					if c == good {
						cands[c] = shifted(inc, s)
					}
				}
				if simHalvingWinner(r, inc, cands, ts) == good {
					wins++
				}
			}
			rate := float64(wins) / float64(simReps)
			t.Logf("%-6s good one wins %.4f (at least %.2f)", name, rate, want)
			if rate < want {
				t.Errorf("the better candidate won %.4f of the time, below %.2f", rate, want)
			}
		})
	}
}

// claimSupported is rule 3 of A2.5 with the suite's p-value.
func claimSupported(d []float64, level float64) bool {
	return mean(d) > 0 && simUpper(d) <= level
}

func TestSimClaimNullStates(t *testing.T) {
	limit := bound(0.045, 0.036)
	const ns, m, aMain = 30, 2, 0.03
	for _, p0 := range simP0s {
		name := fmt.Sprintf("p%.1f", p0)
		t.Run(name, func(t *testing.T) {
			r := cellRand(t.Name())
			hits := 0
			for range simReps {
				p := baseRates(r, ns, p0)
				if claimSupported(diffs(r, p, p, m), aMain) {
					hits++
				}
			}
			rate := float64(hits) / float64(simReps)
			t.Logf("%-6s supported %.4f (at most %.3f)", name, rate, limit)
			if rate > limit {
				t.Errorf("supported %.4f above %.3f", rate, limit)
			}
		})
	}
}

// TestSimExtensionKeepsAlpha: under the null, the first look at a_main 0.03
// and, when the continue offer would show (likely better or too small a
// sample), a second look at 0.02 on the same tasks with 2 more trials per
// arm, on the means over all trials.
func TestSimExtensionKeepsAlpha(t *testing.T) {
	limit := bound(0.065, 0.058)
	const ns, m, extra, aMain, aExt = 30, 2, 2, 0.03, 0.02
	for _, p0 := range simP0s {
		name := fmt.Sprintf("p%.1f", p0)
		t.Run(name, func(t *testing.T) {
			r := cellRand(t.Name())
			hits := 0
			for range simReps {
				p := baseRates(r, ns, p0)
				a1 := make([]float64, ns)
				b1 := make([]float64, ns)
				for i := range p {
					a1[i], b1[i] = trialMean(r, p[i], m), trialMean(r, p[i], m)
				}
				d := make([]float64, ns)
				for i := range d {
					d[i] = b1[i] - a1[i]
				}
				if claimSupported(d, aMain) {
					hits++
					continue
				}
				pu := simUpper(d)
				gain := mean(d)
				mdePost := math.Inf(1)
				if len(d) >= 2 {
					mdePost = (stats.TQuantile(1-aMain, ns-1) + stats.TQuantile(0.8, ns-1)) * math.Max(stats.SD(d), claimSDFloor) / math.Sqrt(ns)
				}
				insufficient := stats.MinPossiblePUpper(nonzero(d)) > aMain || mdePost > 2*DefaultTargetGain
				likely := gain > 0 && pu <= LikelyBetterP
				if gain < 0 && simUpper(negate(d)) <= aMain { // worse: no offer
					continue
				}
				if !insufficient && !likely {
					continue
				}
				d2 := make([]float64, ns)
				for i := range p {
					a := (a1[i]*m + trialMean(r, p[i], extra)*extra) / (m + extra)
					b := (b1[i]*m + trialMean(r, p[i], extra)*extra) / (m + extra)
					d2[i] = b - a
				}
				if claimSupported(d2, aExt) {
					hits++
				}
			}
			rate := float64(hits) / float64(simReps)
			t.Logf("%-6s supported at either look %.4f (at most %.3f)", name, rate, limit)
			if rate > limit {
				t.Errorf("supported %.4f above %.3f", rate, limit)
			}
		})
	}
}

func negate(d []float64) []float64 {
	out := make([]float64, len(d))
	for i, x := range d {
		out[i] = -x
	}
	return out
}

// TestSimUpperMatchesExact checks SignFlipUpper against a brute-force
// enumeration on 200 random inputs of 6 to 20 tasks.
func TestSimUpperMatchesExact(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 13))
	for i := range 200 {
		n := 6 + r.IntN(15)
		d := make([]float64, n)
		for j := range d {
			d[j] = float64(r.IntN(7)-3) / 3
		}
		obs := 0.0
		scale := 0.0
		nz := []float64{}
		for _, x := range d {
			obs += x
			scale += math.Abs(x)
			if x != 0 {
				nz = append(nz, x)
			}
		}
		want := 1.0
		if len(nz) > 0 {
			hits := 0
			thr := obs - 1e-9*scale
			for mask := 0; mask < 1<<len(nz); mask++ {
				s := 0.0
				for j, x := range nz {
					if mask&(1<<j) != 0 {
						s -= x
					} else {
						s += x
					}
				}
				if s >= thr {
					hits++
				}
			}
			want = float64(hits) / float64(int(1)<<len(nz))
		}
		if got := stats.SignFlipUpper(d).P; math.Abs(got-want) > 1e-12 {
			t.Fatalf("input %d %v: SignFlipUpper %v, enumeration %v", i, d, got, want)
		}
	}
}
