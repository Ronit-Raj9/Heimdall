package verdict

import (
	"fmt"
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// W7 adaptive trials (docs/adaptive-trials.md). The manifest freezes the
// maximum design and a stopping rule. Which optional slots the rule released
// is never stored: it is a pure function of the sealed runs, replayed here
// by the scheduler (between stages) and by the verdict (at analysis), so the
// analysis always follows the pre-registered rule applied to the evidence.

// Stop reasons of an adaptive design.
const (
	StopConcordant  = "concordant"  // task rule: every task stopped at or before its maximum
	StopMaxReached  = "max_reached" // task rule: some task used every planned trial
	StopEfficacy    = "efficacy"    // sequential: a look decided at its alpha
	StopFutility    = "futility"    // sequential: no later look could decide
	StopFinalLook   = "final_look"  // sequential: the last look was analysed
	StopWaiting     = "waiting"     // some required slot has no run yet
	reasonWaiting   = "waiting"
	lookContinue    = "continue"
	lookNotReached  = "not_reached"
	maxLooks        = 5
	adaptiveVersion = "1.0.0"
)

// InstrumentAdaptiveAllocation is the instrument check that every analysed
// run belongs to a slot the frozen rule required.
const InstrumentAdaptiveAllocation = "adaptive_allocation"

// ReplayAdaptive replays the manifest's frozen adaptive rule over the runs
// and returns the report, with SlotStates naming every optional slot
// released or not_required. A slot with no counted sealed run, or with only
// a placeholder (CompleteSlots), counts as an unknown value, so the replay
// gives the same answer before and after CompleteSlots. Nil when the
// manifest has no adaptive plan.
func ReplayAdaptive(m *schemas.Manifest, runs []schemas.SealedRun) *schemas.AdaptiveReport {
	return ReplayAdaptiveSettled(m, runs, nil)
}

// ReplayAdaptiveSettled is ReplayAdaptive for a design still running: a slot
// with no counted run is an unknown value only when settled reports it done
// (the scheduler attempted it and it ended without a counted run); any other
// slot without a run is still to come, and the rule waits for it instead of
// releasing anything after it. A nil settled treats every slot as done, as
// the verdict does after the runs are in.
func ReplayAdaptiveSettled(m *schemas.Manifest, runs []schemas.SealedRun, settled func(slotID string) bool) *schemas.AdaptiveReport {
	if m == nil || m.Adaptive == nil {
		return nil
	}
	plan := m.Adaptive
	vals := slotValues(m, runs, plan.Metric, settled)
	var rep *schemas.AdaptiveReport
	switch plan.Rule {
	case schemas.AdaptiveTask:
		rep = replayTask(m, plan, vals)
	case schemas.AdaptiveSequential:
		rep = replaySequential(m, plan, vals)
	default:
		rep = &schemas.AdaptiveReport{Rule: plan.Rule, SlotStates: map[string]string{}, StopReason: "unknown rule",
			Notes: []string{fmt.Sprintf("unknown adaptive rule %q: every optional slot is required", plan.Rule)}}
		for _, sl := range m.Slots {
			if sl.Counted && sl.Optional {
				rep.SlotStates[sl.SlotID] = schemas.SlotReleased
			}
		}
	}
	countSlots(m, vals, rep)
	return rep
}

// NotRequiredSlots returns the optional slots the frozen rule never released.
// They are not missing: CompleteSlots adds no placeholder for them.
func NotRequiredSlots(m *schemas.Manifest, runs []schemas.SealedRun) map[string]bool {
	rep := ReplayAdaptive(m, runs)
	if rep == nil {
		return nil
	}
	out := map[string]bool{}
	for id, st := range rep.SlotStates {
		if st == schemas.SlotNotRequired {
			out[id] = true
		}
	}
	return out
}

// AdaptiveRuns drops the counted runs of optional slots the rule did not
// release. Such a run should not exist (the scheduler runs only released
// slots); it can appear when a regrade changed an outcome the rule read.
// The analysis follows the rule, so the run is left out and named in the
// report (Excluded) and in the adaptive_allocation check.
func AdaptiveRuns(m *schemas.Manifest, runs []schemas.SealedRun) []schemas.SealedRun {
	skip := NotRequiredSlots(m, runs)
	if len(skip) == 0 {
		return runs
	}
	out := make([]schemas.SealedRun, 0, len(runs))
	for _, r := range runs {
		if r.Slot.Counted && skip[r.Slot.SlotID] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// excludedRuns names the counted runs of not-required slots.
func excludedRuns(m *schemas.Manifest, runs []schemas.SealedRun, rep *schemas.AdaptiveReport) []string {
	if rep == nil {
		return nil
	}
	var out []string
	for _, r := range runs {
		if r.Slot.Counted && r.Run.RunID != "" && rep.SlotStates[r.Slot.SlotID] == schemas.SlotNotRequired {
			out = append(out, runLabel(r))
		}
	}
	return out
}

// adaptiveCheck is the adaptive_allocation instrument check: warn when runs
// of slots the rule did not release were left out.
func adaptiveCheck(rep *schemas.AdaptiveReport) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{
		Name:      InstrumentAdaptiveAllocation,
		Threshold: "every analysed run belongs to a slot the frozen adaptive rule required",
		Status:    schemas.InstrumentPass,
		Detail: fmt.Sprintf("%s rule: %d of %d planned slots required, %d not required (runs saved)",
			rep.Rule, rep.RequiredSlots, rep.MaxSlots, rep.RunsSaved),
	}
	if len(rep.Excluded) > 0 {
		c.Status = schemas.InstrumentWarn
		c.Detail = fmt.Sprintf("%d run(s) of slots the rule did not release were left out of the analysis: %s",
			len(rep.Excluded), joinCapped(rep.Excluded))
	}
	return c
}

// slotValue is what the rule reads of one slot: its value when observed.
type slotValue struct {
	x        float64
	observed bool
	run      bool          // a counted sealed run exists (not a placeholder)
	settled  bool          // the slot is done: it has a run, or it ended without one
	val      schemas.Value // the run's value, bounds of an unknown included
}

// slotValues maps every counted slot of m to the metric value of its counted
// run, and whether it is settled (settled nil: every slot is).
func slotValues(m *schemas.Manifest, runs []schemas.SealedRun, metric string, settled func(string) bool) map[string]slotValue {
	out := map[string]slotValue{}
	for _, sl := range m.Slots {
		if sl.Counted {
			out[sl.SlotID] = slotValue{settled: settled == nil || settled(sl.SlotID)}
		}
	}
	for _, r := range runs {
		if !r.Slot.Counted || r.Run.RunID == "" {
			continue
		}
		v := slotValue{run: true, settled: true, val: decisionValue(metricValue(r, metric))}
		if x, ok := v.val.Float(); ok && finite(x) {
			v.x, v.observed = x, true
		}
		out[r.Slot.SlotID] = v
	}
	return out
}

// countSlots fills the slot counts of the report.
func countSlots(m *schemas.Manifest, vals map[string]slotValue, rep *schemas.AdaptiveReport) {
	rep.MaxSlots, rep.RequiredSlots, rep.RunSlots, rep.RunsSaved = 0, 0, 0, 0
	for _, sl := range m.Slots {
		if !sl.Counted {
			continue
		}
		rep.MaxSlots++
		if sl.Optional && rep.SlotStates[sl.SlotID] == schemas.SlotNotRequired {
			rep.RunsSaved++
			continue
		}
		rep.RequiredSlots++
		if vals[sl.SlotID].run {
			rep.RunSlots++
		}
	}
}

// ---- rule task ----

// taskKey is one (condition, task).
type taskKey struct{ cond, task string }

// replayTask applies the task rule: for each adaptive (condition, task), the
// first Min trials are required; trial k+1 is released while the trials so
// far are not all observed and identical across every arm and trial. The
// rule reads only the unordered collection of the task's values (arm labels
// play no part), so swapping A and B never changes how many trials a task
// gets: the task-level sign flip stays exact (docs/adaptive-trials.md).
func replayTask(m *schemas.Manifest, plan *schemas.AdaptivePlan, vals map[string]slotValue) *schemas.AdaptiveReport {
	rep := &schemas.AdaptiveReport{Rule: schemas.AdaptiveTask, SlotStates: map[string]string{}, Conditions: plan.Conditions}
	adaptive := map[string]bool{}
	for _, c := range plan.Conditions {
		adaptive[c] = true
	}
	byTrial := map[taskKey]map[int][]schemas.Slot{}
	var keys []taskKey
	for _, sl := range m.Slots {
		if !sl.Counted || !adaptive[sl.Condition] {
			continue
		}
		k := taskKey{sl.Condition, sl.Task}
		if byTrial[k] == nil {
			byTrial[k] = map[int][]schemas.Slot{}
			keys = append(keys, k)
		}
		byTrial[k][sl.Trial] = append(byTrial[k][sl.Trial], sl)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cond != keys[j].cond {
			return keys[i].cond < keys[j].cond
		}
		return keys[i].task < keys[j].task
	})
	anyMax, anyWait := false, false
	for _, k := range keys {
		trials := byTrial[k]
		maxT := 0
		for t := range trials {
			maxT = max(maxT, t)
		}
		used, reason := taskTrials(trials, vals, plan.Min, maxT)
		for t, slots := range trials {
			for _, sl := range slots {
				if !sl.Optional {
					continue
				}
				if t <= used {
					rep.SlotStates[sl.SlotID] = schemas.SlotReleased
				} else {
					rep.SlotStates[sl.SlotID] = schemas.SlotNotRequired
				}
			}
		}
		anyMax = anyMax || reason == StopMaxReached
		anyWait = anyWait || reason == reasonWaiting
		rep.Tasks = append(rep.Tasks, schemas.AdaptiveTaskState{Condition: k.cond, Task: k.task, Trials: used, Max: maxT, Reason: reason})
	}
	switch {
	case anyWait:
		rep.StopReason = StopWaiting
	case anyMax:
		rep.StopReason = StopMaxReached
	default:
		rep.StopReason = StopConcordant
	}
	rep.DecisionAlpha = plan.Alpha
	return rep
}

// taskTrials returns how many trials per arm the rule asks of one task and
// why it stopped there. A trial with any slot lacking an observed value (an
// unknown outcome, or a settled slot with no run) never lets the task stop:
// an unknown is treated the same whichever arm it is in. A slot not yet
// settled makes the rule wait at its trial.
func taskTrials(trials map[int][]schemas.Slot, vals map[string]slotValue, minT, maxT int) (int, string) {
	if minT < 1 {
		minT = 1
	}
	k := min(minT, maxT)
	for ; ; k++ {
		if !trialsSettled(trials, vals, k) {
			return k, reasonWaiting
		}
		if concordant(trials, vals, k) {
			return k, StopConcordant
		}
		if k >= maxT {
			return maxT, StopMaxReached
		}
	}
}

// trialsSettled reports whether every slot of trials 1..k is settled.
func trialsSettled(trials map[int][]schemas.Slot, vals map[string]slotValue, k int) bool {
	for t := 1; t <= k; t++ {
		for _, sl := range trials[t] {
			if !vals[sl.SlotID].settled {
				return false
			}
		}
	}
	return true
}

// concordant reports whether every slot of trials 1..k has an observed value
// and all those values are equal: both arms unanimous and agreeing.
func concordant(trials map[int][]schemas.Slot, vals map[string]slotValue, k int) bool {
	first, set := 0.0, false
	for t := 1; t <= k; t++ {
		slots := trials[t]
		if len(slots) == 0 {
			return false
		}
		for _, sl := range slots {
			v := vals[sl.SlotID]
			if !v.observed {
				return false
			}
			if !set {
				first, set = v.x, true
				continue
			}
			if v.x != first {
				return false
			}
		}
	}
	return set
}

// ---- rule sequential ----

// replaySequential applies the sequential rule: look 1's tasks always run;
// after look k the primary effect over looks 1..k is tested at that look's
// alpha at both missingness bounds. A directional result stops (efficacy);
// otherwise, with futility curtail, the design stops when no completion of
// the remaining looks could give a directional result at any later look;
// otherwise look k+1's slots are released.
func replaySequential(m *schemas.Manifest, plan *schemas.AdaptivePlan, vals map[string]slotValue) *schemas.AdaptiveReport {
	rep := &schemas.AdaptiveReport{Rule: schemas.AdaptiveSequential, SlotStates: map[string]string{}, Looks: len(plan.Looks),
		Conditions: plan.Conditions}
	lookOf := map[string]int{}
	for i, tasks := range plan.Looks {
		for _, t := range tasks {
			lookOf[t] = i + 1
		}
	}
	cond := ""
	if len(plan.Conditions) > 0 {
		cond = plan.Conditions[0]
	}
	reached := len(plan.Looks)
	stopped := false
	for k := 1; k <= len(plan.Looks); k++ {
		lr := schemas.AdaptiveLook{Look: k, Alpha: lookAlpha(plan, k)}
		if stopped {
			lr.Outcome = lookNotReached
			rep.LookResults = append(rep.LookResults, lr)
			continue
		}
		e, waiting := lookEffect(m, plan, cond, lookOf, k, vals)
		lr.Tasks = pairedTasks(e)
		if e != nil {
			lr.DBar = e.DBar
			if e.LowerBound != nil && e.LowerBound.PValue != nil {
				lr.PLow = ptr(*e.LowerBound.PValue)
			}
			if e.UpperBound != nil && e.UpperBound.PValue != nil {
				lr.PHigh = ptr(*e.UpperBound.PValue)
			}
		}
		switch {
		case waiting:
			lr.Outcome = reasonWaiting
			rep.StopReason, rep.StopLook = StopWaiting, k
			reached, stopped = k, true
		case improvedAtBoth(e, lr.Alpha) || regressedAtBoth(e, lr.Alpha):
			lr.Outcome = StopEfficacy
			rep.StopReason, rep.StopLook = StopEfficacy, k
			reached, stopped = k, true
		case k == len(plan.Looks):
			lr.Outcome = StopFinalLook
			rep.StopReason, rep.StopLook = StopFinalLook, k
		default:
			futile, best := futileAfter(plan, e, k)
			lr.BestCaseP = best
			if futile {
				lr.Outcome = StopFutility
				rep.StopReason, rep.StopLook = StopFutility, k
				reached, stopped = k, true
			} else {
				lr.Outcome = lookContinue
			}
		}
		rep.LookResults = append(rep.LookResults, lr)
	}
	for _, sl := range m.Slots {
		if !sl.Counted || !sl.Optional {
			continue
		}
		if look := max(lookOf[sl.Task], 1); look <= reached {
			rep.SlotStates[sl.SlotID] = schemas.SlotReleased
		} else {
			rep.SlotStates[sl.SlotID] = schemas.SlotNotRequired
		}
	}
	rep.DecisionAlpha = lookAlpha(plan, max(rep.StopLook, 1))
	return rep
}

// lookEffect is the primary effect over the tasks of looks 1..k, each slot's
// value from its run or unknown when it has none. waiting reports that some
// slot of looks 1..k is not settled yet, so look k cannot be read. The
// verdict treats every slot as settled: for it a slot without a run is an
// unknown.
func lookEffect(m *schemas.Manifest, plan *schemas.AdaptivePlan, cond string, lookOf map[string]int, k int, vals map[string]slotValue) (*schemas.Effect, bool) {
	a, b := map[string]cell{}, map[string]cell{}
	rate := RateMetric(plan.Metric)
	waiting := false
	for _, sl := range m.Slots {
		if !sl.Counted || sl.Condition != cond {
			continue
		}
		look := max(lookOf[sl.Task], 1)
		if look > k {
			continue
		}
		v := vals[sl.SlotID]
		val := schemas.Unknown("no sealed run", schemas.EvidenceOutcome)
		if v.run {
			val = v.val
		}
		waiting = waiting || !v.settled
		switch sl.Arm {
		case ArmBaseline:
			c := a[sl.Task]
			c.add(val, rate)
			a[sl.Task] = c
		case ArmCandidate:
			c := b[sl.Task]
			c.add(val, rate)
			b[sl.Task] = c
		}
	}
	return pairedEffect(cond, plan.Metric, ArmBaseline, ArmCandidate, a, b, true), waiting
}

// futileAfter reports whether, after look k, no completion of the remaining
// tasks could make any later look directional, and the best-case p of the
// final look. Per-task differences of a rate metric lie in [-1, 1]; for the
// sign-flip test the smallest p over every completion of m more tasks is
// reached with all m at +1 or all at -1 (docs/adaptive-trials.md, lemma), so
// two evaluations per bound decide it exactly. A directional result needs
// both bounds, so one bound that cannot get there is enough. Never futile
// without bounds, on a metric outside [0, 1], or with futility off.
func futileAfter(plan *schemas.AdaptivePlan, e *schemas.Effect, k int) (bool, *float64) {
	if plan.Futility == schemas.FutilityOff || e == nil || e.LowerBound == nil || e.UpperBound == nil {
		return false, nil
	}
	if plan.Futility == schemas.FutilityNonBinding && noTrend(e) {
		return true, nil
	}
	if !RateMetric(plan.Metric) {
		return false, nil
	}
	low, high := diffs(e.LowerBound), diffs(e.UpperBound)
	more := 0
	var best *float64
	for j := k + 1; j <= len(plan.Looks); j++ {
		more += len(plan.Looks[j-1])
		pl, ph := BestCaseP(low, more), BestCaseP(high, more)
		if j == len(plan.Looks) {
			best = ptr(math.Max(pl, ph))
		}
		if pl < lookAlpha(plan, j) && ph < lookAlpha(plan, j) {
			return false, best
		}
	}
	return true, best
}

// noTrend reports a look with no trend at either missingness bound: the
// sign-flip p at both is at least NonBindingP. Used only by futility
// nonbinding; a futility stop never adds a rejection.
func noTrend(e *schemas.Effect) bool {
	lo, hi := e.LowerBound.PValue, e.UpperBound.PValue
	return lo != nil && hi != nil && *lo >= schemas.NonBindingP && *hi >= schemas.NonBindingP
}

// BestCaseP is the smallest two-sided sign-flip p-value any completion of d
// with more tasks, each difference in [-1, 1], can give: the smaller of the
// p-values with every added difference at +1 and every one at -1.
//
// Lemma: if sum(d) > 0, raising any one d_j never raises p. Pair each sign
// vector with its negation and take the member with s_j = +1: its statistic
// moves from T to T+δ while the observed sum moves from S to S+δ. For T >= 0
// the condition |T+δ| >= S+δ is T >= S, unchanged; for T < 0 it implies
// |T| >= S+2δ, so the vector was already counted. A completion with a
// positive total is therefore dominated by all +1, one with a negative total
// by all -1, and one with total zero has p = 1.
//
// Above stats.ExactMaxNonzero differing tasks SignFlip samples its p, and a
// sampled value is no proof that alpha is out of reach (audit 1-B-07), so
// such a p is lowered by six Monte Carlo standard errors first: futility
// then stops only when even that lower bound is not below alpha.
func BestCaseP(d []float64, more int) float64 {
	if more <= 0 {
		return provableP(stats.SignFlip(d))
	}
	up := append(append(make([]float64, 0, len(d)+more), d...), fill1(more, 1)...)
	down := append(append(make([]float64, 0, len(d)+more), d...), fill1(more, -1)...)
	return math.Min(provableP(stats.SignFlip(up)), provableP(stats.SignFlip(down)))
}

// provableP is r's p when it is exact, else a lower bound six Monte Carlo
// standard errors below the sampled value, never below zero.
func provableP(r stats.SignFlipResult) float64 {
	if r.Exact || r.Draws <= 0 {
		return r.P
	}
	se := math.Sqrt(math.Max(r.P*(1-r.P), 1/float64(r.Draws)) / float64(r.Draws))
	return math.Max(0, r.P-6*se)
}

func fill1(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func diffs(e *schemas.Effect) []float64 {
	out := make([]float64, 0, len(e.PerTask))
	for _, td := range e.PerTask {
		out = append(out, td.D)
	}
	return out
}

// lookAlpha is look k's alpha (1-based), or the plan's alpha when the plan
// lists none.
func lookAlpha(plan *schemas.AdaptivePlan, k int) float64 {
	if k >= 1 && k <= len(plan.LookAlphas) {
		return plan.LookAlphas[k-1]
	}
	return plan.Alpha
}

// LookAlphas splits alpha over the looks of a sequential design, by the
// cumulative fraction of tasks each look completes. equal gives alpha/L to
// every look; obf gives each look the increment of the Lan-DeMets
// O'Brien-Fleming spending function alpha(t) = 2 - 2 Phi(z_{1-alpha/2}/sqrt(t)),
// which sums to alpha at t = 1. Either is used as a Bonferroni split, so the
// chance of any false directional result over all looks is at most the sum,
// alpha, whatever the dependence between looks. explicit, when given, is
// taken as written and must have one alpha per look summing to at most alpha.
func LookAlphas(sizes []int, alpha float64, split string, explicit []float64) ([]float64, error) {
	L := len(sizes)
	if L < 2 || L > maxLooks {
		return nil, fmt.Errorf("looks must be between 2 and %d, got %d", maxLooks, L)
	}
	if len(explicit) > 0 {
		if len(explicit) != L {
			return nil, fmt.Errorf("alphas lists %d values for %d looks", len(explicit), L)
		}
		sum := 0.0
		for _, a := range explicit {
			if a <= 0 || a >= 1 {
				return nil, fmt.Errorf("alpha %v of a look must lie in (0, 1)", a)
			}
			sum += a
		}
		if sum > alpha+1e-12 {
			return nil, fmt.Errorf("look alphas sum to %.6f, above verdict alpha %.6f", sum, alpha)
		}
		return append([]float64(nil), explicit...), nil
	}
	total := 0
	for _, n := range sizes {
		if n < 1 {
			return nil, fmt.Errorf("every look needs at least one task")
		}
		total += n
	}
	out := make([]float64, L)
	switch split {
	case schemas.AlphaSplitEqual:
		for i := range out {
			out[i] = alpha / float64(L)
		}
	case "", schemas.AlphaSplitOBF:
		z := normalQuantile(1 - alpha/2)
		spent, cum := 0.0, 0
		for i, n := range sizes {
			cum += n
			t := float64(cum) / float64(total)
			a := 2 - 2*normalCDF(z/math.Sqrt(t))
			if i == L-1 {
				a = alpha
			}
			out[i] = math.Max(a-spent, 0)
			spent = a
		}
	default:
		return nil, fmt.Errorf("alpha_split %q: want equal or obf", split)
	}
	return out, nil
}

// SplitLooks deals tasks into looks of near-equal size in the order given;
// the first looks take the remainder.
func SplitLooks(tasks []string, looks int) [][]string {
	out := make([][]string, 0, looks)
	n := len(tasks)
	start := 0
	for i := 0; i < looks; i++ {
		size := n / looks
		if i < n%looks {
			size++
		}
		out = append(out, append([]string(nil), tasks[start:start+size]...))
		start += size
	}
	return out
}

func normalCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

func normalQuantile(p float64) float64 { return -math.Sqrt2 * math.Erfcinv(2*p) }

func ptr(x float64) *float64 { return &x }

func joinCapped(items []string) string {
	listed := items
	if len(listed) > maxListedSlots {
		listed = append(append([]string(nil), listed[:maxListedSlots]...), fmt.Sprintf("and %d more", len(items)-maxListedSlots))
	}
	out := ""
	for i, s := range listed {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}
