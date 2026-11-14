package policy

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// maxEvidence caps the run ids one check result lists.
const maxEvidence = 20

// defaultAlpha matches the verdict's default significance level.
const defaultAlpha = 0.05

// equalsTolerance is the relative tolerance of an equals check, so a mean that
// is 0 up to floating noise still equals 0.
const equalsTolerance = 1e-9

// evaluator holds what every check reads: the counted runs in a stable order,
// the primary condition and the verdict's alpha.
type evaluator struct {
	runs    []schemas.SealedRun
	primary string
	alpha   float64
	// smallSample is set by the check being evaluated when it is unknown only
	// because the experiment is too small for its min_n.
	smallSample bool
}

// Evaluate checks every rule of p against the experiment's sealed runs and
// returns the result with its decision. hash is the sha256 of the policy file
// as read now; when the manifest froze a different hash the policy changed
// after the experiment was planned, so the result gets a failed, blocking
// policy_hash check and the decision is block, but every rule is still
// evaluated so the page shows what the changed policy would say.
//
// Decision: any failed block-severity check blocks. An unknown block-severity
// check blocks when on_unknown is block and warns when it is warn. With
// on_unknown unset, an unknown that is only a small sample (fewer runs than
// min_n even counting the unknown ones) warns, because more runs would settle
// it, and any other unknown (metric not recorded, data missing) blocks. A
// failed or unknown warn-severity check warns. Otherwise allow.
func Evaluate(p *schemas.Policy, hash string, m *schemas.Manifest, an *schemas.Analysis, runs []schemas.SealedRun) *schemas.PolicyResult {
	if p == nil {
		return nil
	}
	ev := newEvaluator(m, an, runs)
	res := &schemas.PolicyResult{
		PolicyName:  p.Name,
		PolicyHash:  hash,
		Checks:      make([]schemas.PolicyCheckResult, 0, len(p.Checks)+1),
		EvaluatedAt: time.Now().UTC(),
	}
	if m != nil && m.PolicyHash != "" && m.PolicyHash != hash {
		res.Checks = append(res.Checks, hashMismatch(m.PolicyHash, hash))
	}
	for _, c := range p.Checks {
		ev.smallSample = false
		r := ev.check(c)
		r.Blocking = blocking(c, r.Status, p.OnUnknown, ev.smallSample)
		res.Checks = append(res.Checks, r)
	}
	res.Decision = Decide(res.Checks)
	return res
}

// Decide reduces check results to the policy decision: block when any result
// is blocking, warn when any other result failed or is unknown, else allow.
// Unknown is never read as a pass.
func Decide(checks []schemas.PolicyCheckResult) schemas.PolicyDecision {
	decision := schemas.PolicyAllow
	for _, c := range checks {
		switch {
		case c.Blocking:
			return schemas.PolicyBlock
		case c.Status != schemas.PolicyPass:
			decision = schemas.PolicyWarn
		}
	}
	return decision
}

// newEvaluator keeps the counted runs only, sorted so every result and its
// evidence are deterministic, and resolves the primary condition and alpha
// from the analysis, then the manifest, then the documented defaults.
func newEvaluator(m *schemas.Manifest, an *schemas.Analysis, runs []schemas.SealedRun) *evaluator {
	ev := &evaluator{primary: schemas.ConditionClean, alpha: defaultAlpha}
	if m != nil {
		if c := m.Experiment.Primary.Condition; c != "" {
			ev.primary = c
		}
		if a := m.Experiment.Verdict.Alpha; a > 0 && a < 1 {
			ev.alpha = a
		}
	}
	if an != nil {
		if c := an.Verdict.Primary.Condition; c != "" {
			ev.primary = c
		}
		if a := an.Verdict.Alpha; a > 0 && a < 1 {
			ev.alpha = a
		}
	}
	for _, r := range runs {
		if r.Slot.Counted {
			ev.runs = append(ev.runs, r)
		}
	}
	sort.SliceStable(ev.runs, func(i, j int) bool {
		a, b := ev.runs[i].Slot, ev.runs[j].Slot
		switch {
		case a.Condition != b.Condition:
			return a.Condition < b.Condition
		case a.Task != b.Task:
			return a.Task < b.Task
		case a.Arm != b.Arm:
			return a.Arm < b.Arm
		case a.Trial != b.Trial:
			return a.Trial < b.Trial
		default:
			return ev.runs[i].Run.RunID < ev.runs[j].Run.RunID
		}
	})
	return ev
}

// check evaluates one rule. Blocking is set by the caller.
func (ev *evaluator) check(c schemas.PolicyCheck) schemas.PolicyCheckResult {
	cond := c.Condition
	if cond == "" {
		cond = ev.primary
	}
	base := schemas.PolicyCheckResult{ID: c.ID, Metric: c.Metric, Condition: cond, Arm: armOf(c), Threshold: thresholdText(c)}
	minN := max(c.MinN, 1)
	switch {
	case thresholds(c) != 1:
		return unknown(base, "the check needs exactly one of rule, max, min or equals", 0, nil)
	case c.Rule == RuleTimeoutParity:
		return ev.timeoutParity(base)
	case c.Rule != "":
		return ev.rule(c, base, minN)
	case c.Metric == MetricIntegrityViolations:
		return ev.integrity(c, base, minN)
	case c.Metric == MetricCostPerVerifiedSuccess:
		return ev.costPerSuccess(c, base, minN)
	default:
		return ev.mean(c, base, minN)
	}
}

// mean compares the arm's mean of observed per-run values with the threshold.
// Not-applicable values are left out, except where notApplicableValue says a
// not-applicable run has a known value (a passing run is never a false
// completion). Unknown values are left out of the mean and counted in the
// reason, never read as zero.
func (ev *evaluator) mean(c schemas.PolicyCheck, base schemas.PolicyCheckResult, minN int) schemas.PolicyCheckResult {
	runs := ev.selectRuns(base.Condition, base.Arm)
	if len(runs) == 0 {
		return unknown(base, noRuns(base), 0, nil)
	}
	var obs []float64
	var obsRuns []schemas.SealedRun
	var unknownIDs []string
	var level schemas.EvidenceLevel
	recorded := false
	for _, r := range runs {
		v, ok := r.Metrics[c.Metric]
		if !ok {
			unknownIDs = append(unknownIDs, r.Run.RunID)
			continue
		}
		recorded = true
		x, ok := v.Float()
		if !ok && v.State == schemas.ValueNotApplicable {
			x, ok = notApplicableValue(c.Metric)
		}
		switch {
		case ok && finite(x):
			if len(obs) == 0 {
				level = v.Evidence
			}
			obs = append(obs, x)
			obsRuns = append(obsRuns, r)
		case v.State != schemas.ValueNotApplicable:
			unknownIDs = append(unknownIDs, r.Run.RunID)
		}
	}
	if !recorded {
		return unknown(base, fmt.Sprintf("metric %s is not recorded in any of the %d runs of arm %s on %s", c.Metric, len(runs), base.Arm, base.Condition), 0, nil)
	}
	if len(obs) < minN {
		reason := fmt.Sprintf("%d observed runs of %d, fewer than min_n %d (%d unknown)", len(obs), len(runs), minN, len(unknownIDs))
		return ev.tooFew(base, reason, len(obs), len(obs)+len(unknownIDs) < minN, unknownIDs)
	}
	sum := 0.0
	for _, x := range obs {
		sum += x
	}
	m := sum / float64(len(obs))
	res := base
	res.N = len(obs)
	res.Observed = schemas.Observed(m, level)
	note := fmt.Sprintf("mean %s over %d observed runs", num(m), len(obs))
	if len(unknownIDs) > 0 {
		res.Observed.Reason = fmt.Sprintf("%d observed of %d runs", len(obs), len(obs)+len(unknownIDs))
		note += fmt.Sprintf(", %d unknown left out", len(unknownIDs))
	}
	if satisfied(c, m) {
		res.Status = schemas.PolicyPass
		res.Reason = note
		return res
	}
	res.Status = schemas.PolicyFail
	res.Reason = note + " breaks " + base.Threshold
	for i, r := range obsRuns {
		if !satisfied(c, obs[i]) {
			res.Evidence = appendEvidence(res.Evidence, r.Run.RunID)
		}
	}
	return res
}

// integrity counts the arm's runs with a fired veto gate. A run with no gate
// results and no integrity_violation outcome was not checked, so its absence
// of a violation is unknown: the count is then only a lower bound, which can
// still fail a threshold but never pass one.
func (ev *evaluator) integrity(c schemas.PolicyCheck, base schemas.PolicyCheckResult, minN int) schemas.PolicyCheckResult {
	runs := ev.selectRuns(base.Condition, base.Arm)
	if len(runs) == 0 {
		return unknown(base, noRuns(base), 0, nil)
	}
	var vetoed, unchecked []string
	for _, r := range runs {
		switch {
		case verdict.Vetoed(r):
			vetoed = append(vetoed, r.Run.RunID)
		case len(r.Gates) == 0:
			unchecked = append(unchecked, r.Run.RunID)
		}
	}
	n := len(runs) - len(unchecked)
	count := float64(len(vetoed))
	if n < minN {
		reason := fmt.Sprintf("%d runs with gate results of %d, fewer than min_n %d", n, len(runs), minN)
		return ev.tooFew(base, reason, n, len(runs) < minN, unchecked)
	}
	res := base
	res.N = n
	res.Observed = schemas.Observed(count, schemas.EvidenceOutcome)
	note := fmt.Sprintf("%d of %d runs had a fired veto gate", len(vetoed), n)
	if !satisfied(c, count) {
		res.Status = schemas.PolicyFail
		res.Reason = note + ", breaks " + base.Threshold
		res.Evidence = capEvidence(vetoed)
		return res
	}
	if len(unchecked) > 0 {
		res.Observed = schemas.UnknownWithin(fmt.Sprintf("no gate results in %d runs", len(unchecked)), schemas.EvidenceOutcome, count, count+float64(len(unchecked)))
		return unknown(res, fmt.Sprintf("%s; %d runs have no gate results, so the count is a lower bound", note, len(unchecked)), n, unchecked)
	}
	res.Status = schemas.PolicyPass
	res.Reason = note
	return res
}

// costPerSuccess is the sum of cost_usd over the arm's runs divided by its
// verified successes, so failed runs still count against it (DESIGN.md 11).
// Any unknown cost or unknown success makes it unknown. With no success it is
// undefined: not applicable, read as infinitely expensive, so a max or an
// equals fails and a min passes.
func (ev *evaluator) costPerSuccess(c schemas.PolicyCheck, base schemas.PolicyCheckResult, minN int) schemas.PolicyCheckResult {
	runs := ev.selectRuns(base.Condition, base.Arm)
	if len(runs) == 0 {
		return unknown(base, noRuns(base), 0, nil)
	}
	if len(runs) < minN {
		return ev.tooFew(base, fmt.Sprintf("%d runs, fewer than min_n %d", len(runs), minN), len(runs), true, nil)
	}
	var cost float64
	var costUnknown, succUnknown, spentNoSuccess []string
	succ := 0
	for _, r := range runs {
		x, costOK := r.Metrics[metricCostUSD].Float()
		if costOK && finite(x) {
			cost += x
		} else {
			costUnknown = append(costUnknown, r.Run.RunID)
		}
		s, ok := r.Metrics[metricVerifiedSuccess].Float()
		switch {
		case !ok:
			succUnknown = append(succUnknown, r.Run.RunID)
		case s >= 0.5:
			succ++
		case costOK && x > 0:
			spentNoSuccess = append(spentNoSuccess, r.Run.RunID)
		}
	}
	if len(costUnknown) > 0 {
		return unknown(base, fmt.Sprintf("cost_usd unknown in %d of %d runs", len(costUnknown), len(runs)), len(runs)-len(costUnknown), costUnknown)
	}
	if len(succUnknown) > 0 {
		return unknown(base, fmt.Sprintf("verified_success unknown in %d of %d runs", len(succUnknown), len(runs)), len(runs)-len(succUnknown), succUnknown)
	}
	res := base
	res.N = len(runs)
	if succ == 0 {
		res.Observed = schemas.NotApplicable()
		res.Observed.Evidence = schemas.EvidenceGateway
		res.Observed.Reason = fmt.Sprintf("undefined (0 of %d)", len(runs))
		note := fmt.Sprintf("no verified success in %d runs that cost $%s", len(runs), num(cost))
		if c.Min != nil {
			res.Status, res.Reason = schemas.PolicyPass, note
			return res
		}
		res.Status, res.Reason = schemas.PolicyFail, note+", breaks "+base.Threshold
		res.Evidence = capEvidence(spentNoSuccess)
		return res
	}
	per := cost / float64(succ)
	res.Observed = schemas.Observed(per, schemas.EvidenceGateway)
	note := fmt.Sprintf("$%s over %d verified successes in %d runs", num(cost), succ, len(runs))
	if satisfied(c, per) {
		res.Status, res.Reason = schemas.PolicyPass, note
		return res
	}
	res.Status, res.Reason = schemas.PolicyFail, note+fmt.Sprintf(" is $%s each, breaks %s", num(per), base.Threshold)
	res.Evidence = capEvidence(spentNoSuccess)
	return res
}

// selectRuns returns the arm's counted runs on condition, or on every
// condition for "all".
func (ev *evaluator) selectRuns(condition, arm string) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, r := range ev.runs {
		if r.Slot.Arm == arm && (condition == ConditionAll || r.Slot.Condition == condition) {
			out = append(out, r)
		}
	}
	return out
}

// conditions returns condition itself, or every condition with counted runs
// for "all".
func (ev *evaluator) conditions(condition string) []string {
	if condition != ConditionAll {
		return []string{condition}
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range ev.runs {
		if !seen[r.Slot.Condition] {
			seen[r.Slot.Condition] = true
			out = append(out, r.Slot.Condition)
		}
	}
	return out
}

// tooFew marks a result unknown because fewer than min_n runs were observed.
// small says the experiment is too small even if every unknown run had been
// observed; only then is the unknown a small sample rather than missing data.
func (ev *evaluator) tooFew(res schemas.PolicyCheckResult, reason string, n int, small bool, evidence []string) schemas.PolicyCheckResult {
	if small {
		ev.smallSample = true
		reason += "; needs review: run more tasks or trials"
	}
	return unknown(res, reason, n, evidence)
}

// blocking reports whether one result alone blocks the release. A fail on a
// block-severity check always blocks. An unknown follows on_unknown; when it
// is unset, a small sample only warns and any other unknown blocks.
func blocking(c schemas.PolicyCheck, status schemas.PolicyStatus, onUnknown string, smallSample bool) bool {
	if c.Severity == SeverityWarn {
		return false
	}
	switch status {
	case schemas.PolicyFail:
		return true
	case schemas.PolicyUnknown:
		switch onUnknown {
		case SeverityWarn:
			return false
		case SeverityBlock:
			return true
		default:
			return !smallSample
		}
	default:
		return false
	}
}

// notApplicableValue is the value a not-applicable run has for metric when
// it is still known. false_completion is not applicable on a passing run, and
// a passing run is never a false completion, so it counts as 0: the rate is
// over every counted run whose outcome and claim are known, not only failed
// runs. Every other metric's not-applicable runs are left out.
func notApplicableValue(metric string) (float64, bool) {
	if metric == "false_completion" {
		return 0, true
	}
	return 0, false
}

// hashMismatch is the result added when the policy file changed after the
// experiment froze its hash.
func hashMismatch(frozen, now string) schemas.PolicyCheckResult {
	return schemas.PolicyCheckResult{
		ID:        CheckPolicyHash,
		Metric:    CheckPolicyHash,
		Status:    schemas.PolicyFail,
		Blocking:  true,
		Observed:  schemas.NotApplicable(),
		Threshold: "sha256 " + shortHash(frozen),
		Reason: fmt.Sprintf("the policy file hashes to %s but the manifest froze %s: the policy changed after the experiment was planned",
			shortHash(now), shortHash(frozen)),
	}
}

// unknown marks a result unknown with its reason. The observed value is
// unknown too, unless the caller already set a bounded unknown.
func unknown(res schemas.PolicyCheckResult, reason string, n int, evidence []string) schemas.PolicyCheckResult {
	res.Status = schemas.PolicyUnknown
	res.Reason = reason
	res.N = n
	if res.Observed.State != schemas.ValueUnknown {
		res.Observed = schemas.Unknown(reason, "")
	}
	res.Evidence = capEvidence(evidence)
	return res
}

// satisfied reports whether x meets the check's threshold.
func satisfied(c schemas.PolicyCheck, x float64) bool {
	switch {
	case c.Max != nil:
		return x <= *c.Max+equalsTolerance*math.Max(1, math.Abs(*c.Max))
	case c.Min != nil:
		return x >= *c.Min-equalsTolerance*math.Max(1, math.Abs(*c.Min))
	case c.Equals != nil:
		return math.Abs(x-*c.Equals) <= equalsTolerance*math.Max(1, math.Abs(*c.Equals))
	default:
		return false
	}
}

// thresholdText is the human form of the check's threshold.
func thresholdText(c schemas.PolicyCheck) string {
	switch {
	case c.Rule == RuleNoRegression:
		return ThresholdNoRegression
	case c.Rule == RuleImproved:
		return ThresholdImproved
	case c.Rule == RuleTimeoutParity:
		return verdict.TimeoutParityThreshold
	case c.Max != nil:
		return "<= " + num(*c.Max)
	case c.Min != nil:
		return ">= " + num(*c.Min)
	case c.Equals != nil:
		return "= " + num(*c.Equals)
	default:
		return c.Rule
	}
}

// timeoutParity applies the verdict's timeout_parity rule to the check's arm
// against arm A on the check's condition ("all" pools every condition). The
// observed value is the arm's timeout rate; a failure lists the arm's
// timed-out runs. An arm without runs is unknown, never a pass.
func (ev *evaluator) timeoutParity(base schemas.PolicyCheckResult) schemas.PolicyCheckResult {
	status, per, detail := verdict.TimeoutParity(ev.runs, base.Condition, BaselineArm, base.Arm)
	res := base
	r := per[base.Arm]
	res.N = r.Den
	res.Reason = detail
	switch status {
	case schemas.GuardPass:
		res.Status = schemas.PolicyPass
	case schemas.GuardFail:
		res.Status = schemas.PolicyFail
		for _, run := range ev.selectRuns(base.Condition, base.Arm) {
			if verdict.TimedOut(run) {
				res.Evidence = appendEvidence(res.Evidence, run.Run.RunID)
			}
		}
	default:
		return unknown(res, detail, r.Den, nil)
	}
	res.Observed = schemas.Observed(float64(r.Num)/float64(r.Den), schemas.EvidenceOutcome)
	return res
}

// noRuns is the reason for an arm with no counted runs on the condition.
func noRuns(r schemas.PolicyCheckResult) string {
	return fmt.Sprintf("arm %s has no counted runs on %s", r.Arm, r.Condition)
}

// appendEvidence adds one run id while under the cap.
func appendEvidence(ids []string, id string) []string {
	if len(ids) >= maxEvidence {
		return ids
	}
	return append(ids, id)
}

// capEvidence returns at most maxEvidence ids, nil for none.
func capEvidence(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	return append([]string(nil), ids[:min(len(ids), maxEvidence)]...)
}

// num formats a number without trailing zeros.
func num(x float64) string {
	return strconv.FormatFloat(x, 'g', 6, 64)
}

// shortHash is the first 12 hex characters of a hash, or "none".
func shortHash(h string) string {
	if h == "" {
		return "none"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// finite reports whether x is neither NaN nor infinite.
func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
