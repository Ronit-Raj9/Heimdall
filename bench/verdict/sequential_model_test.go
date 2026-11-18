package verdict

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A fast model of the sequential rule for suites above the 22 discordant
// tasks the exact enumeration covers (stats.SignFlip switches to a million
// Monte Carlo draws there, too slow for thousands of simulated experiments).
// With T trials per arm every task difference is k/T for an integer k, so the
// exact sign-flip distribution is a convolution over integer sums. The model
// is checked against the real replay on suites the enumeration covers
// (TestSequentialModelMatchesReplay), then used for 40 and 60 tasks.

// latticeP is the exact two-sided sign-flip p-value of differences k/T.
func latticeP(k []int) float64 {
	total, obs := 0, 0
	for _, x := range k {
		total += abs(x)
		obs += x
	}
	if obs == 0 {
		return 1
	}
	dist := make([]float64, 2*total+1)
	dist[total] = 1
	span := 0
	for _, x := range k {
		a := abs(x)
		if a == 0 {
			continue
		}
		next := make([]float64, len(dist))
		for s := total - span; s <= total+span; s++ {
			if p := dist[s]; p != 0 {
				next[s+a] += p / 2
				next[s-a] += p / 2
			}
		}
		dist, span = next, span+a
	}
	p := 0.0
	for s, w := range dist {
		if abs(s-total) >= abs(obs) {
			p += w
		}
	}
	return math.Min(1, p)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// modelSequential applies the rule to per-task integer differences in look
// order and returns the stop look and reason.
func modelSequential(k []int, trials int, sizes []int, alphas []float64, futility string) (int, string) {
	cum := 0
	for look := 1; look <= len(sizes); look++ {
		cum += sizes[look-1]
		p := latticeP(k[:cum])
		if p < alphas[look-1] {
			return look, StopEfficacy
		}
		if look == len(sizes) {
			return look, StopFinalLook
		}
		if futility == schemas.FutilityNonBinding && p >= schemas.NonBindingP {
			return look, StopFutility
		}
		futile, more := true, 0
		for j := look + 1; j <= len(sizes); j++ {
			more += sizes[j-1]
			up := append(append([]int(nil), k[:cum]...), fillInt(more, trials)...)
			down := append(append([]int(nil), k[:cum]...), fillInt(more, -trials)...)
			if math.Min(latticeP(up), latticeP(down)) < alphas[j-1] {
				futile = false
				break
			}
		}
		if futile {
			return look, StopFutility
		}
	}
	return len(sizes), StopFinalLook
}

func fillInt(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// drawK draws per-task integer differences: trials Bernoulli runs per arm.
func drawK(r *rand.Rand, n, trials int, delta float64) []int {
	k := make([]int, n)
	for i := range k {
		pA := simProbs[i%len(simProbs)]
		pB := pA
		if i%2 == 0 {
			pB = math.Max(0, math.Min(1, pA+delta))
		}
		for t := 0; t < trials; t++ {
			k[i] += int(bern(r, pB)) - int(bern(r, pA))
		}
	}
	return k
}

func TestSequentialModelMatchesReplay(t *testing.T) {
	for _, fut := range []string{schemas.FutilityCurtail, schemas.FutilityNonBinding} {
		testModelMatches(t, fut)
	}
}

func testModelMatches(t *testing.T, futility string) {
	m := w7Manifest(schemas.AdaptiveSequential, 18, 0, 2, 3, schemas.AlphaSplitOBF)
	m.Adaptive.Futility = futility
	sizes := make([]int, len(m.Adaptive.Looks))
	for i, l := range m.Adaptive.Looks {
		sizes[i] = len(l)
	}
	r := rand.New(rand.NewPCG(41, 43))
	vals := map[string]slotValue{}
	for rep := 0; rep < simReps(t, 400); rep++ {
		k := drawK(r, 18, 2, 0.4*float64(rep%2))
		for _, sl := range m.Slots {
			i := 0
			fmt.Sscanf(sl.Task, "t%d", &i)
			y := 0.0
			// Arm B passes k[i] more trials than A: A passes trials 1..max(0,-k), B 1..max(0,k).
			kk := k[i-1]
			if (sl.Arm == ArmCandidate && sl.Trial <= kk) || (sl.Arm == ArmBaseline && sl.Trial <= -kk) {
				y = 1
			}
			vals[sl.SlotID] = slotValue{x: y, observed: true, run: true, settled: true, val: schemas.Observed(y, schemas.EvidenceOutcome)}
		}
		rp := replaySequential(m, m.Adaptive, vals)
		look, reason := modelSequential(k, 2, sizes, m.Adaptive.LookAlphas, futility)
		if look != rp.StopLook || reason != rp.StopReason {
			t.Fatalf("draw %d k=%v: model %s at %d, replay %s at %d", rep, k, reason, look, rp.StopReason, rp.StopLook)
		}
	}
}

func TestSimSequentialLargeSuites(t *testing.T) {
	reps := simReps(t, 2000)
	cases := []struct {
		n, looks        int
		split, futility string
	}{
		{40, 2, schemas.AlphaSplitOBF, schemas.FutilityCurtail}, {40, 2, schemas.AlphaSplitEqual, schemas.FutilityCurtail}, {60, 3, schemas.AlphaSplitOBF, schemas.FutilityCurtail},
		{20, 2, schemas.AlphaSplitOBF, schemas.FutilityNonBinding}, {40, 2, schemas.AlphaSplitOBF, schemas.FutilityNonBinding}, {60, 3, schemas.AlphaSplitOBF, schemas.FutilityNonBinding},
	}
	const trials = 2
	t.Logf("sequential rule, model (checked against the replay), %d replications, 2 trials per arm, futility curtail", reps)
	t.Logf("| tasks | looks | split | futility | null: sequential | null: fixed | power +30pp on half: sequential | fixed | stopped early (alt) | futility stops (null) | tasks run / planned (null) | (alt) |")
	for ci, c := range cases {
		sizes := make([]int, c.looks)
		for i := range sizes {
			sizes[i] = c.n / c.looks
		}
		alphas, err := LookAlphas(sizes, 0.05, c.split, nil)
		if err != nil {
			t.Fatal(err)
		}
		var nullRej, nullFix, nullFut, nullTasks, altRej, altFix, altEarly, altTasks int
		r := rand.New(rand.NewPCG(uint64(700+ci), 9))
		for rep := 0; rep < reps; rep++ {
			for _, alt := range []bool{false, true} {
				delta := 0.0
				if alt {
					delta = 0.3
				}
				k := drawK(r, c.n, trials, delta)
				look, reason := modelSequential(k, trials, sizes, alphas, c.futility)
				run := 0
				for i := 0; i < look; i++ {
					run += sizes[i]
				}
				fixed := latticeP(k) < 0.05
				if alt {
					altTasks += run
					if reason == StopEfficacy {
						altRej++
						if look < c.looks {
							altEarly++
						}
					}
					if fixed {
						altFix++
					}
				} else {
					nullTasks += run
					if reason == StopEfficacy {
						nullRej++
					}
					if reason == StopFutility {
						nullFut++
					}
					if fixed {
						nullFix++
					}
				}
			}
		}
		rate := func(k int) float64 { return float64(k) / float64(reps) }
		t.Logf("| %d | %d | %s | %s | %.4f | %.4f | %.3f | %.3f | %.3f | %.3f | %.0f%% | %.0f%% |", c.n, c.looks, c.split, c.futility,
			rate(nullRej), rate(nullFix), rate(altRej), rate(altFix), rate(altEarly), rate(nullFut),
			100*float64(nullTasks)/float64(reps*c.n), 100*float64(altTasks)/float64(reps*c.n))
		if got, bound := rate(nullRej), mcBound(0.05, reps); got > bound {
			t.Errorf("%+v: type-I error %.4f above %.4f", c, got, bound)
		}
	}
}
