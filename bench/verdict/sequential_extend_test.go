package verdict

import (
	"context"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// extendPair is a parent with n tasks and a follow-up with m new tasks,
// every run's outcome from out.
func extendPair(n, m int, declared bool, out func(task, arm string, trial int) float64) (*schemas.Manifest, []schemas.SealedRun, *schemas.Manifest, []schemas.SealedRun) {
	build := func(id string, first, count int) (*schemas.Manifest, []schemas.SealedRun) {
		mf := testManifest(ArmBaseline, ArmCandidate)
		mf.ExpID, mf.Hash = id, "h_"+id
		var runs []schemas.SealedRun
		for i := first; i < first+count; i++ {
			for _, arm := range []string{ArmBaseline, ArmCandidate} {
				for trial := 1; trial <= 2; trial++ {
					r := sealedRun(taskID(i), arm, schemas.ConditionClean, trial, schemas.Observed(out(taskID(i), arm, trial), schemas.EvidenceOutcome))
					r.Slot.ExpID, r.Run.ManifestHash = id, mf.Hash
					runs = append(runs, r)
					mf.Slots = append(mf.Slots, r.Slot)
				}
			}
		}
		return mf, runs
	}
	p, pr := build("e_parent", 0, n)
	if declared {
		p.Experiment.Trials = &schemas.TrialsPolicy{Mode: schemas.TrialsFixed, Extension: &schemas.ExtensionRule{Rule: schemas.ExtensionInverseNormal}}
	}
	c, cr := build("e_child", n, m)
	rule := schemas.ExtensionExploratory
	if declared {
		rule = schemas.ExtensionInverseNormal
	}
	c.Experiment.Extends = &schemas.ExtendsRef{ExpID: p.ExpID, ManifestHash: p.Hash, Kind: "tasks", Rule: rule}
	return p, pr, c, cr
}

func TestCombineExtension(t *testing.T) {
	// B passes every trial on 5 of the parent's 8 tasks and on 6 of the
	// child's 8; A fails those; the rest pass in both arms.
	out := func(task, arm string, _ int) float64 {
		strong := map[string]bool{"t01": true, "t02": true, "t03": true, "t04": true, "t05": true,
			"t09": true, "t10": true, "t11": true, "t12": true, "t13": true, "t14": true}
		if strong[task] {
			return map[bool]float64{true: 1, false: 0}[arm == ArmCandidate]
		}
		return 1
	}
	p, pr, c, cr := extendPair(8, 8, true, out)
	res, err := CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInconclusive)
	if err != nil {
		t.Fatal(err)
	}
	// Stage 1: 5 of 5 discordant up, one-sided p 1/32; stage 2: 6 of 6, 1/64.
	if math.Abs(res.Stages[0].PImproveLow-1.0/32) > 1e-12 || math.Abs(res.Stages[1].PImproveLow-1.0/64) > 1e-12 {
		t.Fatalf("stage p %+v", res.Stages)
	}
	if res.Decision != schemas.DecisionImproved || res.Exploratory || res.Alpha != 0.025 {
		t.Fatalf("combined %+v", res)
	}
	// Without a pre-declared rule the same data is exploratory.
	p2, pr2, c2, cr2 := extendPair(8, 8, false, out)
	res2, err := CombineExtension(p2, pr2, schemas.DecisionInconclusive, c2, cr2, schemas.DecisionInconclusive)
	if err != nil || !res2.Exploratory || res2.Decision != schemas.DecisionInconclusive {
		t.Fatalf("exploratory %+v %v", res2, err)
	}
	// A follow-up pointing at another manifest hash is refused.
	c.Experiment.Extends.ManifestHash = "other"
	if _, err := CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInconclusive); err == nil {
		t.Fatal("wrong parent hash accepted")
	}
}

// TestSimExtensionTypeI draws a parent under the null, lets an adversarial
// analyst size the follow-up from the parent's direction (6 tasks when B
// leads, 12 otherwise), and checks that the parent test at alpha_first plus
// the combined test stays at alpha. Naive pooling (test the parent at 0.05,
// then the pooled tasks at 0.05) is printed beside it: it has no guarantee,
// though the discrete sign-flip test keeps it low at these sizes.
func TestSimExtensionTypeI(t *testing.T) {
	reps := simReps(t, 3000)
	r := rand.New(rand.NewPCG(31, 37))
	const n1, trials = 10, 2
	a1, a2 := 0.025, 0.025
	w := math.Sqrt(0.5)
	crit := normalQuantile(1 - a2/2)
	task := func(i int) float64 {
		p := simProbs[i%len(simProbs)]
		sa, sb := 0.0, 0.0
		for k := 0; k < trials; k++ {
			sa += bern(r, p)
			sb += bern(r, p)
		}
		return (sb - sa) / trials
	}
	combined, naive := 0, 0
	for rep := 0; rep < reps; rep++ {
		d1 := make([]float64, n1)
		for i := range d1 {
			d1[i] = task(i)
		}
		p1 := stats.SignFlip(d1).P
		if p1 < 0.05 {
			naive++
		}
		if p1 < a1 {
			combined++
			continue
		}
		n2 := 12
		if s := sumOf(d1); s > 0 {
			n2 = 6
		}
		d2 := make([]float64, n2)
		for i := range d2 {
			d2[i] = task(n1 + i)
		}
		zi := combineZ(w, w, oneSidedP(d1, 1), oneSidedP(d2, 1))
		zr := combineZ(w, w, oneSidedP(d1, -1), oneSidedP(d2, -1))
		if zi >= crit || zr >= crit {
			combined++
		}
		if p1 >= 0.05 && stats.SignFlip(append(append([]float64(nil), d1...), d2...)).P < 0.05 {
			naive++
		}
	}
	rate := func(k int) float64 { return float64(k) / float64(reps) }
	t.Logf("extension, %d replications, null, data-dependent follow-up size: pre-declared inverse normal %.4f, naive pooling %.4f (alpha 0.05)", reps, rate(combined), rate(naive))
	if rate(combined) > mcBound(0.05, reps) {
		t.Errorf("combined type-I error %.4f above %.4f", rate(combined), mcBound(0.05, reps))
	}
}

func sumOf(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s
}

func TestCombineExtensionInvalidAndRegressed(t *testing.T) {
	// B fails where A passes on 5 of 8 parent tasks and 6 of 8 child tasks.
	out := func(task, arm string, _ int) float64 {
		weak := map[string]bool{"t01": true, "t02": true, "t03": true, "t04": true, "t05": true,
			"t09": true, "t10": true, "t11": true, "t12": true, "t13": true, "t14": true}
		if weak[task] {
			return map[bool]float64{true: 0, false: 1}[arm == ArmCandidate]
		}
		return 1
	}
	p, pr, c, cr := extendPair(8, 8, true, out)
	res, err := CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInconclusive)
	if err != nil || res.Decision != schemas.DecisionRegressed {
		t.Fatalf("regressed: %+v %v", res, err)
	}
	res, err = CombineExtension(p, pr, schemas.DecisionInconclusive, c, cr, schemas.DecisionInvalid)
	if err != nil || res.Decision != schemas.DecisionInvalid {
		t.Fatalf("invalid stage: %+v %v", res, err)
	}
}

func TestExtensionStageNeverDecidesAlone(t *testing.T) {
	clean := schemas.ConditionClean
	runs := concat(passes(clean, ArmBaseline, 3, 0, 0, 0, 0, 0, 0, 0, 0), passes(clean, ArmCandidate, 3, 3, 3, 3, 3, 3, 3, 3, 3))
	m := withSlots(testManifest(ArmBaseline, ArmCandidate), runs)
	m.Experiment.Extends = &schemas.ExtendsRef{ExpID: "e_first", ManifestHash: "h", Kind: "tasks", Rule: schemas.ExtensionInverseNormal}
	an, err := newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	if an.Verdict.Decision != schemas.DecisionInconclusive || checkNamed(an.Verdict.Instruments, InstrumentExtensionStage) == nil {
		t.Fatalf("a stage-2 experiment decided alone: %s", an.Verdict.Decision)
	}
	m.Experiment.Extends.Rule = schemas.ExtensionExploratory
	an, err = newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil || an.Verdict.Decision != schemas.DecisionImproved {
		t.Fatalf("an exploratory follow-up is its own experiment: %s %v", an.Verdict.Decision, err)
	}
}
