package agentdiff

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

// Run files the outcome diff reads.
const (
	fileMetrics = "metrics.json"
	fileGates   = "gates.json"
	fileLedger  = "proxy.ledger.jsonl"
)

// Exclusion reasons that are not an outcome. They match the leaderboard's.
const (
	ReasonNotSealed      = "not sealed"
	ReasonSealMismatch   = "seal mismatch"
	ReasonDuplicateSlot  = "slot has more than one counted run"
	ReasonVerdictInvalid = "experiment verdict invalid"
	ReasonExternalLive   = "external APIs live (not repeatable)"
	ReasonSynthetic      = "synthetic (scripted agent, scripted model or smoke experiment)"
	reasonNoOutcome      = "no outcome"
)

// DeltaMetrics are the metrics the outcome diff compares, in report order.
var DeltaMetrics = []string{
	verdict.MetricCostUSD, verdict.MetricTokens, verdict.MetricSteps, verdict.MetricWallTime, verdict.MetricFalseCompletion,
}

// countedRun is one verified, graded run of a version.
type countedRun struct {
	exp     string
	slot    schemas.Slot
	run     schemas.Run
	pass    bool
	metrics map[string]schemas.Value
	fired   []string // gates that fired
}

// versionRuns is what a version contributed on the suite and condition.
type versionRuns struct {
	byTask      map[string][]countedRun
	experiments []string
	runs        int
	excluded    map[string]int
}

// candidate is one slot's counted run before it is verified and graded.
type candidate struct {
	exp     string
	m       *schemas.Manifest
	arm     schemas.ArmManifest
	slot    schemas.Slot
	run     schemas.Run
	sealed  bool
	counted int
}

// collectRuns reads a version's runs on suite and condition: every arm that
// resolved to its hash (only its own arm when picked by <exp_id>:<arm>),
// each slot's counted run picked as the store does, then excluded or
// verified and graded. Exclusions are counted by reason.
func collectRuns(ctx context.Context, src Source, arms []*armRecord, v resolved, suite, condition string, synthetic bool) (*versionRuns, error) {
	out := &versionRuns{byTask: map[string][]countedRun{}, excluded: map[string]int{}}
	decisions, _ := src.(DecisionSource)
	expSeen := map[string]bool{}
	for _, a := range arms {
		if a.hash != v.hash || a.ref.Suite != suite || (v.expID != "" && (a.ref.ExpID != v.expID || a.ref.Arm != v.arm)) {
			continue
		}
		cands := candidates(src, a, condition)
		if len(cands) == 0 {
			continue
		}
		invalid := false
		if decisions != nil {
			d, ok := decisions.LatestDecision(a.ref.ExpID)
			invalid = ok && d == schemas.DecisionInvalid
		}
		isSynthetic, err := syntheticArm(src, a, cands)
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("agent diff: %w", err)
			}
			reason := excludeReason(c, invalid, isSynthetic && !synthetic)
			if reason == "" {
				if reason, err = sealReason(ctx, src, c); err != nil {
					return nil, err
				}
			}
			if reason != "" {
				out.excluded[reason]++
				continue
			}
			cr, err := gradeRun(src, c)
			if err != nil {
				return nil, err
			}
			out.byTask[c.slot.Task] = append(out.byTask[c.slot.Task], cr)
			out.runs++
			if !expSeen[c.exp] {
				expSeen[c.exp] = true
				out.experiments = append(out.experiments, c.exp)
			}
		}
	}
	slices.Sort(out.experiments)
	return out, nil
}

// candidates returns the counted run of every slot of an arm on condition:
// the first sealed run that is not aborted_infra, else the last attempt.
// Slots with no run are planned work and are skipped.
func candidates(src Source, a *armRecord, condition string) []candidate {
	bySlot := map[string][]Run{}
	var ids []string
	for _, r := range src.Runs(a.ref.ExpID) {
		if r.Slot.Arm != a.ref.Arm || r.Slot.Condition != condition || !r.Slot.Counted {
			continue
		}
		if _, ok := bySlot[r.Slot.SlotID]; !ok {
			ids = append(ids, r.Slot.SlotID)
		}
		bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
	}
	slices.Sort(ids)
	out := make([]candidate, 0, len(ids))
	for _, id := range ids {
		runs := bySlot[id]
		slices.SortStableFunc(runs, func(x, y Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
		counts := func(r Run) bool { return r.Sealed && r.Run.Outcome != schemas.OutcomeAbortedInfra && !r.Superseded }
		pick := runs[len(runs)-1]
		if i := slices.IndexFunc(runs, counts); i >= 0 {
			pick = runs[i]
		}
		c := candidate{exp: a.ref.ExpID, m: a.m, arm: a.arm, slot: pick.Slot, run: pick.Run, sealed: pick.Sealed}
		for _, r := range runs {
			if counts(r) {
				c.counted++
			}
		}
		out = append(out, c)
	}
	return out
}

// excludeReason returns why a candidate does not count, or "" when it may be
// verified and graded. An invalid verdict wins: the experiment's instrument
// failed, so none of its runs are evidence.
func excludeReason(c candidate, invalid, synthetic bool) string {
	switch {
	case invalid:
		return ReasonVerdictInvalid
	case utils.ExternalUnrepeatable(c.m):
		return ReasonExternalLive
	case synthetic:
		return ReasonSynthetic
	case !c.sealed:
		return ReasonNotSealed
	case c.counted > 1:
		return ReasonDuplicateSlot
	}
	if _, ok := c.run.Outcome.Score(); ok {
		return ""
	}
	if c.run.Outcome == "" {
		return reasonNoOutcome
	}
	return string(c.run.Outcome)
}

// sealReason verifies a sealed run through the source: ReasonSealMismatch
// when its files no longer match its seal, "" when they do.
func sealReason(ctx context.Context, src Source, c candidate) (string, error) {
	err := src.VerifySeal(ctx, c.run.RunID)
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, schemas.ErrSealMismatch):
		return ReasonSealMismatch, nil
	default:
		return "", fmt.Errorf("agent diff: verify seal of run %s: %w", c.run.RunID, err)
	}
}

// gradeRun reads a verified run's metrics and gates. A run passes when its
// verified_success metric is at least 0.5, or, without the metric, when its
// outcome scores 1: intention to treat, so a timeout or crash is a failure.
func gradeRun(src Source, c candidate) (countedRun, error) {
	cr := countedRun{exp: c.exp, slot: c.slot, run: c.run, metrics: map[string]schemas.Value{}}
	if data, found, err := readRunFile(src, c.run.RunID, fileMetrics); err != nil {
		return cr, err
	} else if found {
		if err := json.Unmarshal(data, &cr.metrics); err != nil {
			return cr, fmt.Errorf("parse %s of run %s: %w", fileMetrics, c.run.RunID, err)
		}
	}
	y, _ := c.run.Outcome.Score()
	cr.pass = y >= 0.5
	if v, ok := cr.metrics[verdict.MetricVerifiedSuccess].Float(); ok {
		cr.pass = v >= 0.5
	}
	data, found, err := readRunFile(src, c.run.RunID, fileGates)
	if err != nil {
		return cr, err
	}
	if found {
		var gates []schemas.GateResult
		if err := json.Unmarshal(data, &gates); err != nil {
			return cr, fmt.Errorf("parse %s of run %s: %w", fileGates, c.run.RunID, err)
		}
		for _, g := range gates {
			if g.Fired {
				cr.fired = append(cr.fired, g.Gate)
			}
		}
	}
	return cr, nil
}

// readRunFile reads one run file. A missing file returns found false.
func readRunFile(src Source, runID, name string) ([]byte, bool, error) {
	data, err := src.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, true, nil
}

// compareOutcomes builds the outcome half over the tasks both versions ran.
func compareOutcomes(a, b *versionRuns, alpha float64) OutcomeDiff {
	out := OutcomeDiff{OnlyA: []string{}, OnlyB: []string{}, Tasks: []TaskOutcome{}, Metrics: []MetricDelta{}, Gates: []GateChange{}}
	var common []string
	for task := range a.byTask {
		if _, ok := b.byTask[task]; ok {
			common = append(common, task)
		} else {
			out.OnlyA = append(out.OnlyA, task)
		}
	}
	for task := range b.byTask {
		if _, ok := a.byTask[task]; !ok {
			out.OnlyB = append(out.OnlyB, task)
		}
	}
	slices.Sort(common)
	slices.Sort(out.OnlyA)
	slices.Sort(out.OnlyB)
	out.CommonTasks = len(common)
	d := make([]float64, 0, len(common))
	for _, task := range common {
		t := classify(task, a.byTask[task], b.byTask[task])
		countClass(&out.Counts, t.Class)
		out.PassRateA += t.RateA
		out.PassRateB += t.RateB
		d = append(d, t.RateB-t.RateA)
		out.Tasks = append(out.Tasks, t)
	}
	if n := len(common); n > 0 {
		out.PassRateA /= float64(n)
		out.PassRateB /= float64(n)
	}
	slices.SortStableFunc(out.Tasks, func(x, y TaskOutcome) int {
		return cmp.Or(cmp.Compare(classRank(x.Class), classRank(y.Class)), cmp.Compare(x.Task, y.Task))
	})
	out.Net = out.Counts.FlippedToPass - out.Counts.FlippedToFail
	out.Test = signTest(d, alpha)
	out.Metrics = metricDeltas(common, a, b)
	out.Gates = gateChanges(common, a, b)
	return out
}

// classify places one common task in its class and picks a representative
// run pair for a flip or a move: a failing A run and a passing B run for a
// change toward pass, the other way round toward fail.
func classify(task string, ra, rb []countedRun) TaskOutcome {
	t := TaskOutcome{Task: task, NA: len(ra), NB: len(rb)}
	for _, r := range ra {
		if r.pass {
			t.PassA++
		}
	}
	for _, r := range rb {
		if r.pass {
			t.PassB++
		}
	}
	t.RateA, t.RateB = float64(t.PassA)/float64(t.NA), float64(t.PassB)/float64(t.NB)
	stableA, stableB := t.PassA == 0 || t.PassA == t.NA, t.PassB == 0 || t.PassB == t.NB
	switch {
	case stableA && stableB && t.RateA == t.RateB && t.RateA == 1:
		t.Class = ClassUnchangedPass
	case stableA && stableB && t.RateA == t.RateB:
		t.Class = ClassUnchangedFail
	case stableA && stableB && t.RateB > t.RateA:
		t.Class = ClassFlippedToPass
	case stableA && stableB:
		t.Class = ClassFlippedToFail
	case !stableA && !stableB:
		t.Class = ClassUnstableInBoth
	case t.RateB > t.RateA:
		t.Class = ClassMovedToPass
	default:
		t.Class = ClassMovedToFail
	}
	switch t.Class {
	case ClassFlippedToPass, ClassMovedToPass:
		t.Pair = pickPair(ra, rb, false, true)
	case ClassFlippedToFail, ClassMovedToFail:
		t.Pair = pickPair(ra, rb, true, false)
	}
	return t
}

// pickPair returns the first A run with pass wantA and the first B run with
// pass wantB, preferring two runs of the same experiment pair.
func pickPair(ra, rb []countedRun, wantA, wantB bool) *RunPair {
	var firstA, firstB *countedRun
	for i := range ra {
		if ra[i].pass != wantA {
			continue
		}
		if firstA == nil {
			firstA = &ra[i]
		}
		for j := range rb {
			if rb[j].pass == wantB && rb[j].slot.PairID != "" && rb[j].slot.PairID == ra[i].slot.PairID {
				return &RunPair{A: ref(ra[i]), B: ref(rb[j]), PairID: ra[i].slot.PairID}
			}
		}
	}
	for j := range rb {
		if rb[j].pass == wantB {
			firstB = &rb[j]
			break
		}
	}
	if firstA == nil || firstB == nil {
		return nil
	}
	return &RunPair{A: ref(*firstA), B: ref(*firstB)}
}

// ref points at a counted run.
func ref(r countedRun) RunRef {
	pair := r.slot.PairID
	if pair == "" {
		pair = r.run.PairID
	}
	return RunRef{RunID: r.run.RunID, ExpID: r.exp, PairID: pair, Outcome: string(r.run.Outcome)}
}

// classRank orders classes for the task table: flips, moves, unstable,
// unchanged.
func classRank(c string) int {
	return slices.Index([]string{ClassFlippedToFail, ClassFlippedToPass, ClassMovedToFail, ClassMovedToPass, ClassUnstableInBoth, ClassUnchangedFail, ClassUnchangedPass}, c)
}

// countClass adds one task to its class count.
func countClass(c *ClassCounts, class string) {
	switch class {
	case ClassFlippedToPass:
		c.FlippedToPass++
	case ClassFlippedToFail:
		c.FlippedToFail++
	case ClassUnchangedPass:
		c.UnchangedPass++
	case ClassUnchangedFail:
		c.UnchangedFail++
	case ClassUnstableInBoth:
		c.UnstableInBoth++
	case ClassMovedToPass:
		c.MovedToPass++
	case ClassMovedToFail:
		c.MovedToFail++
	}
}

// signTest runs the exact paired sign-flip test on per-task differences. It
// cannot tell when the changed tasks are too few for any p below alpha
// (stats.MinPossibleP): then the result says so instead of reading a large p
// as "no difference".
func signTest(d []float64, alpha float64) SignTest {
	t := SignTest{NTasks: len(d), Alpha: alpha, Exact: true, MinPossibleP: 1}
	if len(d) == 0 {
		t.Conclusion = ConclusionCannotTell
		return t
	}
	r := stats.SignFlip(d)
	p := r.P
	t.P, t.Exact = &p, r.Exact
	t.NNonzero, t.NPositive, t.NNegative = r.NNonzero, r.NPositive, r.NNegative
	t.MinPossibleP = stats.MinPossibleP(r.NNonzero)
	switch {
	case math.IsNaN(p) || t.MinPossibleP > alpha:
		t.Conclusion = ConclusionCannotTell
	case p < alpha && r.NPositive > r.NNegative:
		t.Conclusion = ConclusionBetter
	case p < alpha && r.NNegative > r.NPositive:
		t.Conclusion = ConclusionWorse
	default:
		t.Conclusion = ConclusionNoClear
	}
	return t
}

// metricDeltas compares each delta metric paired by task: per version the
// mean of a task's observed values, then the mean over tasks observed on
// both sides. Unknown and not-applicable values are counted, never read as
// zero. Direction reads B minus A through verdict.HigherIsBetter.
func metricDeltas(common []string, a, b *versionRuns) []MetricDelta {
	out := make([]MetricDelta, 0, len(DeltaMetrics))
	for _, m := range DeltaMetrics {
		md := MetricDelta{Metric: m, HigherIsBetter: verdict.HigherIsBetter(m), Direction: DirectionUnknown}
		var sumA, sumB float64
		for _, task := range common {
			ma, unknownA := taskMean(a.byTask[task], m)
			mb, unknownB := taskMean(b.byTask[task], m)
			md.UnknownA += unknownA
			md.UnknownB += unknownB
			if ma == nil || mb == nil {
				continue
			}
			sumA, sumB = sumA+*ma, sumB+*mb
			md.Tasks++
		}
		if md.Tasks > 0 {
			va, vb := sumA/float64(md.Tasks), sumB/float64(md.Tasks)
			delta := vb - va
			md.A, md.B, md.Delta = &va, &vb, &delta
			md.Direction = direction(m, delta, va, vb)
		}
		out = append(out, md)
	}
	return out
}

// taskMean is the mean of a metric over a task's runs where it was
// observed, nil when none was, and the count of runs where it was not.
func taskMean(runs []countedRun, metric string) (*float64, int) {
	var sum float64
	n, unknown := 0, 0
	for _, r := range runs {
		v, ok := r.metrics[metric].Float()
		if !ok {
			unknown++
			continue
		}
		sum += v
		n++
	}
	if n == 0 {
		return nil, unknown
	}
	mean := sum / float64(n)
	return &mean, unknown
}

// direction says whether a delta is an improvement. A change within a
// millionth of the larger magnitude is the same.
func direction(metric string, delta, a, b float64) string {
	if math.Abs(delta) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b))) {
		return DirectionSame
	}
	if (delta > 0) == verdict.HigherIsBetter(metric) {
		return DirectionBetter
	}
	return DirectionWorse
}

// gateChanges counts gate firings per version on the common tasks. A gate is
// new when it fired in B and never in A; Tasks lists the tasks where it fired
// in B and not in A.
func gateChanges(common []string, a, b *versionRuns) []GateChange {
	byGate := map[string]*GateChange{}
	get := func(g string) *GateChange {
		gc, ok := byGate[g]
		if !ok {
			gc = &GateChange{Gate: g, Tasks: []string{}}
			byGate[g] = gc
		}
		return gc
	}
	for _, task := range common {
		inA := map[string]bool{}
		for _, r := range a.byTask[task] {
			for _, g := range r.fired {
				get(g).FiredA++
				inA[g] = true
			}
		}
		inB := map[string]bool{}
		for _, r := range b.byTask[task] {
			for _, g := range r.fired {
				get(g).FiredB++
				inB[g] = true
			}
		}
		for g := range inB {
			if !inA[g] {
				gc := get(g)
				gc.Tasks = append(gc.Tasks, task)
			}
		}
	}
	out := make([]GateChange, 0, len(byGate))
	for _, gc := range byGate {
		gc.New = gc.FiredA == 0 && gc.FiredB > 0
		slices.Sort(gc.Tasks)
		out = append(out, *gc)
	}
	slices.SortFunc(out, func(x, y GateChange) int {
		return cmp.Or(compareTrueFirst(x.New, y.New), cmp.Compare(x.Gate, y.Gate))
	})
	return out
}

// compareTrueFirst orders true before false.
func compareTrueFirst(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return -1
	default:
		return 1
	}
}
