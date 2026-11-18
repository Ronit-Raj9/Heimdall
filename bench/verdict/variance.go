package verdict

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// InstrumentAANoiseFloor is the instrument check of the A/A arm
// (docs/noise-floor.md): arm A2, a second copy of arm A, must not differ from
// arm A on the clean condition. A directional difference fails it, and the
// verdict is INVALID with a "harness noise:" reason.
const InstrumentAANoiseFloor = "aa_noise_floor"

// VarianceObservedEffect is the variance card source of an audit: the SD of
// the published table's own per-task differences, which is neither an A/A
// measurement nor an assumption.
const VarianceObservedEffect = schemas.VarianceObservedEffect

// harnessNoisePrefix starts the invalid reason of a directional A/A result.
const harnessNoisePrefix = "harness noise: "

// hasAAArm reports whether the manifest's arm A2 is an A/A copy of arm A: the
// experiment set aa: true, or A2 resolved to arm A's config hash. A declared
// arm that is merely named A2 never has A's hash, because the planner refuses
// two declared arms with one hash, so it is never read as a noise floor.
func hasAAArm(m *schemas.Manifest) bool {
	if m.Experiment.AA {
		return true
	}
	a2, ok := m.Arms[ArmAA]
	a, okA := m.Arms[ArmBaseline]
	return ok && okA && a2.ConfigHash == a.ConfigHash
}

// noiseFloor builds the variance card and the aa_noise_floor check. The card
// carries the SD the MDE and tasks needed use: the SD of the per-task
// differences A2 minus A on clean (observed runs, at least two tasks) when
// the experiment ran an A/A arm, else the assumed SD, labelled as such. A
// measured SD of exactly 0 (A2 agreed with A on every task, as deterministic
// runs do) is recorded but not used: an MDE of 0 would claim that any effect
// is detectable, which no sign-flip test over a few tasks can do. The caller
// fills in the MDE.
//
// The A/A effect is the paired sign-flip of A2 against A on clean and the
// primary metric, at both missingness bounds, like the primary effect. Two
// copies of one config differ only by chance and by the harness, so a
// difference significant at both bounds fails the check: the harness, not
// the agent, moved the number. A difference significant at one bound only,
// or no task with runs in both arms, leaves the check unknown, which holds an
// IMPROVED verdict. Without an A/A arm the check is not applicable.
func noiseFloor(cfg settings, idx runIndex, hasAA bool) (*schemas.VarianceCard, schemas.InstrumentCheck) {
	clean, metric := schemas.ConditionClean, cfg.primary.Metric
	card := &schemas.VarianceCard{Source: schemas.VarianceAssumed, SD: cfg.assumedSD, AssumedSD: cfg.assumedSD}
	check := schemas.InstrumentCheck{
		Name:      InstrumentAANoiseFloor,
		Threshold: fmt.Sprintf("arm %s against arm %s on %s, %s: not significant at alpha %.2f at either missingness bound", ArmAA, ArmBaseline, clean, metric, cfg.alpha),
	}
	if !hasAA {
		check.Status = schemas.InstrumentNotApplicable
		check.Detail = "the experiment ran no A/A arm (aa: true adds one)"
		card.Detail = fmt.Sprintf("SD %.2f assumed: the experiment ran no A/A arm (aa: true adds one), so the noise floor is not measured", cfg.assumedSD)
		return card, check
	}
	eff := pairedEffect(clean, metric, ArmBaseline, ArmAA, idx.cells(clean, ArmBaseline, metric), idx.cells(clean, ArmAA, metric), true)
	decision := aaDecision(eff, cfg.alpha)
	card.AAArm, card.AATasks, card.AAEffect, card.AADecision = ArmAA, eff.NTasks, eff, &decision
	if sd, ok := aaSD(idx, metric); ok {
		card.MeasuredSD = &sd
		if sd > 0 {
			card.Source, card.SD = schemas.VarianceMeasuredAA, sd
		}
	}
	card.Detail = varianceDetail(card, metric)
	check.Status, check.Detail = aaStatus(eff, decision, cfg.alpha)
	return card, check
}

// aaDecision is the A/A comparison read as a release decision: improved or
// regressed when A2 differs from A significantly at both missingness bounds,
// in the metric's direction, inconclusive otherwise. Either directional
// result is harness noise.
func aaDecision(e *schemas.Effect, alpha float64) schemas.Decision {
	switch {
	case improvedAtBoth(e, alpha):
		return schemas.DecisionImproved
	case regressedAtBoth(e, alpha):
		return schemas.DecisionRegressed
	default:
		return schemas.DecisionInconclusive
	}
}

// aaStatus is the aa_noise_floor status and detail for the A/A effect. A
// failed check's detail is the verdict's invalid reason, so it starts with
// "harness noise:" and names the arms, the metric, d_bar and p.
func aaStatus(e *schemas.Effect, d schemas.Decision, alpha float64) (schemas.InstrumentStatus, string) {
	where := fmt.Sprintf("arm %s (a copy of arm %s) against arm %s on %s, %s", ArmAA, ArmBaseline, ArmBaseline, e.Condition, e.Metric)
	switch {
	case pairedTasks(e) == 0:
		return schemas.InstrumentUnknown, fmt.Sprintf("no task has counted runs in both arm %s and arm %s on %s, so the A/A comparison could not be made", ArmBaseline, ArmAA, e.Condition)
	case e.LowerBound == nil || e.UpperBound == nil:
		return schemas.InstrumentUnknown, where + ": unknown values with no bounds; a metric outside [0, 1] cannot be bounded, so harness noise cannot be ruled out"
	case d != schemas.DecisionInconclusive:
		return schemas.InstrumentFail, fmt.Sprintf("%s%s: d_bar %s, p = %s over %d tasks (%d discordant), significant at both missingness bounds (d_bar %s / %s, p %s / %s); two copies of one config should not differ, so the harness, not the agent, moved the number",
			harnessNoisePrefix, where, diffText(e.Metric, e.DBar), pText(e), e.NTasks, e.NNonzero,
			diffText(e.Metric, e.LowerBound.DBar), diffText(e.Metric, e.UpperBound.DBar), pText(e.LowerBound), pText(e.UpperBound))
	case significant(e.LowerBound, alpha, -1) || significant(e.LowerBound, alpha, 1) ||
		significant(e.UpperBound, alpha, -1) || significant(e.UpperBound, alpha, 1):
		return schemas.InstrumentUnknown, fmt.Sprintf("%s: significant at one missingness bound only (d_bar %s / %s, p %s / %s), so harness noise cannot be ruled out",
			where, diffText(e.Metric, e.LowerBound.DBar), diffText(e.Metric, e.UpperBound.DBar), pText(e.LowerBound), pText(e.UpperBound))
	default:
		return schemas.InstrumentPass, fmt.Sprintf("%s: d_bar %s, p = %s over %d tasks (%d discordant), not significant at either missingness bound",
			where, diffText(e.Metric, e.DBar), pText(e), e.NTasks, e.NNonzero)
	}
}

// varianceDetail is the card's one sentence: which SD the MDE used and why.
func varianceDetail(c *schemas.VarianceCard, metric string) string {
	switch {
	case c.Source == schemas.VarianceMeasuredAA:
		return fmt.Sprintf("SD %.3f measured from the A/A arm: per-task differences %s minus %s on %s, %s, over %d tasks; it replaces the assumed %.2f. An SD from few tasks is itself noisy",
			c.SD, ArmAA, ArmBaseline, schemas.ConditionClean, metric, c.AATasks, c.AssumedSD)
	case c.MeasuredSD != nil:
		return fmt.Sprintf("SD %.2f assumed: arms %s and %s agree on every one of %d tasks on %s (SD 0), and an SD of 0 cannot size an MDE",
			c.AssumedSD, ArmAA, ArmBaseline, c.AATasks, schemas.ConditionClean)
	default:
		return fmt.Sprintf("SD %.2f assumed: the A/A arm has observed runs beside arm %s on %d task(s) of %s, and an SD needs 2",
			c.AssumedSD, ArmBaseline, c.AATasks, schemas.ConditionClean)
	}
}

// diffText formats a difference on metric: percentage points on a rate
// metric, the plain number otherwise.
func diffText(metric string, d float64) string {
	if RateMetric(metric) {
		return fmt.Sprintf("%+.1f pp", 100*d)
	}
	return fmt.Sprintf("%+.4g", d)
}

// pText formats an effect's p-value, "none" when it has none.
func pText(e *schemas.Effect) string {
	if e == nil || e.PValue == nil {
		return "none"
	}
	return fmt.Sprintf("%.4f", *e.PValue)
}
