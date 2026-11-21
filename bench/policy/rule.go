package policy

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// ruleOutcome is one condition's result for a rule check.
type ruleOutcome struct {
	condition string
	status    schemas.PolicyStatus
	effect    *schemas.Effect
	note      string
}

// rule evaluates no_regression or improved on the release effect of the
// check's arm over arm A, computed by the verdict package exactly as the
// verdict's own guards compute it. no_regression fails only when the effect is
// REGRESSED at both missingness bounds and is unknown when the bounds
// disagree, because the missing runs could decide it. improved passes only
// when the effect is IMPROVED at both bounds. With condition "all" every
// condition is checked and the worst result stands.
func (ev *evaluator) rule(c schemas.PolicyCheck, base schemas.PolicyCheckResult, minN int) schemas.PolicyCheckResult {
	armRuns := ev.selectRuns(base.Condition, base.Arm)
	if len(armRuns) == 0 {
		return unknown(base, noRuns(base), 0, nil)
	}
	obsN, unknownN, recorded, level := 0, 0, false, schemas.EvidenceLevel("")
	for _, r := range armRuns {
		v, ok := runMetric(r, c.Metric)
		recorded = recorded || ok
		if x, ok := v.Float(); ok && finite(x) {
			if obsN == 0 {
				level = v.Evidence
			}
			obsN++
		} else if v.State != schemas.ValueNotApplicable {
			unknownN++
		}
	}
	if !recorded {
		return unknown(base, fmt.Sprintf("metric %s is not recorded in any of the %d runs of arm %s on %s", c.Metric, len(armRuns), base.Arm, base.Condition), 0, nil)
	}
	if obsN < minN {
		return ev.tooFew(base, fmt.Sprintf("%d observed runs of %d, fewer than min_n %d", obsN, len(armRuns), minN), obsN, obsN+unknownN < minN, nil)
	}
	if unbounded := ev.unboundedUnknowns(c.Metric, base.Condition, base.Arm); len(unbounded) > 0 && !unitInterval(c.Metric) {
		return unknown(base, fmt.Sprintf("%d runs have an unknown %s with no bounds; the missingness bounds of a metric outside [0, 1] need them",
			len(unbounded), c.Metric), obsN, unbounded)
	}
	sign := direction(c.Metric)
	var decided *ruleOutcome
	for _, cond := range ev.conditions(base.Condition) {
		eff := verdict.ReleaseEffect(ev.runs, cond, c.Metric, BaselineArm, base.Arm)
		o := ruleOutcomeFor(c.Rule, eff, sign, ev.alpha)
		o.condition = cond
		if decided == nil || worse(o, *decided, sign) {
			decided = &o
		}
	}
	res := base
	res.N = obsN
	res.Status = decided.status
	res.Reason = decided.condition + ": " + decided.note
	if decided.effect == nil || decided.effect.NTasks == 0 {
		res.Observed = schemas.Unknown(decided.note, level)
		return res
	}
	res.Observed = schemas.Observed(decided.effect.DBar, level)
	if res.Status == schemas.PolicyUnknown {
		res.Evidence = nil
		return res
	}
	if res.Status == schemas.PolicyFail {
		res.Evidence = ev.badTaskRuns(decided, base.Arm, sign)
	}
	return res
}

// ruleOutcomeFor reads one condition's effect at both missingness bounds.
// sign is +1 when a rise in the metric is better and -1 when it is worse, so
// a fall in verified_success and a rise in cost_usd are both regressions.
func ruleOutcomeFor(rule string, eff *schemas.Effect, sign int, alpha float64) ruleOutcome {
	if eff == nil || eff.LowerBound == nil || eff.UpperBound == nil || eff.LowerBound.NTasks == 0 {
		return ruleOutcome{status: schemas.PolicyUnknown, effect: eff, note: "no task ran in both arms"}
	}
	lo := sign * verdict.Direction(eff.LowerBound, alpha)
	hi := sign * verdict.Direction(eff.UpperBound, alpha)
	p := "unknown"
	if eff.PValue != nil {
		p = num(*eff.PValue)
	}
	bounds := fmt.Sprintf("d_bar %+.1f pp (%+.1f / %+.1f pp at the missingness bounds), p %s, %d of %d tasks discordant",
		100*eff.DBar, 100*eff.LowerBound.DBar, 100*eff.UpperBound.DBar, p, eff.NNonzero, eff.NTasks)
	o := ruleOutcome{effect: eff}
	switch rule {
	case RuleNoRegression:
		switch {
		case lo == -1 && hi == -1:
			o.status, o.note = schemas.PolicyFail, "regressed at both bounds, "+bounds
		case lo == -1 || hi == -1:
			o.status, o.note = schemas.PolicyUnknown, "regressed at one bound only, "+bounds
		default:
			o.status, o.note = schemas.PolicyPass, "not regressed at either bound, "+bounds
		}
	case RuleImproved:
		switch {
		case lo == 1 && hi == 1:
			o.status, o.note = schemas.PolicyPass, "improved at both bounds, "+bounds
		case lo == 1 || hi == 1:
			o.status, o.note = schemas.PolicyUnknown, "improved at one bound only, "+bounds
		default:
			o.status, o.note = schemas.PolicyFail, "not improved at both bounds, "+bounds
		}
	default:
		o.status, o.note = schemas.PolicyUnknown, fmt.Sprintf("unknown rule %q", rule)
	}
	return o
}

// worse reports whether o should stand over cur: fail over unknown over pass,
// and between equals the condition whose effect is worse for the candidate.
func worse(o, cur ruleOutcome, sign int) bool {
	rank := map[schemas.PolicyStatus]int{schemas.PolicyPass: 0, schemas.PolicyUnknown: 1, schemas.PolicyFail: 2}
	if rank[o.status] != rank[cur.status] {
		return rank[o.status] > rank[cur.status]
	}
	if o.effect == nil || o.effect.NTasks == 0 || cur.effect == nil || cur.effect.NTasks == 0 {
		return false
	}
	return float64(sign)*o.effect.DBar < float64(sign)*cur.effect.DBar
}

// unboundedUnknowns returns the runs of arm A and of arm, on condition, whose
// value of metric is unknown without bounds. The verdict fills such a value
// with [0, 1], which only holds for a metric whose values lie there.
func (ev *evaluator) unboundedUnknowns(metric, condition, arm string) []string {
	var ids []string
	for _, a := range []string{BaselineArm, arm} {
		for _, r := range ev.selectRuns(condition, a) {
			v, ok := runMetric(r, metric)
			if !ok || (!v.IsObserved() && v.State != schemas.ValueNotApplicable && v.Bounds == nil) {
				ids = append(ids, r.Run.RunID)
			}
		}
	}
	return ids
}

// badTaskRuns lists the arm's runs on the tasks where it did worse than arm A
// in the deciding condition: the runs behind a failed rule.
func (ev *evaluator) badTaskRuns(o *ruleOutcome, arm string, sign int) []string {
	bad := map[string]bool{}
	for _, td := range o.effect.PerTask {
		if float64(sign)*td.D < 0 {
			bad[td.Task] = true
		}
	}
	var ids []string
	for _, r := range ev.selectRuns(o.condition, arm) {
		if bad[r.Slot.Task] {
			ids = appendEvidence(ids, r.Run.RunID)
		}
	}
	return ids
}

// direction is +1 when a rise in metric is better and -1 when a rise is a
// regression, read from verdict.HigherIsBetter so a policy rule and the
// verdict's guards never disagree on which way is better.
func direction(metric string) int {
	if verdict.HigherIsBetter(metric) {
		return 1
	}
	return -1
}

// unitInterval reports whether every value of metric lies in [0, 1]
// (verdict.RateMetric). For those an unknown run with no bounds can be
// filled with 0 and with 1 to get the missingness bounds.
func unitInterval(metric string) bool {
	return verdict.RateMetric(metric)
}
