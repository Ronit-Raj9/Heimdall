package agentdiff

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// trials builds counted runs of one task from pass flags, each in its own pair.
func trials(exp string, passes ...bool) []countedRun {
	out := make([]countedRun, len(passes))
	for i, p := range passes {
		outcome := schemas.OutcomeFail
		if p {
			outcome = schemas.OutcomePass
		}
		out[i] = countedRun{exp: exp, pass: p, slot: schemas.Slot{PairID: exp + string(rune('0'+i))},
			run: schemas.Run{RunID: exp + "_r" + string(rune('0'+i)), Outcome: outcome}, metrics: map[string]schemas.Value{}}
	}
	return out
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []bool
		class    string
		wantPair bool
	}{
		{"unchanged pass", []bool{true}, []bool{true}, ClassUnchangedPass, false},
		{"unchanged fail", []bool{false, false}, []bool{false}, ClassUnchangedFail, false},
		{"flipped to pass", []bool{false, false}, []bool{true, true}, ClassFlippedToPass, true},
		{"flipped to fail", []bool{true}, []bool{false}, ClassFlippedToFail, true},
		{"unstable in both", []bool{true, false}, []bool{false, true, true}, ClassUnstableInBoth, false},
		{"moved to pass", []bool{false, false}, []bool{true, false}, ClassMovedToPass, true},
		{"moved to fail", []bool{true, false}, []bool{false, false}, ClassMovedToFail, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify("t", trials("ea", c.a...), trials("eb", c.b...))
			if got.Class != c.class || (got.Pair != nil) != c.wantPair {
				t.Fatalf("got %+v", got)
			}
			if got.Pair != nil && (got.Pair.PairID != "" || got.Pair.A.ExpID != "ea" || got.Pair.B.ExpID != "eb") {
				t.Fatalf("pair across experiments %+v", got.Pair)
			}
		})
	}
	t.Run("shared pair preferred", func(t *testing.T) {
		a, b := trials("e", false, false), trials("e", true, true)
		got := classify("t", a, b)
		if got.Pair == nil || got.Pair.PairID != "e0" {
			t.Fatalf("pair %+v", got.Pair)
		}
	})
}

func TestSignTest(t *testing.T) {
	ones := func(n int, v float64) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = v
		}
		return out
	}
	cases := []struct {
		name       string
		d          []float64
		conclusion string
	}{
		{"no tasks", nil, ConclusionCannotTell},
		{"five flips cannot reach 0.05", ones(5, 1), ConclusionCannotTell},
		{"six flips to pass", ones(6, 1), ConclusionBetter},
		{"seven flips to fail", ones(7, -1), ConclusionWorse},
		{"balanced", append(ones(4, 1), ones(4, -1)...), ConclusionNoClear},
		{"nan", []float64{math.NaN(), 1, 1, 1, 1, 1, 1}, ConclusionCannotTell},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := signTest(c.d, 0.05)
			if got.Conclusion != c.conclusion || got.NTasks != len(c.d) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestMetricDeltasAndGates(t *testing.T) {
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	a := &versionRuns{byTask: map[string][]countedRun{
		"t1": {{metrics: map[string]schemas.Value{"false_completion": obs(1), "wall_time_s": obs(10)}, fired: []string{"g2"}}},
		"t2": {{metrics: map[string]schemas.Value{"false_completion": obs(0), "wall_time_s": obs(10)}}},
	}}
	b := &versionRuns{byTask: map[string][]countedRun{
		"t1": {{metrics: map[string]schemas.Value{"false_completion": obs(0), "wall_time_s": obs(10)}, fired: []string{"g2", "g1"}}},
		"t2": {{metrics: map[string]schemas.Value{"false_completion": obs(0), "wall_time_s": schemas.Unknown("no clock", schemas.EvidenceOutcome)}, fired: []string{"g1"}}},
	}}
	common := []string{"t1", "t2"}
	ms := map[string]MetricDelta{}
	for _, m := range metricDeltas(common, a, b) {
		ms[m.Metric] = m
	}
	if fc := ms["false_completion"]; fc.Direction != DirectionBetter || fc.HigherIsBetter || *fc.Delta != -0.5 {
		t.Fatalf("false_completion %+v", fc)
	}
	if wt := ms["wall_time_s"]; wt.Direction != DirectionSame || wt.Tasks != 1 || wt.UnknownB != 1 {
		t.Fatalf("wall_time_s %+v", wt)
	}
	gates := gateChanges(common, a, b)
	if len(gates) != 2 || gates[0].Gate != "g1" || !gates[0].New || len(gates[0].Tasks) != 2 || gates[1].New || gates[1].FiredA != 1 {
		t.Fatalf("gates %+v", gates)
	}
}

func TestCompareOutcomesOnlyOneSide(t *testing.T) {
	a := &versionRuns{byTask: map[string][]countedRun{"t1": trials("e", true), "t2": trials("e", true)}}
	b := &versionRuns{byTask: map[string][]countedRun{"t1": trials("e", false), "t3": trials("e", true)}}
	o := compareOutcomes(a, b, 0.05)
	if o.CommonTasks != 1 || o.OnlyA[0] != "t2" || o.OnlyB[0] != "t3" || o.Net != -1 || o.PassRateA != 1 || o.PassRateB != 0 {
		t.Fatalf("outcome %+v", o)
	}
}
