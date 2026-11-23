package verdict

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// W7 buy more evidence (docs/adaptive-trials.md): a follow-up planned by
// `extend` is analysed together with the experiment it extends only under
// the combination rule the first experiment pre-declared before its first
// run. The rule is the weighted inverse-normal combination of one-sided
// stage p-values, each stage's own exact sign-flip test on its own runs:
//
//	Z = w1 * Phi^-1(1 - p1) + w2 * Phi^-1(1 - p2),  w1^2 + w2^2 = 1
//
// tested at alpha - alpha_first (the first stage decided at alpha_first),
// each direction at half of it. The second stage's p-value is exact given
// everything the first stage showed, however its size was chosen, so the
// combination keeps its level when the follow-up size depends on the first
// result; Bonferroni over the two analyses keeps the whole at alpha. With
// no pre-declared rule the pooled analysis is exploratory and decides
// nothing.

// CombineExtension analyses child (planned by `extend`) together with
// parent. Each decision is the stage's own verdict; an INVALID stage makes
// the combination INVALID.
func CombineExtension(parent *schemas.Manifest, parentRuns []schemas.SealedRun, parentDecision schemas.Decision,
	child *schemas.Manifest, childRuns []schemas.SealedRun, childDecision schemas.Decision) (*schemas.ExtensionResult, error) {
	if parent == nil || child == nil {
		return nil, errors.New("combine: nil manifest")
	}
	ref := child.Experiment.Extends
	if ref == nil {
		return nil, fmt.Errorf("combine: experiment %s extends nothing", child.ExpID)
	}
	if ref.ExpID != parent.ExpID || ref.ManifestHash != parent.Hash {
		return nil, fmt.Errorf("combine: %s extends %s at manifest %s, not %s at %s", child.ExpID, ref.ExpID, ref.ManifestHash, parent.ExpID, parent.Hash)
	}
	pc, cc := settingsFor(parent), settingsFor(child)
	if pc.primary != cc.primary {
		return nil, fmt.Errorf("combine: primary endpoints differ: %s/%s and %s/%s", pc.primary.Condition, pc.primary.Metric, cc.primary.Condition, cc.primary.Metric)
	}
	if err := sameComparison(parent, child, pc, cc, ref.Kind); err != nil {
		return nil, fmt.Errorf("combine: %w", err)
	}
	res := &schemas.ExtensionResult{Parent: parent.ExpID, Child: child.ExpID, Kind: ref.Kind, Rule: schemas.ExtensionExploratory}
	pe := stageEffect(parent, parentRuns, pc.primary)
	ce := stageEffect(child, childRuns, cc.primary)
	res.Stages = []schemas.ExtensionStage{stageOf(parent.ExpID, parentDecision, pe), stageOf(child.ExpID, childDecision, ce)}
	res.PooledDBar, res.PooledTasks = pooled(pe, ce)

	a1, declared := ExtensionAlphaFirst(parent, pc.alpha)
	if !declared || ref.Rule != schemas.ExtensionInverseNormal {
		res.Exploratory = true
		res.Decision = schemas.DecisionInconclusive
		res.Reasons = append(res.Reasons, fmt.Sprintf("experiment %s pre-declared no combination rule: the pooled analysis is exploratory and decides nothing; run a new experiment with enough tasks instead", parent.ExpID))
		return res, nil
	}
	res.Rule = schemas.ExtensionInverseNormal
	res.Alpha = pc.alpha - a1
	w1sq := parent.Experiment.Trials.Extension.WeightFirst
	if w1sq <= 0 || w1sq >= 1 {
		w1sq = 0.5
	}
	res.WeightFirst = w1sq
	w1, w2 := math.Sqrt(w1sq), math.Sqrt(1-w1sq)
	res.ZCritical = normalQuantile(1 - res.Alpha/2)
	p, c := res.Stages[0], res.Stages[1]
	res.ZImproveLow = combineZ(w1, w2, p.PImproveLow, c.PImproveLow)
	res.ZImproveHi = combineZ(w1, w2, p.PImproveHi, c.PImproveHi)
	res.ZRegressLow = combineZ(w1, w2, p.PRegressLow, c.PRegressLow)
	res.ZRegressHi = combineZ(w1, w2, p.PRegressHi, c.PRegressHi)
	improved := res.ZImproveLow >= res.ZCritical && res.ZImproveHi >= res.ZCritical
	regressed := res.ZRegressLow >= res.ZCritical && res.ZRegressHi >= res.ZCritical
	switch {
	case parentDecision == schemas.DecisionInvalid || childDecision == schemas.DecisionInvalid:
		res.Decision = schemas.DecisionInvalid
		res.Reasons = append(res.Reasons, "a stage's own verdict is INVALID: fix the harness; no combined decision")
	case regressed:
		res.Decision = schemas.DecisionRegressed
	case improved && childDecision == schemas.DecisionRegressed:
		res.Decision = schemas.DecisionInconclusive
		res.Reasons = append(res.Reasons, "the follow-up's own verdict is REGRESSED (a guard or the primary): the combination cannot ship it")
	case improved:
		res.Decision = schemas.DecisionImproved
	default:
		res.Decision = schemas.DecisionInconclusive
	}
	return res, nil
}

// sameComparison checks that child repeats parent's frozen comparison (audit
// 1-B-03): the same arm configs, suite and alpha, and only the change its
// kind permits: fresh tasks the parent never ran (tasks) or more trials of
// the parent's own tasks (trials) on the primary condition.
func sameComparison(parent, child *schemas.Manifest, pc, cc settings, kind string) error {
	for _, arm := range []string{ArmBaseline, ArmCandidate} {
		pa, okP := parent.Arms[arm]
		ca, okC := child.Arms[arm]
		if !okP || !okC || pa.ConfigHash != ca.ConfigHash {
			return fmt.Errorf("arm %s config hash differs between %s (%s) and %s (%s): not the same comparison", arm, parent.ExpID, pa.ConfigHash, child.ExpID, ca.ConfigHash)
		}
	}
	if parent.SuiteID != child.SuiteID {
		return fmt.Errorf("suites differ: %s and %s", parent.SuiteID, child.SuiteID)
	}
	if err := sameEvaluator(parent, child); err != nil {
		return err
	}
	if pc.alpha != cc.alpha {
		return fmt.Errorf("verdict alpha differs: %v and %v", pc.alpha, cc.alpha)
	}
	parentTasks := slotTasks(parent, pc.primary.Condition)
	for task := range slotTasks(child, cc.primary.Condition) {
		switch kind {
		case "tasks":
			if parentTasks[task] {
				return fmt.Errorf("a tasks follow-up reruns task %s of %s; only fresh tasks may join the combination", task, parent.ExpID)
			}
		case "trials":
			if !parentTasks[task] {
				return fmt.Errorf("a trials follow-up runs task %s that %s never ran", task, parent.ExpID)
			}
		default:
			return fmt.Errorf("follow-up kind %q: want tasks or trials", kind)
		}
	}
	return nil
}

// sameEvaluator checks that both stages were graded by the same frozen
// evaluator (audit qc2-03-05): the same suite version, the same scenario
// inputs (suite services, judge, verifier mode), and the same suite lock, or
// at least the same lock fingerprints (every evaluator setting and file).
// Equal task ids under a re-locked probe or verifier are not the same
// comparison. `extend -tasks` draws fresh tasks from the parent's own lock,
// so an ordinary follow-up passes.
func sameEvaluator(parent, child *schemas.Manifest) error {
	if parent.SuiteVersion != child.SuiteVersion {
		return fmt.Errorf("suite versions differ: %s and %s: not the same evaluator", parent.SuiteVersion, child.SuiteVersion)
	}
	if !maps.Equal(parent.Scenario, child.Scenario) {
		return fmt.Errorf("scenario inputs (suite services, judge, verifier mode) differ between %s and %s: not the same evaluator", parent.ExpID, child.ExpID)
	}
	if parent.SuiteLockHash == child.SuiteLockHash {
		return nil
	}
	if parent.FingerprintHash != "" && parent.FingerprintHash == child.FingerprintHash {
		return nil
	}
	return fmt.Errorf("suite lock differs between %s (%s) and %s (%s) and the lock fingerprints do not show the same evaluator settings: a probe or verifier may have changed, so the stages cannot combine", parent.ExpID, parent.SuiteLockHash, child.ExpID, child.SuiteLockHash)
}

// slotTasks are the tasks of m's counted slots on condition.
func slotTasks(m *schemas.Manifest, condition string) map[string]bool {
	out := map[string]bool{}
	for _, sl := range m.Slots {
		if sl.Counted && sl.Condition == condition {
			out[sl.Task] = true
		}
	}
	return out
}

// AuthorizedFollowUp returns the id of the one follow-up of parent the
// combination may use: the first planned (earliest frozen manifest, then
// smallest id) among candidates extending parent at its manifest hash. The
// pre-declared alpha pays for one follow-up only; a later child of the same
// parent, planned after more evidence was seen, never joins (audit 1-B-03).
func AuthorizedFollowUp(parent *schemas.Manifest, candidates []*schemas.Manifest) string {
	var best *schemas.Manifest
	for _, c := range candidates {
		if c == nil || c.Experiment.Extends == nil || c.Experiment.Extends.ExpID != parent.ExpID || c.Experiment.Extends.ManifestHash != parent.Hash {
			continue
		}
		if best == nil || c.CreatedAt.Before(best.CreatedAt) || (c.CreatedAt.Equal(best.CreatedAt) && c.ExpID < best.ExpID) {
			best = c
		}
	}
	if best == nil {
		return ""
	}
	return best.ExpID
}

// ExtensionStageProblems lists why a stage's own analysis forbids a
// directional combined decision (audit 1-B-09): a judge whose calibration
// caps or invalidates its verdict, or an instrument that could not observe.
func ExtensionStageProblems(an *schemas.Analysis) []string {
	if an == nil {
		return []string{"stage has no analysis"}
	}
	var out []string
	if jc := an.JudgeCalibration; jc != nil && (jc.Caps || jc.State == schemas.CalibrationStateUnreliable) {
		out = append(out, fmt.Sprintf("stage %s judge calibration is %s: %s", an.ExpID, jc.State, jc.Detail))
	}
	for _, c := range an.Verdict.Instruments {
		if c.Status == schemas.InstrumentUnknown {
			out = append(out, fmt.Sprintf("stage %s instrument %s is unknown", an.ExpID, c.Name))
		}
	}
	return out
}

// ExtensionStageHolds lists why a stage's own analysis holds a release
// (audit qc2-03-03): a failed guard (a hold-only one such as timeout_parity
// included), regressed integrity, a hold action, or a release policy that
// blocks. A stage on hold cannot let the combination ship, so these withhold
// a combined IMPROVED (HoldImprovement); a combined REGRESSED stands.
func ExtensionStageHolds(an *schemas.Analysis) []string {
	if an == nil {
		return []string{"stage has no analysis"}
	}
	var out []string
	for _, g := range an.Verdict.Guards {
		if g.Status == schemas.GuardFail {
			out = append(out, fmt.Sprintf("stage %s guard %s failed", an.ExpID, g.Spec.Rule))
		}
	}
	if an.Verdict.Integrity == schemas.IntegrityRegressed {
		out = append(out, fmt.Sprintf("stage %s integrity regressed", an.ExpID))
	}
	if an.Verdict.Action == schemas.ActionHold && len(out) == 0 && an.Verdict.Decision != schemas.DecisionRegressed {
		out = append(out, fmt.Sprintf("stage %s action is hold", an.ExpID))
	}
	if p := an.Policy; p != nil && p.Decision == schemas.PolicyBlock {
		out = append(out, fmt.Sprintf("stage %s release policy %s blocks", an.ExpID, p.RuleSetName()))
	}
	return out
}

// HoldImprovement makes a combined IMPROVED INCONCLUSIVE when a stage is on
// hold, with the holds as reasons; any other decision is kept.
func HoldImprovement(res *schemas.ExtensionResult, holds []string) {
	if res == nil || len(holds) == 0 || res.Decision != schemas.DecisionImproved {
		return
	}
	res.Reasons = append(res.Reasons, "combined improved withheld: a stage is on release hold")
	res.Reasons = append(res.Reasons, holds...)
	res.Decision = schemas.DecisionInconclusive
}

// HoldExtension makes a directional combined decision INCONCLUSIVE when any
// problem stands, with the problems as reasons. An INVALID one stays.
func HoldExtension(res *schemas.ExtensionResult, problems []string) {
	if res == nil || len(problems) == 0 {
		return
	}
	if res.Decision == schemas.DecisionImproved || res.Decision == schemas.DecisionRegressed {
		res.Reasons = append(res.Reasons, fmt.Sprintf("combined %s withheld: the stage evidence cannot decide", res.Decision))
		res.Decision = schemas.DecisionInconclusive
	}
	res.Reasons = append(res.Reasons, problems...)
}

// stageEffect is the primary effect of one stage, with its missingness
// bounds, over the slots its own design required.
func stageEffect(m *schemas.Manifest, runs []schemas.SealedRun, primary schemas.Endpoint) *schemas.Effect {
	runs = CompleteSlots(m, AdaptiveRuns(m, runs))
	idx := newRunIndex(runs)
	return pairedEffect(primary.Condition, primary.Metric, ArmBaseline, ArmCandidate,
		idx.cells(primary.Condition, ArmBaseline, primary.Metric), idx.cells(primary.Condition, ArmCandidate, primary.Metric), true)
}

// stageOf reads the one-sided p-values of a stage at both bounds. Without
// bounds (an unbounded unknown) every p is 1, so the stage can never help
// a combined decision.
func stageOf(id string, d schemas.Decision, e *schemas.Effect) schemas.ExtensionStage {
	st := schemas.ExtensionStage{ExpID: id, Decision: d, PImproveLow: 1, PImproveHi: 1, PRegressLow: 1, PRegressHi: 1}
	if e == nil {
		return st
	}
	st.Tasks, st.DBar = e.NTasks, e.DBar
	if e.LowerBound == nil || e.UpperBound == nil {
		return st
	}
	up := better(e.Metric)
	st.PImproveLow, st.PImproveHi = oneSidedP(diffs(e.LowerBound), up), oneSidedP(diffs(e.UpperBound), up)
	st.PRegressLow, st.PRegressHi = oneSidedP(diffs(e.UpperBound), -up), oneSidedP(diffs(e.LowerBound), -up)
	return st
}

// oneSidedP is the exact one-sided sign-flip p-value P(T >= t) for the sum
// of the differences oriented by dir: half the two-sided p when the observed
// sum points that way (the null distribution is symmetric), else 1, which
// is larger than the exact value and so still valid.
func oneSidedP(d []float64, dir float64) float64 {
	sum, scale := 0.0, 0.0
	for _, x := range d {
		sum += dir * x
		scale += math.Abs(x)
	}
	if sum <= 1e-9*scale || sum <= 0 {
		return 1
	}
	p := stats.SignFlip(d).P
	if !finite(p) {
		return 1
	}
	return math.Min(1, p/2)
}

// combineZ is w1*z(p1) + w2*z(p2) with z(p) = Phi^-1(1-p); a p of 1 gives
// minus infinity, so a stage pointing the other way blocks the direction.
func combineZ(w1, w2, p1, p2 float64) float64 {
	z := func(p float64) float64 {
		if p >= 1 {
			return math.Inf(-1)
		}
		return normalQuantile(1 - p)
	}
	return w1*z(p1) + w2*z(p2)
}

// pooled is the descriptive pooled d_bar: per task the mean of the stage
// differences it has, then the mean over tasks.
func pooled(a, b *schemas.Effect) (float64, int) {
	per := map[string][]float64{}
	for _, e := range []*schemas.Effect{a, b} {
		if e == nil {
			continue
		}
		for _, td := range e.PerTask {
			per[td.Task] = append(per[td.Task], td.D)
		}
	}
	tasks := make([]string, 0, len(per))
	for t := range per {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	sum := 0.0
	for _, t := range tasks {
		sum += meanFloats(per[t])
	}
	if len(tasks) == 0 {
		return 0, 0
	}
	return sum / float64(len(tasks)), len(tasks)
}

func meanFloats(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}
