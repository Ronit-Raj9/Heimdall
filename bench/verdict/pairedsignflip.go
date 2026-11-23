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

// Guard rules (DESIGN.md 9.2). RuleTimeoutParity is in timeoutparity.go.
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

	decisionAlpha float64 // W7: alpha of the primary decision when it differs from alpha (sequential_verdict.go)
}

// newPairedSignFlipPolicy returns the paired_signflip verdict policy.
func newPairedSignFlipPolicy() *pairedSignFlipPolicy {
	return &pairedSignFlipPolicy{}
}

// GetName implements schemas.Part.
func (p *pairedSignFlipPolicy) GetName() string { return PolicyPairedSignFlip }

// GetVersion implements schemas.Part.
func (p *pairedSignFlipPolicy) GetVersion() string { return pairedSignFlipVersion }

// Decide compares arm B against arm A on the primary endpoint. With three or
// more setups (not counting an A/A copy) it decides over all of them instead
// (decideSetups): leader, inconclusive or invalid, never B against A. Only counted
// runs enter, and every counted slot of the manifest enters: one without a
// sealed run is added as an unknown (CompleteSlots), so it widens the
// missingness bounds and lowers graded coverage instead of vanishing. The
// decision must hold at both missingness bounds; the paired-t interval is
// descriptive and never decides. Mode is left for the caller, which knows
// whether the runs are live, recorded or synthetic.
//
// An A/A arm (A2, docs/noise-floor.md) enters only the variance card and the
// aa_noise_floor check; the naive scoreboard, diagnostics, meters, arm
// metrics and the instrument checks that span every arm show it under its own
// id. It never enters the A-against-B effect, the guards or integrity.
func (p *pairedSignFlipPolicy) Decide(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun) (*schemas.Analysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("decide: %w", err)
	}
	if m == nil {
		return nil, errors.New("decide: nil manifest")
	}
	if arm := SingleArm(m); arm != "" {
		return decideSingleAnalysis(m, runs, arm), nil
	}
	arms := manifestArms(m)
	if !arms[ArmBaseline] || !arms[ArmCandidate] {
		return nil, fmt.Errorf("decide experiment %s: manifest needs arms %s and %s", m.ExpID, ArmBaseline, ArmCandidate)
	}
	cfg := settingsFor(m)
	raw := runs
	runs = DecisionRuns(m, runs)                                             // W7 adaptive trials: slots the frozen rule never released are not missing (sequential.go)
	cfg.decisionAlpha = DecisionAlpha(m, cfg.alpha, ReplayAdaptive(m, runs)) // W7: look alpha or first-stage alpha
	idx := newRunIndex(runs)
	v := verdictFor(m, cfg, idx, runs, raw, hasAAArm(m))
	w7Verdict(m, cfg, raw, runs, idx, &v) // W7 adaptive report, tier, buy more evidence (sequential_verdict.go)
	if setups := setupArms(m); len(setups) > 2 {
		decideSetups(m, cfg, idx, runs, setups, &v) // fix wave 3, N244: one verdict for three or more setups (setups.go)
	} else {
		v.Basis = basisOf(&v, cfg.primaryAlpha()) // fix wave 3, N244: effect or guard (basis.go)
	}
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

		SafetyEvidence: safetyEvidence(m, idx, cfg.primary), // control, framing and insider-threat block (safetyevidence.go)
	}, nil
}

// verdictFor assembles the verdict: primary effect, guards, instrument gates
// and checks, integrity, decision, action, the minimal detectable effect and
// the variance card behind it (noiseFloor). A directional A/A result fails
// the aa_noise_floor check, whose detail becomes an invalid reason.
//
// executed is every run the caller supplied, before the adaptive rule left
// any out: evidence problems and egress are read from it, so a run the
// stopping rule no longer needs still has its integrity checked (audit
// qc2-03-07).
func verdictFor(m *schemas.Manifest, cfg settings, idx runIndex, runs, executed []schemas.SealedRun, hasAA bool) schemas.Verdict {
	pc, pm := cfg.primary.Condition, cfg.primary.Metric
	primary := pairedEffect(pc, pm, ArmBaseline, ArmCandidate, idx.cells(pc, ArmBaseline, pm), idx.cells(pc, ArmCandidate, pm), true)
	guards := evaluateGuards(cfg, idx)
	variance, aaCheck := noiseFloor(cfg, idx, hasAA)
	checks := append(instrumentChecks(m, idx), slotCoverage(m, runs), aaCheck)
	fair, fairChecks := fairnessFor(m, idx) // Multi-agent area: equal-budget fairness (fairness.go)
	checks = append(checks, fairChecks...)
	invalid := append(instrumentGates(m, cfg, idx, runs, executed), instrumentReasons(checks)...)
	integrity := integrityStatus(idx)
	decision := decide(invalid, primary, guards, cfg.primaryAlpha()) // W7: alpha of the primary decision (sequential_verdict.go)

	sd := variance.SD
	variance.MDE = mdePP(pairedTasks(primary), sd, cfg.alpha)
	return schemas.Verdict{
		Primary:           cfg.primary,
		Decision:          decision,
		Action:            actionFor(decision, guards, integrity, checks),
		Effect:            primary,
		Alpha:             cfg.alpha,
		MDE:               variance.MDE,
		MDEAssumedSD:      variance.Source != schemas.VarianceMeasuredAA,
		SD:                sd,
		Variance:          variance,
		TasksNeeded:       tasksNeeded(primary.DBar, sd, cfg.alpha),
		RandomizationUnit: randomizationUnit,
		TieRule:           tieRule,
		Guards:            guards,
		InvalidReasons:    invalid,
		Instruments:       checks,
		Integrity:         integrity,
		ManifestHash:      m.Hash,
		FrozenAt:          m.CreatedAt,
		Fairness:          fair,
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
// both missingness bounds, and IMPROVED needs every decision guard to pass, so
// an undetermined one blocks it. Hold-only guards (timeout_parity) never move
// the decision; actionFor reads them.
func decide(invalid []string, primary *schemas.Effect, guards []schemas.GuardResult, alpha float64) schemas.Decision {
	gating := decisionGuards(guards)
	switch {
	case len(invalid) > 0:
		return schemas.DecisionInvalid
	case regressedAtBoth(primary, alpha) || anyGuard(gating, schemas.GuardFail):
		return schemas.DecisionRegressed
	case improvedAtBoth(primary, alpha) && !anyGuard(gating, schemas.GuardFail, schemas.GuardUndetermined):
		return schemas.DecisionImproved
	default:
		return schemas.DecisionInconclusive
	}
}

// actionFor maps a decision to the release table (DESIGN.md 9.4). IMPROVED
// ships only when every guard passed, integrity did not regress and no
// instrument check is unknown, since an unknown is never read as a pass. A
// failed hold-only guard (timeout_parity) also holds an INCONCLUSIVE verdict:
// the comparison ran under unequal pressure and needs a look before a rerun.
func actionFor(d schemas.Decision, guards []schemas.GuardResult, integrity schemas.IntegrityStatus, checks []schemas.InstrumentCheck) schemas.Action {
	switch d {
	case schemas.DecisionInvalid:
		return schemas.ActionFixHarness
	case schemas.DecisionRegressed:
		return schemas.ActionHold
	case schemas.DecisionImproved:
		if anyGuard(guards, schemas.GuardFail, schemas.GuardUndetermined) || integrity == schemas.IntegrityRegressed ||
			anyInstrument(checks, schemas.InstrumentUnknown) {
			return schemas.ActionHold
		}
		return schemas.ActionShip
	default:
		if anyGuard(holdGuards(guards), schemas.GuardFail) {
			return schemas.ActionHold
		}
		return schemas.ActionNoDecision
	}
}

// holdOnly reports whether a guard rule only holds a release and never moves
// the decision.
func holdOnly(rule string) bool {
	return rule == RuleTimeoutParity
}

// decisionGuards returns the guards that can move the decision.
func decisionGuards(guards []schemas.GuardResult) []schemas.GuardResult {
	var out []schemas.GuardResult
	for _, g := range guards {
		if !holdOnly(g.Spec.Rule) {
			out = append(out, g)
		}
	}
	return out
}

// holdGuards returns the hold-only guards.
func holdGuards(guards []schemas.GuardResult) []schemas.GuardResult {
	var out []schemas.GuardResult
	for _, g := range guards {
		if holdOnly(g.Spec.Rule) {
			out = append(out, g)
		}
	}
	return out
}

// regressedAtBoth reports an effect significantly worse for B at both bounds:
// d_bar below zero on a higher-is-better metric, above zero on a
// lower-is-better one (HigherIsBetter). An effect without bounds is never
// directional.
func regressedAtBoth(e *schemas.Effect, alpha float64) bool {
	if e == nil {
		return false
	}
	worse := -better(e.Metric)
	return significant(e.LowerBound, alpha, worse) && significant(e.UpperBound, alpha, worse)
}

// improvedAtBoth reports an effect significantly better for B at both bounds,
// in the metric's direction.
func improvedAtBoth(e *schemas.Effect, alpha float64) bool {
	if e == nil {
		return false
	}
	up := better(e.Metric)
	return significant(e.LowerBound, alpha, up) && significant(e.UpperBound, alpha, up)
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
		case RuleTimeoutParity:
			out = append(out, timeoutParityGuard(g, idx))
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
// bounds, in the metric's direction, and is undetermined when the bounds
// disagree or do not exist (an unbounded unknown on a metric outside [0, 1]).
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
	if pairedTasks(eff) == 0 {
		res.Status = schemas.GuardUndetermined
		res.Detail = fmt.Sprintf("no task ran in both arms on %s", g.Condition)
		return res
	}
	if eff.LowerBound == nil || eff.UpperBound == nil {
		res.Status = schemas.GuardUndetermined
		res.Detail = fmt.Sprintf("%s on %s has unknown values with no bounds; a metric outside [0, 1] cannot be bounded, so a regression cannot be ruled out",
			g.Metric, g.Condition)
		return res
	}
	worse := -better(g.Metric)
	// A no_regression guard moves the decision (REGRESSED), so it is tested
	// at the planned threshold (a sequential look or first-stage alpha), not
	// only when it names the exact primary endpoint: another metric on the
	// same sequentially sampled condition, such as safe_success beside
	// verified_success, would otherwise spend alpha the plan never allotted
	// (audits 1-B-01, qc2-03-04). Without a plan primaryAlpha is alpha.
	alpha := cfg.primaryAlpha()
	lo := significant(eff.LowerBound, alpha, worse)
	hi := significant(eff.UpperBound, alpha, worse)
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
// arm, shown beside the verdict on purpose. A pass is always read from
// verified_success: a primary metric in seconds, counts or USD has no pass
// threshold (audit 1-C-07).
func naiveScoreboard(idx runIndex, primary schemas.Endpoint) map[string]schemas.Ratio {
	out := map[string]schemas.Ratio{}
	for _, arm := range armsIn(idx, primary.Condition) {
		out[arm] = successRatio(idx.armRuns(primary.Condition, arm), MetricVerifiedSuccess)
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
