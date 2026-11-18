package verdict

import (
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// W7 adaptive trials and suite tiers: what the verdict adds. Every field it
// sets is optional, so a manifest without an adaptive plan, a tier or an
// extension rule gets the verdict it always got, plus the "buy more
// evidence" line on an INCONCLUSIVE one.

// defaultTargetPP is the effect the "buy more evidence" line sizes for when
// the observed d_bar is zero.
const defaultTargetPP = 10.0

// DecisionAlpha is the alpha the primary decision uses: the look alpha a
// sequential design stopped at, the first-stage alpha when the experiment
// pre-declared an extension rule, else the verdict alpha.
func DecisionAlpha(m *schemas.Manifest, alpha float64, rep *schemas.AdaptiveReport) float64 {
	if rep != nil && rep.DecisionAlpha > 0 {
		return rep.DecisionAlpha
	}
	if a, ok := ExtensionAlphaFirst(m, alpha); ok {
		return a
	}
	return alpha
}

// ExtensionAlphaFirst is the alpha the experiment's own analysis keeps when
// it pre-declared an extension rule (default half of alpha), and true.
func ExtensionAlphaFirst(m *schemas.Manifest, alpha float64) (float64, bool) {
	if m == nil || m.Experiment.Trials == nil || m.Experiment.Trials.Extension == nil {
		return alpha, false
	}
	a := m.Experiment.Trials.Extension.AlphaFirst
	if a <= 0 || a >= alpha {
		a = alpha / 2
	}
	return a, true
}

// primaryAlpha is the alpha the primary decision uses.
func (s settings) primaryAlpha() float64 {
	if s.decisionAlpha > 0 {
		return s.decisionAlpha
	}
	return s.alpha
}

// w7Verdict fills the adaptive report, the tier and the "buy more evidence"
// line, and adds the adaptive_allocation check. raw are the runs before
// AdaptiveRuns and CompleteSlots; runs after.
func w7Verdict(m *schemas.Manifest, cfg settings, raw, runs []schemas.SealedRun, idx runIndex, v *schemas.Verdict) {
	if rep := ReplayAdaptive(m, runs); rep != nil {
		rep.Excluded = excludedRuns(m, raw, ReplayAdaptive(m, raw))
		rep.DecisionAlpha = DecisionAlpha(m, cfg.alpha, rep)
		v.Adaptive = rep
		v.Instruments = append(v.Instruments, adaptiveCheck(rep))
	}
	extensionStage(m, v)
	v.Tier = m.Experiment.Tier
	if m.Tier != nil && m.Tier.Name != "" {
		v.Tier = m.Tier.Name
	}
	if v.Decision == schemas.DecisionInconclusive {
		v.MoreEvidence = moreEvidence(m, cfg, idx, v)
	}
}

// moreEvidence sizes a follow-up: tasks needed to detect |d_bar| (or 10 pp
// when d_bar is zero) at the verdict's alpha with 80% power, from the
// measured SD when an A/A arm gave one, else the assumed SD; runs per task
// and cost per run from this experiment's own manifest and sealed runs.
func moreEvidence(m *schemas.Manifest, cfg settings, idx runIndex, v *schemas.Verdict) *schemas.MoreEvidence {
	target := 0.0
	if v.Effect != nil {
		target = math.Abs(v.Effect.DBar)
	}
	if target*100 < 1 {
		target = defaultTargetPP / 100
	}
	n := pairedTasks(v.Effect)
	total := stats.TasksNeeded(target, v.SD, cfg.alpha, mdePower, minTasksNeeded, maxTasksNeeded)
	out := &schemas.MoreEvidence{
		TargetPP:    100 * target,
		TasksTotal:  total,
		SD:          v.SD,
		SDAssumed:   v.MDEAssumedSD,
		Combination: schemas.ExtensionExploratory,
		CostBasis:   "no observed cost in this experiment",
	}
	if total >= maxTasksNeeded {
		out.Over = true
	}
	out.Tasks = max(total-n, 0)
	perTask := runsPerTask(m)
	out.Runs = out.Tasks * perTask
	if c, k, ok := costPerRun(idx); ok {
		cost := float64(out.Runs) * c
		out.CostUSD = &cost
		out.CostBasis = fmt.Sprintf("mean cost %.4f USD per run over %d sealed runs of this experiment, times %d runs per task", c, k, perTask)
	}
	if a1, ok := ExtensionAlphaFirst(m, cfg.alpha); ok {
		out.Combination = schemas.ExtensionInverseNormal
		out.AlphaKept = cfg.alpha - a1
	}
	if out.Tasks > 0 && m.Experiment.Extends == nil { // a follow-up is not extended again: size a new experiment instead
		out.Command = fmt.Sprintf("heimdall extend -exp %s -tasks %d", m.ExpID, out.Tasks)
	}
	return out
}

// runsPerTask is the counted slots of the maximum design per task, the runs
// one more task of the same design costs.
func runsPerTask(m *schemas.Manifest) int {
	tasks := map[string]bool{}
	slots := 0
	for _, sl := range m.Slots {
		if sl.Counted {
			slots++
			tasks[sl.Task] = true
		}
	}
	if len(tasks) == 0 {
		return 0
	}
	return int(math.Ceil(float64(slots) / float64(len(tasks))))
}

// costPerRun is the mean observed cost_usd over counted runs.
func costPerRun(idx runIndex) (float64, int, bool) {
	sum, n := 0.0, 0
	for _, byTask := range idx {
		for _, byArm := range byTask {
			for _, rs := range byArm {
				for _, r := range rs {
					if x, ok := metricValue(r, MetricCostUSD).Float(); ok && finite(x) {
						sum += x
						n++
					}
				}
			}
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return sum / float64(n), n, true
}

// InstrumentExtensionStage is the check that marks a follow-up whose
// decision belongs to the combined analysis with the experiment it extends.
const InstrumentExtensionStage = "extension_stage"

// extensionStage keeps a follow-up planned under a pre-declared combination
// rule from deciding on its own: its primary effect is one stage of the
// combined test (`extend status`), and reading it alone as well would spend
// alpha twice. A directional decision from the primary becomes INCONCLUSIVE;
// a guard that failed still makes it REGRESSED, since holding is always
// allowed.
func extensionStage(m *schemas.Manifest, v *schemas.Verdict) {
	ref := m.Experiment.Extends
	if ref == nil || ref.Rule != schemas.ExtensionInverseNormal {
		return
	}
	c := schemas.InstrumentCheck{Name: InstrumentExtensionStage, Status: schemas.InstrumentWarn,
		Threshold: "a follow-up under a pre-declared combination rule decides only together with the experiment it extends",
		Detail:    fmt.Sprintf("stage 2 of %s: the decision is `heimdall extend status -exp %s`", ref.ExpID, m.ExpID)}
	guardFailed := anyGuard(decisionGuards(v.Guards), schemas.GuardFail)
	if v.Decision == schemas.DecisionImproved || (v.Decision == schemas.DecisionRegressed && !guardFailed) {
		c.Detail = fmt.Sprintf("its own primary result was %s; as stage 2 of %s it is not a decision: run `heimdall extend status -exp %s`", v.Decision, ref.ExpID, m.ExpID)
		v.Decision = schemas.DecisionInconclusive
		v.Action = actionFor(v.Decision, v.Guards, v.Integrity, v.Instruments)
	}
	v.Instruments = append(v.Instruments, c)
}
