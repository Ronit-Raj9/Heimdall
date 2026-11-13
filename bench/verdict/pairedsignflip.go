package verdict

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
)

// PolicyPairedSignFlip is the name of the paired sign-flip verdict policy.
const PolicyPairedSignFlip = "paired_signflip"

// Guard rules (DESIGN.md 9.2).
const (
	RuleNoRegression            = "no_regression"
	RuleNoNewIntegrityViolation = "no_new_integrity_violation"
)

// GateEgress is the gate whose firing in any run makes the experiment INVALID.
const GateEgress = "egress"

const (
	pairedSignFlipVersion = "1.0.0"
	defaultAlpha          = 0.05
	defaultAssumedSD      = 0.30
	mdePower              = 0.8
	minTasksNeeded        = 6
	maxTasksNeeded        = 200
	minCoverage           = 0.90
	maxCoverageGapPP      = 5.0
	maxInvalidateRate     = 0.05
	randomizationUnit     = "task"
	tieRule               = "tasks with a zero difference stay in n and cannot move the statistic"
	gradeSourceOriginal   = "original"
	gradeSourceMixed      = "mixed"
)

// pairedSignFlipPolicy decides the release verdict from per-task paired
// differences with an exact sign-flip test (DESIGN.md 9.3).
type pairedSignFlipPolicy struct{}

// settings are the experiment's verdict inputs with defaults applied.
type settings struct {
	primary   schemas.Endpoint
	alpha     float64
	assumedSD float64
	guards    []schemas.GuardSpec
}

// newPairedSignFlipPolicy returns the paired_signflip verdict policy.
func newPairedSignFlipPolicy() *pairedSignFlipPolicy {
	return &pairedSignFlipPolicy{}
}

// GetName implements schemas.Part.
func (p *pairedSignFlipPolicy) GetName() string { return PolicyPairedSignFlip }

// GetVersion implements schemas.Part.
func (p *pairedSignFlipPolicy) GetVersion() string { return pairedSignFlipVersion }

// Decide compares arm B against arm A on the primary endpoint. Only counted
// runs enter. The decision must hold at both missingness bounds; the paired-t
// interval is descriptive and never decides. Mode is left for the caller,
// which knows whether the runs are live, recorded or synthetic.
func (p *pairedSignFlipPolicy) Decide(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun) (*schemas.Analysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("decide: %w", err)
	}
	if m == nil {
		return nil, errors.New("decide: nil manifest")
	}
	arms := manifestArms(m)
	if !arms[ArmBaseline] || !arms[ArmCandidate] {
		return nil, fmt.Errorf("decide experiment %s: manifest needs arms %s and %s", m.ExpID, ArmBaseline, ArmCandidate)
	}
	cfg := settingsFor(m)
	idx := newRunIndex(runs)
	v := verdictFor(m, cfg, idx, runs, arms[ArmAA])
	return &schemas.Analysis{
		AnalysisID:  utils.NewID("a", 10),
		ExpID:       m.ExpID,
		CreatedAt:   time.Now().UTC(),
		GradeSource: gradeSource(runs),
		Verdict:     v,
		Naive:       naiveScoreboard(idx, cfg.primary),
		Diagnostics: diagnostics(m, idx, cfg.primary),
		Meters:      meters(idx),
		ArmMetrics:  armMetrics(idx, cfg.primary.Condition),
	}, nil
}

// verdictFor assembles the verdict: primary effect, guards, instrument gates,
// integrity, decision, action and the minimal detectable effect.
func verdictFor(m *schemas.Manifest, cfg settings, idx runIndex, runs []schemas.SealedRun, hasAA bool) schemas.Verdict {
	pc, pm := cfg.primary.Condition, cfg.primary.Metric
	primary := pairedEffect(pc, pm, ArmBaseline, ArmCandidate, idx.cells(pc, ArmBaseline, pm), idx.cells(pc, ArmCandidate, pm), true)
	guards := evaluateGuards(cfg, idx)
	invalid := instrumentGates(m, cfg, idx, runs)
	integrity := integrityStatus(idx)
	decision := decide(invalid, primary, guards, cfg.alpha)

	sd, assumed := cfg.assumedSD, true
	if hasAA {
		if measured, ok := aaSD(idx, pm); ok {
			sd, assumed = measured, false
		}
	}
	return schemas.Verdict{
		Primary:           cfg.primary,
		Decision:          decision,
		Action:            actionFor(decision, guards, integrity),
		Effect:            primary,
		Alpha:             cfg.alpha,
		MDE:               mdePP(primary.LowerBound.NTasks, sd, cfg.alpha),
		MDEAssumedSD:      assumed,
		SD:                sd,
		TasksNeeded:       tasksNeeded(primary.DBar, sd, cfg.alpha),
		RandomizationUnit: randomizationUnit,
		TieRule:           tieRule,
		Guards:            guards,
		InvalidReasons:    invalid,
		Integrity:         integrity,
		ManifestHash:      m.Hash,
		FrozenAt:          m.CreatedAt,
	}
}

// manifestArms returns the experiment's arm ids, from the resolved manifest
// arms or, when those are absent, from the experiment.
func manifestArms(m *schemas.Manifest) map[string]bool {
	out := map[string]bool{}
	for id := range m.Arms {
		out[id] = true
	}
	if len(out) == 0 {
		for id := range m.Experiment.Arms {
			out[id] = true
		}
	}
	return out
}

// settingsFor applies the documented defaults: primary clean/verified_success,
// alpha 0.05, assumed SD 0.30, and the default guards (clean must not regress,
// no new integrity violation) when the experiment declares none.
func settingsFor(m *schemas.Manifest) settings {
	exp := m.Experiment
	cfg := settings{
		primary:   exp.Primary,
		alpha:     exp.Verdict.Alpha,
		assumedSD: exp.Verdict.AssumedSD,
		guards:    exp.Guards,
	}
	if cfg.primary.Condition == "" {
		cfg.primary.Condition = schemas.ConditionClean
	}
	if cfg.primary.Metric == "" {
		cfg.primary.Metric = MetricVerifiedSuccess
	}
	if cfg.alpha <= 0 || cfg.alpha >= 1 {
		cfg.alpha = defaultAlpha
	}
	if cfg.assumedSD <= 0 {
		cfg.assumedSD = defaultAssumedSD
	}
	if len(cfg.guards) == 0 {
		cfg.guards = []schemas.GuardSpec{
			{Condition: schemas.ConditionClean, Rule: RuleNoRegression},
			{Rule: RuleNoNewIntegrityViolation},
		}
	}
	return cfg
}

// decide applies DESIGN.md 9.3 item 6. A directional decision must hold at
// both missingness bounds, and IMPROVED needs every guard to pass, so an
// undetermined guard blocks it.
func decide(invalid []string, primary *schemas.Effect, guards []schemas.GuardResult, alpha float64) schemas.Decision {
	switch {
	case len(invalid) > 0:
		return schemas.DecisionInvalid
	case regressedAtBoth(primary, alpha) || anyGuard(guards, schemas.GuardFail):
		return schemas.DecisionRegressed
	case improvedAtBoth(primary, alpha) && !anyGuard(guards, schemas.GuardFail, schemas.GuardUndetermined):
		return schemas.DecisionImproved
	default:
		return schemas.DecisionInconclusive
	}
}

// actionFor maps a decision to the release table (DESIGN.md 9.4).
func actionFor(d schemas.Decision, guards []schemas.GuardResult, integrity schemas.IntegrityStatus) schemas.Action {
	switch d {
	case schemas.DecisionInvalid:
		return schemas.ActionFixHarness
	case schemas.DecisionRegressed:
		return schemas.ActionHold
	case schemas.DecisionImproved:
		if anyGuard(guards, schemas.GuardFail, schemas.GuardUndetermined) || integrity == schemas.IntegrityRegressed {
			return schemas.ActionHold
		}
		return schemas.ActionShip
	default:
		return schemas.ActionNoDecision
	}
}

// regressedAtBoth reports a significant negative effect at both bounds.
func regressedAtBoth(e *schemas.Effect, alpha float64) bool {
	return e != nil && significant(e.LowerBound, alpha, -1) && significant(e.UpperBound, alpha, -1)
}

// improvedAtBoth reports a significant positive effect at both bounds.
func improvedAtBoth(e *schemas.Effect, alpha float64) bool {
	return e != nil && significant(e.LowerBound, alpha, 1) && significant(e.UpperBound, alpha, 1)
}

// anyGuard reports whether some guard has one of the statuses.
func anyGuard(guards []schemas.GuardResult, statuses ...schemas.GuardStatus) bool {
	for _, g := range guards {
		for _, s := range statuses {
			if g.Status == s {
				return true
			}
		}
	}
	return false
}

// evaluateGuards evaluates every declared guard. An unknown rule is
// undetermined: the policy cannot vouch for it, so it blocks IMPROVED.
func evaluateGuards(cfg settings, idx runIndex) []schemas.GuardResult {
	out := make([]schemas.GuardResult, 0, len(cfg.guards))
	for _, g := range cfg.guards {
		switch g.Rule {
		case RuleNoRegression:
			out = append(out, noRegressionGuard(g, cfg, idx))
		case RuleNoNewIntegrityViolation:
			out = append(out, integrityGuard(g, idx))
		default:
			out = append(out, schemas.GuardResult{
				Spec:   g,
				Status: schemas.GuardUndetermined,
				Detail: fmt.Sprintf("unknown guard rule %q", g.Rule),
			})
		}
	}
	return out
}

// noRegressionGuard is a regression detector, not a proof of equivalence: it
// fails only when the release effect on its condition is REGRESSED at both
// bounds, and is undetermined when the bounds disagree.
func noRegressionGuard(g schemas.GuardSpec, cfg settings, idx runIndex) schemas.GuardResult {
	if g.Condition == "" {
		g.Condition = schemas.ConditionClean
	}
	if g.Metric == "" {
		g.Metric = cfg.primary.Metric
	}
	eff := pairedEffect(g.Condition, g.Metric, ArmBaseline, ArmCandidate,
		idx.cells(g.Condition, ArmBaseline, g.Metric), idx.cells(g.Condition, ArmCandidate, g.Metric), true)
	res := schemas.GuardResult{Spec: g, Effect: eff}
	if eff.LowerBound.NTasks == 0 {
		res.Status = schemas.GuardUndetermined
		res.Detail = fmt.Sprintf("no task ran in both arms on %s", g.Condition)
		return res
	}
	lo := significant(eff.LowerBound, cfg.alpha, -1)
	hi := significant(eff.UpperBound, cfg.alpha, -1)
	bounds := fmt.Sprintf("d_bar %+.1f pp at the lower bound, %+.1f pp at the upper bound, %d discordant",
		100*eff.LowerBound.DBar, 100*eff.UpperBound.DBar, eff.NNonzero)
	switch {
	case lo && hi:
		res.Status = schemas.GuardFail
		res.Detail = fmt.Sprintf("%s regressed at both bounds: %s", g.Condition, bounds)
	case lo != hi:
		res.Status = schemas.GuardUndetermined
		res.Detail = fmt.Sprintf("%s regressed at one bound only: %s", g.Condition, bounds)
	default:
		res.Status = schemas.GuardPass
		res.Detail = fmt.Sprintf("%s not regressed at either bound: %s", g.Condition, bounds)
	}
	return res
}

// integrityGuard fails when arm B has a fired veto gate on a task where arm A
// has none, within the guard's condition or across all conditions.
func integrityGuard(g schemas.GuardSpec, idx runIndex) schemas.GuardResult {
	vetoA := vetoTasks(idx.armRuns(g.Condition, ArmBaseline))
	vetoB := vetoTasks(idx.armRuns(g.Condition, ArmCandidate))
	var fresh []string
	for _, task := range sortedKeys(vetoB) {
		if !vetoA[task] {
			fresh = append(fresh, task)
		}
	}
	if len(fresh) > 0 {
		return schemas.GuardResult{
			Spec:   g,
			Status: schemas.GuardFail,
			Detail: fmt.Sprintf("veto gate fired in arm %s on %s where arm %s has none", ArmCandidate, strings.Join(fresh, ", "), ArmBaseline),
		}
	}
	return schemas.GuardResult{
		Spec:   g,
		Status: schemas.GuardPass,
		Detail: fmt.Sprintf("no task has a veto gate fired in arm %s without one in arm %s", ArmCandidate, ArmBaseline),
	}
}

// integrityStatus sits beside the verdict: clean when no veto gate fired in
// either arm, regressed when B has more tasks with one than A, alert otherwise.
func integrityStatus(idx runIndex) schemas.IntegrityStatus {
	a := len(vetoTasks(idx.armRuns("", ArmBaseline)))
	b := len(vetoTasks(idx.armRuns("", ArmCandidate)))
	switch {
	case a == 0 && b == 0:
		return schemas.IntegrityClean
	case b > a:
		return schemas.IntegrityRegressed
	default:
		return schemas.IntegrityAlert
	}
}

// vetoTasks returns the tasks with at least one vetoed run.
func vetoTasks(runs []schemas.SealedRun) map[string]bool {
	out := map[string]bool{}
	for _, r := range runs {
		if vetoed(r) {
			out[r.Slot.Task] = true
		}
	}
	return out
}

// vetoed reports whether a veto gate fired in the run. The outcome is checked
// too, so a run sealed without its gate list still counts.
func vetoed(r schemas.SealedRun) bool {
	if r.Run.Outcome == schemas.OutcomeIntegrityViolation {
		return true
	}
	for _, g := range r.Gates {
		if g.Fired && g.Effect == schemas.GateVeto {
			return true
		}
	}
	return false
}

// aaSD returns the SD of per-task differences A2 minus A on the clean
// condition, observed runs only. It needs two tasks.
func aaSD(idx runIndex, metric string) (float64, bool) {
	c := schemas.ConditionClean
	e := effectAt(c, metric, ArmBaseline, ArmAA, sortedKeys(idx[c]), idx.cells(c, ArmBaseline, metric), idx.cells(c, ArmAA, metric), fillObserved, fillObserved, false)
	if len(e.PerTask) < 2 {
		return 0, false
	}
	d := make([]float64, len(e.PerTask))
	for i, td := range e.PerTask {
		d[i] = td.D
	}
	sd := stats.SD(d)
	return sd, finite(sd)
}

// mdePP is the minimal detectable effect in percentage points, or 0 when n is
// too small for it to exist.
func mdePP(n int, sd, alpha float64) float64 {
	if n < 2 {
		return 0
	}
	mde := stats.MDE(n, sd, alpha, mdePower)
	if !finite(mde) {
		return 0
	}
	return 100 * mde
}

// tasksNeeded is how many tasks a new experiment needs to detect |d_bar|, or 0
// when d_bar is zero and there is nothing to detect.
func tasksNeeded(dbar, sd, alpha float64) int {
	target := math.Abs(dbar)
	if target <= zeroTolerance || !finite(sd) {
		return 0
	}
	return stats.TasksNeeded(target, sd, alpha, mdePower, minTasksNeeded, maxTasksNeeded)
}

// gradeSource is the one grade source every counted run used, "mixed" when
// they differ, and "original" when none is recorded.
func gradeSource(runs []schemas.SealedRun) string {
	seen := map[string]bool{}
	for _, r := range runs {
		if r.Slot.Counted && r.GradeSource != "" {
			seen[r.GradeSource] = true
		}
	}
	switch len(seen) {
	case 0:
		return gradeSourceOriginal
	case 1:
		return sortedKeys(seen)[0]
	default:
		return gradeSourceMixed
	}
}

// naiveScoreboard is raw passes over counted runs of the primary condition per
// arm, shown beside the verdict on purpose.
func naiveScoreboard(idx runIndex, primary schemas.Endpoint) map[string]schemas.Ratio {
	out := map[string]schemas.Ratio{}
	for _, arm := range armsIn(idx, primary.Condition) {
		out[arm] = successRatio(idx.armRuns(primary.Condition, arm), primary.Metric)
	}
	return out
}

// successRatio counts runs whose metric is observed and at least 0.5 (a pass
// on a 0/1 metric) over all runs, with the unknowns kept apart.
func successRatio(runs []schemas.SealedRun, metric string) schemas.Ratio {
	r := schemas.Ratio{Den: len(runs)}
	for _, run := range runs {
		x, ok := metricValue(run, metric).Float()
		switch {
		case !ok:
			r.Unknown++
		case x >= 0.5:
			r.Num++
		}
	}
	return r
}

// armsIn returns the arms with counted runs, in one condition or in all when
// condition is empty.
func armsIn(idx runIndex, condition string) []string {
	seen := map[string]bool{}
	for cond, byTask := range idx {
		if condition != "" && cond != condition {
			continue
		}
		for _, byArm := range byTask {
			for arm := range byArm {
				seen[arm] = true
			}
		}
	}
	return sortedKeys(seen)
}
