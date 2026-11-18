package verdict

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// w7Manifest is a manifest with tasks t01..tNN, arms A and B, one clean
// condition planned at maxT trials, and the adaptive plan of rule: task
// (trials after minT optional) or sequential (looks of near-equal size, every
// task after look 1 optional).
func w7Manifest(rule string, n, minT, maxT, looks int, split string) *schemas.Manifest {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: schemas.ConditionClean, Trials: maxT}}
	tasks := make([]string, n)
	for i := range tasks {
		tasks[i] = taskID(i)
	}
	plan := &schemas.AdaptivePlan{Rule: rule, Conditions: []string{schemas.ConditionClean}, Metric: MetricVerifiedSuccess, Alpha: 0.05, Futility: schemas.FutilityCurtail}
	lookOf := map[string]int{}
	if rule == schemas.AdaptiveTask {
		plan.Min, plan.Max = minT, maxT
	} else {
		plan.Looks = SplitLooks(tasks, looks)
		sizes := make([]int, len(plan.Looks))
		for i, l := range plan.Looks {
			sizes[i] = len(l)
			for _, t := range l {
				lookOf[t] = i + 1
			}
		}
		a, err := LookAlphas(sizes, 0.05, split, nil)
		if err != nil {
			panic(err)
		}
		plan.LookAlphas = a
	}
	for trial := 1; trial <= maxT; trial++ {
		for _, task := range tasks {
			for _, arm := range []string{ArmBaseline, ArmCandidate} {
				id := fmt.Sprintf("r_%s_%s_%s_%d", task, arm, schemas.ConditionClean, trial)
				opt := (rule == schemas.AdaptiveTask && trial > minT) || (rule == schemas.AdaptiveSequential && lookOf[task] > 1)
				m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + id, PairID: fmt.Sprintf("p_%s_%s_%d", task, schemas.ConditionClean, trial),
					ExpID: "e_test", Task: task, TaskVersion: "1.0.0", Arm: arm, Condition: schemas.ConditionClean, Trial: trial, Counted: true, Optional: opt})
				if opt {
					plan.Optional++
				} else {
					plan.Required++
				}
			}
		}
	}
	m.Adaptive = plan
	return m
}

// runsFor returns sealed runs for the slots of m whose outcome outcome
// returns, skipping the slots it reports absent.
func runsFor(m *schemas.Manifest, outcome func(sl schemas.Slot) (float64, bool)) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, sl := range m.Slots {
		y, ok := outcome(sl)
		if !ok {
			continue
		}
		out = append(out, sealedRun(sl.Task, sl.Arm, sl.Condition, sl.Trial, schemas.Observed(y, schemas.EvidenceOutcome)))
	}
	return out
}

func TestReplayTaskRule(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 3, 2, 4, 0, "")
	// t01 concordant (1,1 / 1,1): stops at 2. t02 discordant: runs to 4.
	// t03 mixed in arm A at trial 2, then concordant? never: mixed stays mixed.
	val := map[string][]float64{
		"t01A": {1, 1, 1, 1}, "t01B": {1, 1, 0, 0},
		"t02A": {0, 0, 0, 0}, "t02B": {1, 1, 1, 1},
		"t03A": {1, 0, 1, 1}, "t03B": {1, 1, 1, 1},
	}
	full := runsFor(m, func(sl schemas.Slot) (float64, bool) { return val[sl.Task+sl.Arm][sl.Trial-1], true })
	rep := ReplayAdaptive(m, full)
	want := map[string]int{"t01": 2, "t02": 4, "t03": 4}
	for _, ts := range rep.Tasks {
		if ts.Trials != want[ts.Task] {
			t.Fatalf("task %s: %d trials, want %d (%+v)", ts.Task, ts.Trials, want[ts.Task], rep.Tasks)
		}
	}
	if rep.RunsSaved != 4 || rep.MaxSlots != 24 || rep.RequiredSlots != 20 {
		t.Fatalf("saved %d max %d required %d, want 4, 24, 20", rep.RunsSaved, rep.MaxSlots, rep.RequiredSlots)
	}
	if rep.StopReason != StopMaxReached {
		t.Fatalf("stop reason %q", rep.StopReason)
	}
	// Swapping the arm labels never changes the allocation.
	swapped := runsFor(m, func(sl schemas.Slot) (float64, bool) {
		other := ArmBaseline
		if sl.Arm == ArmBaseline {
			other = ArmCandidate
		}
		return val[sl.Task+other][sl.Trial-1], true
	})
	rs := ReplayAdaptive(m, swapped)
	for id, st := range rep.SlotStates {
		if rs.SlotStates[id] != st {
			t.Fatalf("slot %s: %s after the swap, %s before", id, rs.SlotStates[id], st)
		}
	}
	// An unknown in a minimum trial never lets a task stop.
	unk := append([]schemas.SealedRun(nil), full...)
	for i := range unk {
		if unk[i].Slot.Task == "t01" && unk[i].Slot.Arm == ArmCandidate && unk[i].Slot.Trial == 1 {
			unk[i].Metrics = map[string]schemas.Value{MetricVerifiedSuccess: schemas.Unknown("grader could not decide", schemas.EvidenceOutcome)}
		}
	}
	if got := ReplayAdaptive(m, unk).Tasks[0].Trials; got != 4 {
		t.Fatalf("t01 with an unknown: %d trials, want 4", got)
	}
	// At analysis, a slot with no run is an unknown: with no runs at all the
	// rule keeps going and every slot is required (and missing).
	if empty := ReplayAdaptive(m, nil); empty.RequiredSlots != 24 || empty.StopReason != StopMaxReached {
		t.Fatalf("no runs: required %d reason %s", empty.RequiredSlots, empty.StopReason)
	}
	// While running, a slot not yet attempted makes the rule wait: before
	// any run only the minimum is asked for.
	none := func(string) bool { return false }
	if w := ReplayAdaptiveSettled(m, nil, none); w.RequiredSlots != 12 || w.StopReason != StopWaiting {
		t.Fatalf("waiting: required %d reason %s", w.RequiredSlots, w.StopReason)
	}
	// With trials 1-2 in, trial 3 is released for t02 and t03, trial 4 not yet.
	two := without(full, func(r schemas.SealedRun) bool { return r.Slot.Trial > 2 })
	if w := ReplayAdaptiveSettled(m, two, none); w.RequiredSlots != 16 {
		t.Fatalf("after trial 2: required %d (%+v)", w.RequiredSlots, w.Tasks)
	}
}

func TestDecideTaskRuleSkipsNotRequiredSlots(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveTask, 8, 2, 4, 0, "")
	// t01..t06: B solves, A fails (discordant, run to 4). t07, t08 both pass (stop at 2).
	outcome := func(sl schemas.Slot) (float64, bool) {
		concordant := sl.Task == "t07" || sl.Task == "t08"
		if concordant {
			return 1, sl.Trial <= 2
		}
		if sl.Arm == ArmCandidate {
			return 1, true
		}
		return 0, true
	}
	runs := runsFor(m, outcome)
	a, err := newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	v := a.Verdict
	if v.Decision != schemas.DecisionImproved {
		t.Fatalf("decision %s, invalid %v", v.Decision, v.InvalidReasons)
	}
	if v.Adaptive == nil || v.Adaptive.RunsSaved != 8 || v.Adaptive.RunSlots != 56 {
		t.Fatalf("adaptive %+v", v.Adaptive)
	}
	if c := checkNamed(v.Instruments, InstrumentSlotCoverage); c == nil || c.Status != schemas.InstrumentPass {
		t.Fatalf("slot coverage %+v", c)
	}
	if c := checkNamed(v.Instruments, InstrumentAdaptiveAllocation); c == nil || c.Status != schemas.InstrumentPass {
		t.Fatalf("adaptive allocation %+v", c)
	}
	// A run of a slot the rule never released is left out and named.
	extra := append(runs, sealedRun("t07", ArmBaseline, schemas.ConditionClean, 3, schemas.Observed(0, schemas.EvidenceOutcome)),
		sealedRun("t07", ArmCandidate, schemas.ConditionClean, 3, schemas.Observed(1, schemas.EvidenceOutcome)))
	a2, err := newPairedSignFlipPolicy().Decide(context.Background(), m, extra)
	if err != nil {
		t.Fatal(err)
	}
	if c := checkNamed(a2.Verdict.Instruments, InstrumentAdaptiveAllocation); c == nil || c.Status != schemas.InstrumentWarn || len(a2.Verdict.Adaptive.Excluded) != 2 {
		t.Fatalf("extra runs: %+v %+v", c, a2.Verdict.Adaptive)
	}
	if a2.Verdict.Effect.DBar != v.Effect.DBar {
		t.Fatalf("extra runs moved d_bar %v -> %v", v.Effect.DBar, a2.Verdict.Effect.DBar)
	}
	// A released slot with no run is missing, not "not required".
	short := without(runs, func(r schemas.SealedRun) bool { return r.Slot.Task == "t01" && r.Slot.Trial == 4 })
	a3, err := newPairedSignFlipPolicy().Decide(context.Background(), m, short)
	if err != nil {
		t.Fatal(err)
	}
	if c := checkNamed(a3.Verdict.Instruments, InstrumentSlotCoverage); c == nil || c.Status != schemas.InstrumentWarn {
		t.Fatalf("a missing released slot: %+v", c)
	}
}

func TestReplaySequentialStops(t *testing.T) {
	m := w7Manifest(schemas.AdaptiveSequential, 16, 0, 2, 2, schemas.AlphaSplitEqual)
	// Every look-1 task: B passes both, A fails both. 8 of 8 discordant one
	// way: p = 2/256 < 0.025, efficacy at look 1.
	strong := runsFor(m, func(sl schemas.Slot) (float64, bool) {
		if sl.Optional {
			return 0, false
		}
		return map[bool]float64{true: 1, false: 0}[sl.Arm == ArmCandidate], true
	})
	rep := ReplayAdaptive(m, strong)
	if rep.StopReason != StopEfficacy || rep.StopLook != 1 || rep.RunsSaved != 32 {
		t.Fatalf("strong: %+v", rep)
	}
	a, err := newPairedSignFlipPolicy().Decide(context.Background(), m, strong)
	if err != nil {
		t.Fatal(err)
	}
	if a.Verdict.Decision != schemas.DecisionImproved || a.Verdict.Adaptive.DecisionAlpha != 0.025 {
		t.Fatalf("strong verdict %s alpha %v reasons %v", a.Verdict.Decision, a.Verdict.Adaptive.DecisionAlpha, a.Verdict.InvalidReasons)
	}
	// Every look-1 task concordant: 8 more tasks at +1 give p = 2/256 <
	// 0.025, so not futile; look 2 is released.
	flat := runsFor(m, func(sl schemas.Slot) (float64, bool) { return 1, !sl.Optional })
	ran := map[string]bool{}
	for _, r := range flat {
		ran[r.Slot.SlotID] = true
	}
	if rep := ReplayAdaptiveSettled(m, flat, func(id string) bool { return ran[id] }); rep.StopReason != StopWaiting || rep.RunsSaved != 0 || rep.StopLook != 2 {
		t.Fatalf("flat: %+v", rep)
	}
	// Look 1 split 4 up, 4 down at 1.0: 8 more at +1 give sum 8 over 16
	// unit differences; p >= 0.025, so futile.
	split := runsFor(m, func(sl schemas.Slot) (float64, bool) {
		if sl.Optional {
			return 0, false
		}
		up := sl.Task <= "t04"
		return map[bool]float64{true: 1, false: 0}[(sl.Arm == ArmCandidate) == up], true
	})
	rep = ReplayAdaptive(m, split)
	if rep.StopReason != StopFutility || rep.RunsSaved != 32 {
		t.Fatalf("split: %+v", rep.LookResults)
	}
	if best := *rep.LookResults[0].BestCaseP; best < 0.025 {
		t.Fatalf("futile with best-case p %v", best)
	}
}

// TestBestCasePExact checks the lemma behind the futility bound: over every
// completion on a grid that includes -1 and +1, the smallest sign-flip p is
// the one BestCaseP returns.
func TestBestCasePExact(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	grid := []float64{-1, -0.75, -0.5, -0.25, 0, 0.25, 0.5, 0.75, 1}
	for iter := 0; iter < 150; iter++ {
		n := 2 + r.IntN(5)
		d := make([]float64, n)
		for i := range d {
			if r.IntN(2) == 0 {
				d[i] = grid[r.IntN(len(grid))]
			} else {
				d[i] = 2*r.Float64() - 1
			}
		}
		more := 1 + r.IntN(3)
		brute := 1.0
		idx := make([]int, more)
		for {
			x := append([]float64(nil), d...)
			for _, k := range idx {
				x = append(x, grid[k])
			}
			brute = math.Min(brute, stats.SignFlip(x).P)
			i := 0
			for ; i < more; i++ {
				idx[i]++
				if idx[i] < len(grid) {
					break
				}
				idx[i] = 0
			}
			if i == more {
				break
			}
		}
		if got := BestCaseP(d, more); math.Abs(got-brute) > 1e-12 {
			t.Fatalf("d=%v more=%d: BestCaseP %v, brute force %v", d, more, got, brute)
		}
	}
}

func TestLookAlphas(t *testing.T) {
	eq, err := LookAlphas([]int{8, 8}, 0.05, schemas.AlphaSplitEqual, nil)
	if err != nil || eq[0] != 0.025 || eq[1] != 0.025 {
		t.Fatalf("equal %v %v", eq, err)
	}
	obf, err := LookAlphas([]int{8, 8}, 0.05, schemas.AlphaSplitOBF, nil)
	if err != nil || math.Abs(obf[0]+obf[1]-0.05) > 1e-12 || obf[0] > 0.006 || obf[0] < 0.005 {
		t.Fatalf("obf %v %v", obf, err)
	}
	if _, err := LookAlphas([]int{8, 8}, 0.05, "", []float64{0.03, 0.03}); err == nil {
		t.Fatal("explicit alphas above alpha accepted")
	}
	if _, err := LookAlphas([]int{8}, 0.05, "", nil); err == nil {
		t.Fatal("one look accepted")
	}
}

func TestMoreEvidenceOnInconclusive(t *testing.T) {
	clean := schemas.ConditionClean
	a := passes(clean, ArmBaseline, 3, 3, 3, 0, 2, 0, 2, 1, 3)
	b := passes(clean, ArmCandidate, 3, 3, 3, 1, 3, 0, 3, 2, 3)
	runs := concat(a, b)
	for i := range runs {
		runs[i].Metrics[MetricCostUSD] = schemas.Observed(0.5, schemas.EvidenceGateway)
	}
	m := withSlots(testManifest(ArmBaseline, ArmCandidate), runs)
	an, err := newPairedSignFlipPolicy().Decide(context.Background(), m, runs)
	if err != nil {
		t.Fatal(err)
	}
	me := an.Verdict.MoreEvidence
	if an.Verdict.Decision != schemas.DecisionInconclusive || me == nil {
		t.Fatalf("decision %s more %+v", an.Verdict.Decision, me)
	}
	if me.TasksTotal != an.Verdict.TasksNeeded || me.Tasks != me.TasksTotal-8 || me.CostUSD == nil || *me.CostUSD != float64(me.Tasks*6)*0.5 {
		t.Fatalf("more evidence %+v (tasks needed %d)", me, an.Verdict.TasksNeeded)
	}
	if me.Combination != schemas.ExtensionExploratory || me.Command == "" {
		t.Fatalf("combination %q command %q", me.Combination, me.Command)
	}
	m.Experiment.Extends = &schemas.ExtendsRef{ExpID: "e_first", ManifestHash: "h", Kind: "tasks", Rule: schemas.ExtensionExploratory}
	if an, err = newPairedSignFlipPolicy().Decide(context.Background(), m, runs); err != nil || an.Verdict.MoreEvidence.Command != "" {
		t.Fatalf("a follow-up was offered another extend: %+v %v", an.Verdict.MoreEvidence, err)
	}
}
