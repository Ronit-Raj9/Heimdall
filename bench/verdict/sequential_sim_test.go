package verdict

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Seeded simulations of the W7 adaptive rules (docs/adaptive-trials.md,
// "Simulation"). Each draws whole experiments under the null (both arms
// share each task's success probability) and under an alternative, runs
// the real rule code (taskTrials, replaySequential) and the exact sign-flip
// test, and compares with the fixed design on the same draws. Run with -v to
// print the table; -short cuts the replications.

// simProbs are per-task success probabilities, spread from always-fail to
// always-pass, cycled to n tasks.
var simProbs = []float64{0, 0.05, 0.2, 0.35, 0.5, 0.65, 0.8, 0.95, 1, 0.5, 0.3, 0.7}

func simReps(t *testing.T, n int) int {
	if testing.Short() {
		return n / 4
	}
	return n
}

// mcBound is the rejection rate a test at alpha may show by chance in reps
// replications: alpha plus three standard errors.
func mcBound(alpha float64, reps int) float64 {
	return alpha + 3*math.Sqrt(alpha*(1-alpha)/float64(reps))
}

type taskSimResult struct {
	rejectAdaptive, rejectFixed, rejectAsym, rejectMin, rejectLess int
	runsAdaptive, runsFixed, runsMin                               int
}

// simTaskRule runs reps experiments of n tasks under the task rule (min, max)
// with B's success probability pA+delta on the first half of tasks (delta 0
// is the null). It also runs a deliberately asymmetric rule (more trials
// only while B is not ahead) to show the simulation can see inflation.
func simTaskRule(seed uint64, reps, n, minT, maxT int, delta float64) taskSimResult {
	return simTaskRuleProbs(seed, reps, n, minT, maxT, delta, simProbs)
}

// satProbs is a suite where most tasks are saturated: both arms always pass
// or always fail under the null, as in a regression suite.
var satProbs = []float64{1, 1, 0, 1, 0.5, 1, 0, 1, 0.3, 1, 0, 0.8}

func simTaskRuleProbs(seed uint64, reps, n, minT, maxT int, delta float64, probs []float64) taskSimResult {
	r := rand.New(rand.NewPCG(seed, 0x57))
	trials := make([]map[int][]schemas.Slot, n)
	for i := range trials {
		trials[i] = map[int][]schemas.Slot{}
		for k := 1; k <= maxT; k++ {
			for _, arm := range []string{ArmBaseline, ArmCandidate} {
				trials[i][k] = append(trials[i][k], schemas.Slot{SlotID: fmt.Sprintf("%d_%s_%d", i, arm, k), Arm: arm, Trial: k})
			}
		}
	}
	var res taskSimResult
	vals := map[string]slotValue{}
	a := make([][]float64, n)
	b := make([][]float64, n)
	dA, dF, dX, dM, dL := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for rep := 0; rep < reps; rep++ {
		for i := 0; i < n; i++ {
			pA := probs[i%len(probs)]
			pB := pA
			if i < n/2 {
				pB = math.Max(0, math.Min(1, pA+delta))
			}
			a[i], b[i] = make([]float64, maxT), make([]float64, maxT)
			for k := 0; k < maxT; k++ {
				a[i][k], b[i][k] = bern(r, pA), bern(r, pB)
				vals[fmt.Sprintf("%d_A_%d", i, k+1)] = slotValue{x: a[i][k], observed: true, run: true, settled: true}
				vals[fmt.Sprintf("%d_B_%d", i, k+1)] = slotValue{x: b[i][k], observed: true, run: true, settled: true}
			}
			used, _ := taskTrials(trials[i], vals, minT, maxT)
			dA[i] = mean(b[i][:used]) - mean(a[i][:used])
			dF[i] = mean(b[i]) - mean(a[i])
			dM[i] = mean(b[i][:minT]) - mean(a[i][:minT])
			dL[i] = mean(b[i][:maxT-1]) - mean(a[i][:maxT-1])
			asym := minT
			for asym < maxT && mean(b[i][:asym]) <= mean(a[i][:asym]) {
				asym++
			}
			dX[i] = mean(b[i][:asym]) - mean(a[i][:asym])
			res.runsAdaptive += 2 * used
			res.runsFixed += 2 * maxT
			res.runsMin += 2 * minT
		}
		if stats.SignFlip(dA).P < 0.05 {
			res.rejectAdaptive++
		}
		if stats.SignFlip(dF).P < 0.05 {
			res.rejectFixed++
		}
		if stats.SignFlip(dX).P < 0.05 {
			res.rejectAsym++
		}
		if stats.SignFlip(dM).P < 0.05 {
			res.rejectMin++
		}
		if stats.SignFlip(dL).P < 0.05 {
			res.rejectLess++
		}
	}
	return res
}

func bern(r *rand.Rand, p float64) float64 {
	if r.Float64() < p {
		return 1
	}
	return 0
}

func mean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func TestSimTaskRuleTypeIAndPower(t *testing.T) {
	reps := simReps(t, 4000)
	cases := []struct {
		n, min, max int
	}{{10, 2, 5}, {16, 2, 4}, {20, 1, 3}}
	t.Logf("task rule, %d replications per row, alpha 0.05", reps)
	t.Logf("| tasks | min-max | null: adaptive | null: fixed max | null: asymmetric control | power +50pp on half: adaptive | fixed max | fixed max-1 | fixed min | runs adaptive / fixed max (null) | (alt) |")
	for i, c := range cases {
		null := simTaskRule(uint64(100+i), reps, c.n, c.min, c.max, 0)
		alt := simTaskRule(uint64(200+i), reps, c.n, c.min, c.max, 0.5)
		rate := func(k int) float64 { return float64(k) / float64(reps) }
		t.Logf("| %d | %d-%d | %.4f | %.4f | %.4f | %.3f | %.3f | %.3f | %.3f | %.0f%% | %.0f%% |", c.n, c.min, c.max,
			rate(null.rejectAdaptive), rate(null.rejectFixed), rate(null.rejectAsym), rate(alt.rejectAdaptive), rate(alt.rejectFixed), rate(alt.rejectLess), rate(alt.rejectMin),
			100*float64(null.runsAdaptive)/float64(null.runsFixed), 100*float64(alt.runsAdaptive)/float64(alt.runsFixed))
		if got, bound := rate(null.rejectAdaptive), mcBound(0.05, reps); got > bound {
			t.Errorf("n=%d: type-I error %.4f above %.4f", c.n, got, bound)
		}
		if null.runsAdaptive >= null.runsFixed {
			t.Errorf("n=%d: adaptive used %d runs, fixed %d", c.n, null.runsAdaptive, null.runsFixed)
		}
	}
	// A regression suite: most tasks saturated, B regresses by 50pp on half
	// of the tasks (an always-pass task drops to 0.5).
	t.Logf("regression-suite rows: power is against a 50pp regression on half of the tasks")
	for i, c := range []struct{ n, min, max int }{{16, 2, 4}, {20, 2, 4}} {
		null := simTaskRuleProbs(uint64(500+i), reps, c.n, c.min, c.max, 0, satProbs)
		alt := simTaskRuleProbs(uint64(600+i), reps, c.n, c.min, c.max, -0.5, satProbs)
		rate := func(k int) float64 { return float64(k) / float64(reps) }
		t.Logf("| %d saturated | %d-%d | %.4f | %.4f | %.4f | %.3f | %.3f | %.3f | %.3f | %.0f%% | %.0f%% |", c.n, c.min, c.max,
			rate(null.rejectAdaptive), rate(null.rejectFixed), rate(null.rejectAsym), rate(alt.rejectAdaptive), rate(alt.rejectFixed), rate(alt.rejectLess), rate(alt.rejectMin),
			100*float64(null.runsAdaptive)/float64(null.runsFixed), 100*float64(alt.runsAdaptive)/float64(alt.runsFixed))
		if got, bound := rate(null.rejectAdaptive), mcBound(0.05, reps); got > bound {
			t.Errorf("saturated n=%d: type-I error %.4f above %.4f", c.n, got, bound)
		}
	}
	// The asymmetric control must show inflation the symmetric rule does not.
	ctl := simTaskRule(999, reps, 20, 1, 6, 0)
	if float64(ctl.rejectAsym)/float64(reps) <= mcBound(0.05, reps) {
		t.Errorf("asymmetric control rejected %.4f: the simulation cannot see inflation", float64(ctl.rejectAsym)/float64(reps))
	}
	t.Logf("asymmetric control (20 tasks, min 1, max 6, null): %.4f vs symmetric %.4f", float64(ctl.rejectAsym)/float64(reps), float64(ctl.rejectAdaptive)/float64(reps))
}

type seqSimResult struct {
	reject, rejectFixed, early, futile int
	runs, runsFixed                    int
}

// simSequential runs reps experiments of n tasks, trials per arm, in looks
// under the sequential rule with the given split, B at pA+delta on every
// other task; the fixed design tests all n tasks once at 0.05.
func simSequential(seed uint64, reps, n, trials, looks int, split string, delta float64, futility string) seqSimResult {
	r := rand.New(rand.NewPCG(seed, 0x5e))
	m := w7Manifest(schemas.AdaptiveSequential, n, 0, trials, looks, split)
	m.Adaptive.Futility = futility
	var res seqSimResult
	vals := map[string]slotValue{}
	d := make([]float64, n)
	sa, sb := make([]float64, n), make([]float64, n)
	for rep := 0; rep < reps; rep++ {
		for i := range sa {
			sa[i], sb[i] = 0, 0
		}
		for _, sl := range m.Slots {
			i := 0
			fmt.Sscanf(sl.Task, "t%d", &i)
			i--
			pA := simProbs[i%len(simProbs)]
			p := pA
			if sl.Arm == ArmCandidate && i%2 == 0 {
				p = math.Min(1, pA+delta)
			}
			y := bern(r, p)
			vals[sl.SlotID] = slotValue{x: y, observed: true, run: true, settled: true, val: schemas.Observed(y, schemas.EvidenceOutcome)}
			if sl.Arm == ArmCandidate {
				sb[i] += y
			} else {
				sa[i] += y
			}
		}
		rp := replaySequential(m, m.Adaptive, vals)
		switch rp.StopReason {
		case StopEfficacy:
			res.reject++
			if rp.StopLook < looks {
				res.early++
			}
		case StopFutility:
			res.futile++
		}
		for _, sl := range m.Slots {
			if !sl.Optional || rp.SlotStates[sl.SlotID] == schemas.SlotReleased {
				res.runs++
			}
		}
		res.runsFixed += len(m.Slots)
		for i := range d {
			d[i] = (sb[i] - sa[i]) / float64(trials)
		}
		if stats.SignFlip(d).P < 0.05 {
			res.rejectFixed++
		}
	}
	return res
}

func TestSimSequentialTypeIAndPower(t *testing.T) {
	reps := simReps(t, 1000)
	cases := []struct {
		n, trials, looks int
		split, futility  string
	}{
		{20, 2, 2, schemas.AlphaSplitOBF, schemas.FutilityCurtail},
		{20, 2, 2, schemas.AlphaSplitEqual, schemas.FutilityCurtail},
		{18, 2, 3, schemas.AlphaSplitOBF, schemas.FutilityCurtail},
		{20, 2, 2, schemas.AlphaSplitOBF, schemas.FutilityNonBinding},
	}
	t.Logf("sequential rule, %d replications per row, alpha 0.05, futility curtail", reps)
	t.Logf("| tasks | looks | split | futility | null: sequential | null: fixed | power +60pp on half: sequential | power: fixed | stopped early (alt) | futility stops (null) | runs sequential / fixed (null) | runs (alt) |")
	for i, c := range cases {
		null := simSequential(uint64(300+i), reps, c.n, c.trials, c.looks, c.split, 0, c.futility)
		alt := simSequential(uint64(400+i), reps, c.n, c.trials, c.looks, c.split, 0.6, c.futility)
		rate := func(k int) float64 { return float64(k) / float64(reps) }
		t.Logf("| %d | %d | %s | %s | %.4f | %.4f | %.3f | %.3f | %.3f | %.3f | %.0f%% | %.0f%% |", c.n, c.looks, c.split, c.futility,
			rate(null.reject), rate(null.rejectFixed), rate(alt.reject), rate(alt.rejectFixed), rate(alt.early), rate(null.futile),
			100*float64(null.runs)/float64(null.runsFixed), 100*float64(alt.runs)/float64(alt.runsFixed))
		if got, bound := rate(null.reject), mcBound(0.05, reps); got > bound {
			t.Errorf("%+v: type-I error %.4f above %.4f", c, got, bound)
		}
	}
}

// TestSimFutilityNeverLosesARejection checks curtailment is exact: on every
// draw where the rule stopped for futility, running every look would not
// have given a directional result at any later look.
func TestSimFutilityNeverLosesARejection(t *testing.T) {
	reps := simReps(t, 800)
	m := w7Manifest(schemas.AdaptiveSequential, 18, 0, 2, 3, schemas.AlphaSplitOBF)
	r := rand.New(rand.NewPCG(5, 6))
	vals := map[string]slotValue{}
	futile := 0
	for rep := 0; rep < reps; rep++ {
		for _, sl := range m.Slots {
			i := 0
			fmt.Sscanf(sl.Task, "t%d", &i)
			y := bern(r, simProbs[(i-1)%len(simProbs)])
			vals[sl.SlotID] = slotValue{x: y, observed: true, run: true, settled: true, val: schemas.Observed(y, schemas.EvidenceOutcome)}
		}
		rp := replaySequential(m, m.Adaptive, vals)
		if rp.StopReason != StopFutility {
			continue
		}
		futile++
		lookOf := map[string]int{}
		for k, ts := range m.Adaptive.Looks {
			for _, task := range ts {
				lookOf[task] = k + 1
			}
		}
		for k := rp.StopLook + 1; k <= len(m.Adaptive.Looks); k++ {
			e, _ := lookEffect(m, m.Adaptive, schemas.ConditionClean, lookOf, k, vals)
			if a := lookAlpha(m.Adaptive, k); improvedAtBoth(e, a) || regressedAtBoth(e, a) {
				t.Fatalf("draw %d: futility at look %d, but look %d would decide (p %v)", rep, rp.StopLook, k, *e.PValue)
			}
		}
	}
	t.Logf("futility stops checked: %d of %d draws", futile, reps)
	if futile == 0 {
		t.Fatal("no futility stop in the simulation: the check did nothing")
	}
}
