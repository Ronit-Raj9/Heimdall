package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	suitepkg "github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/verdict"
)

// W7 adaptive trials and suite tiers (docs/adaptive-trials.md,
// docs/suite-tiers.md): the planner's part. The tier narrows `tasks: all`
// before anything else is resolved; the trials policy is checked there too,
// and after the slots are laid out the maximum design's optional slots are
// marked and the frozen plan is returned for the manifest. A fixed
// experiment without a tier gets nil for both, so its manifest is unchanged.

// lookStream is the second PCG word of the sequential look draw, apart from
// the arm-order stream so neither draw moves the other.
const lookStream = 0x6c6f6f6b // "look"

// minTasksPerLook is the size below which a look can never decide: with
// fewer than 6 discordant tasks the sign-flip p is never below 0.05.
const minTasksPerLook = 6

// w7Conditions applies the experiment's tier to the resolved conditions and
// checks the trials policy against them. `tasks: all` becomes the tier's
// tasks; an explicit list must lie inside the tier.
func w7Conditions(suite *schemas.Suite, e *schemas.Experiment, conds []condPlan) ([]condPlan, *schemas.TierPlan, error) {
	var tier *schemas.TierPlan
	if e.Tier != "" {
		tasks, err := suitepkg.TierTasks(suite, e.Tier)
		if err != nil {
			return nil, nil, err
		}
		in := map[string]bool{}
		for _, t := range tasks {
			in[t] = true
		}
		out := make([]condPlan, len(conds))
		for i, c := range conds {
			spec := e.Conditions[i]
			out[i] = c
			if spec.Tasks.All {
				var kept []string
				for _, t := range c.tasks {
					if in[t] {
						kept = append(kept, t)
					}
				}
				if len(kept) == 0 {
					return nil, nil, fmt.Errorf("condition %s selects no task of tier %s", c.id, e.Tier)
				}
				out[i].tasks = kept
				continue
			}
			for _, t := range c.tasks {
				if !in[t] {
					return nil, nil, fmt.Errorf("condition %s lists task %s, which is not in tier %s", c.id, t, e.Tier)
				}
			}
		}
		conds = out
		tier = &schemas.TierPlan{Name: e.Tier, Tasks: tasks}
	}
	if err := checkTrials(e, conds); err != nil {
		return nil, nil, fmt.Errorf("trials: %w", err)
	}
	return conds, tier, nil
}

// adaptiveConditions are the conditions the rule applies to: the policy's
// list, else the decision conditions (the primary, then every no_regression
// guard's, or clean when the experiment declares no guards, as the verdict
// defaults), kept only when the experiment runs them.
func adaptiveConditions(e *schemas.Experiment, conds []condPlan) []string {
	have := map[string]bool{}
	for _, c := range conds {
		have[c.id] = true
	}
	var want []string
	switch {
	case e.Trials != nil && len(e.Trials.Conditions) > 0:
		want = e.Trials.Conditions
	case e.Trials != nil && e.Trials.Rule == schemas.AdaptiveSequential:
		want = []string{e.Primary.Condition}
	default:
		want = []string{e.Primary.Condition}
		if len(e.Guards) == 0 {
			want = append(want, schemas.ConditionClean)
		}
		for _, g := range e.Guards {
			if g.Rule == GuardNoRegression {
				want = append(want, g.Condition)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range want {
		if have[c] && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// checkTrials validates the trials policy before any I/O-heavy work.
func checkTrials(e *schemas.Experiment, conds []condPlan) error {
	t := e.Trials
	if t == nil {
		return nil
	}
	alpha := e.Verdict.Alpha
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.05
	}
	if x := t.Extension; x != nil {
		if x.Rule != schemas.ExtensionInverseNormal {
			return fmt.Errorf("extension rule %q: want %s", x.Rule, schemas.ExtensionInverseNormal)
		}
		if x.AlphaFirst < 0 || x.AlphaFirst >= alpha {
			return fmt.Errorf("extension alpha_first %v must lie in (0, %v)", x.AlphaFirst, alpha)
		}
		if x.WeightFirst < 0 || x.WeightFirst >= 1 {
			return fmt.Errorf("extension weight_first %v must lie in (0, 1)", x.WeightFirst)
		}
	}
	switch t.Mode {
	case "", schemas.TrialsFixed:
		if t.Rule != "" {
			return fmt.Errorf("rule %q needs mode adaptive", t.Rule)
		}
		return nil
	case schemas.TrialsAdaptive:
	default:
		return fmt.Errorf("mode %q: want fixed or adaptive", t.Mode)
	}
	if p := e.Verdict.Policy; p != "" && p != verdict.PolicyPairedSignFlip {
		return fmt.Errorf("adaptive trials need the %s verdict policy, not %q", verdict.PolicyPairedSignFlip, p)
	}
	if _, ok := e.Arms[verdict.ArmBaseline]; !ok {
		return errors.New("adaptive trials need arms A and B")
	}
	if _, ok := e.Arms[verdict.ArmCandidate]; !ok {
		return errors.New("adaptive trials need arms A and B")
	}
	known := map[string]bool{}
	for _, c := range conds {
		known[c.id] = true
	}
	for _, id := range t.Conditions {
		if !known[id] {
			return fmt.Errorf("trials condition %q is not a condition of the experiment", id)
		}
	}
	if t.Rule == schemas.AdaptiveSequential && len(t.Conditions) > 0 {
		primary := e.Primary.Condition
		if primary == "" {
			primary = schemas.ConditionClean
		}
		if len(t.Conditions) != 1 || t.Conditions[0] != primary {
			return fmt.Errorf("rule sequential: conditions %v must be only the primary condition %s, since the looks stop the primary endpoint", t.Conditions, primary)
		}
	}
	adaptive := adaptiveConditions(e, conds)
	if len(adaptive) == 0 {
		return errors.New("no condition of the experiment is adaptive")
	}
	byID := map[string]condPlan{}
	for _, c := range conds {
		byID[c.id] = c
	}
	switch t.Rule {
	case schemas.AdaptiveTask:
		if t.Min < 1 {
			return fmt.Errorf("rule task: min must be >= 1, got %d", t.Min)
		}
		for _, id := range adaptive {
			c := byID[id]
			maxT := t.Max
			if maxT == 0 {
				maxT = c.trials
			}
			if c.trials != maxT {
				return fmt.Errorf("rule task: condition %s plans %d trials but max is %d; with adaptive trials a condition's trials is the maximum", id, c.trials, maxT)
			}
			if t.Min >= maxT {
				return fmt.Errorf("rule task: min %d must be below max %d on condition %s", t.Min, maxT, id)
			}
		}
		if t.Looks != 0 || t.AlphaSplit != "" || len(t.Alphas) > 0 {
			return errors.New("rule task: looks, alpha_split and alphas belong to rule sequential")
		}
	case schemas.AdaptiveSequential:
		if t.Extension != nil {
			return errors.New("rule sequential already spends alpha over its looks; it cannot also declare an extension")
		}
		if t.Min != 0 || t.Max != 0 {
			return errors.New("rule sequential: min and max belong to rule task; every task of a look runs its condition's trials")
		}
		if t.Looks < 2 || t.Looks > 5 {
			return fmt.Errorf("rule sequential: looks must be between 2 and 5, got %d", t.Looks)
		}
		n := len(byID[e.Primary.Condition].tasks)
		if n < t.Looks {
			return fmt.Errorf("rule sequential: %d looks need at least %d tasks on %s, got %d", t.Looks, t.Looks, e.Primary.Condition, n)
		}
		if _, err := verdict.LookAlphas(lookSizes(n, t.Looks), alpha, t.AlphaSplit, t.Alphas); err != nil {
			return fmt.Errorf("rule sequential: %w", err)
		}
		if t.Futility != "" && t.Futility != schemas.FutilityCurtail && t.Futility != schemas.FutilityNonBinding && t.Futility != schemas.FutilityOff {
			return fmt.Errorf("rule sequential: futility %q: want curtail, nonbinding or off", t.Futility)
		}
	default:
		return fmt.Errorf("adaptive rule %q: want task or sequential", t.Rule)
	}
	return nil
}

// lookSizes are the sizes SplitLooks gives n tasks in looks groups.
func lookSizes(n, looks int) []int {
	out := make([]int, looks)
	for i := range out {
		out[i] = n / looks
		if i < n%looks {
			out[i]++
		}
	}
	return out
}

// planAdaptive marks the optional slots of the maximum design and returns
// the frozen plan; nil for a fixed design. It runs after buildSlots and
// before the run inputs and the hash, so the plan and every slot's Optional
// flag are part of the hashed manifest.
func planAdaptive(e *schemas.Experiment, m *schemas.Manifest, conds []condPlan, logger schemas.Logger) (*schemas.AdaptivePlan, error) {
	t := e.Trials
	if t == nil || t.Mode != schemas.TrialsAdaptive {
		return nil, nil
	}
	alpha := e.Verdict.Alpha
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.05
	}
	metric := e.Primary.Metric
	if metric == "" {
		metric = verdict.MetricVerifiedSuccess
	}
	plan := &schemas.AdaptivePlan{Rule: t.Rule, Conditions: adaptiveConditions(e, conds), Metric: metric, Alpha: alpha}
	if a, ok := verdict.ExtensionAlphaFirst(&schemas.Manifest{Experiment: *e}, alpha); ok {
		plan.Alpha = a
	}
	adaptive := map[string]bool{}
	for _, c := range plan.Conditions {
		adaptive[c] = true
	}
	var optional func(sl schemas.Slot) bool
	switch t.Rule {
	case schemas.AdaptiveTask:
		plan.Min = t.Min
		for _, c := range conds {
			if adaptive[c.id] {
				plan.Max = c.trials
			}
		}
		optional = func(sl schemas.Slot) bool { return adaptive[sl.Condition] && sl.Trial > t.Min }
	case schemas.AdaptiveSequential:
		var primary []string
		for _, c := range conds {
			if c.id == e.Primary.Condition {
				primary = append([]string(nil), c.tasks...)
			}
		}
		sort.Strings(primary)
		r := rand.New(rand.NewPCG(uint64(e.Seed), lookStream))
		r.Shuffle(len(primary), func(i, j int) { primary[i], primary[j] = primary[j], primary[i] })
		plan.Looks = verdict.SplitLooks(primary, t.Looks)
		for _, l := range plan.Looks {
			sort.Strings(l)
		}
		sizes := make([]int, len(plan.Looks))
		lookOf := map[string]int{}
		for i, l := range plan.Looks {
			sizes[i] = len(l)
			for _, task := range l {
				lookOf[task] = i + 1
			}
			if len(l) < minTasksPerLook {
				logger.Warn("a look with fewer than 6 tasks can never decide on its own; the sequential rule will rarely stop early", "experiment", e.ID, "look", i+1, "tasks", len(l))
			}
		}
		var err error
		if plan.LookAlphas, err = verdict.LookAlphas(sizes, alpha, t.AlphaSplit, t.Alphas); err != nil {
			return nil, err
		}
		plan.Futility = t.Futility
		if plan.Futility == "" {
			plan.Futility = schemas.FutilityCurtail
		}
		if plan.Futility == schemas.FutilityCurtail && !verdict.RateMetric(metric) {
			logger.Warn("futility curtailment needs a metric in [0, 1]; it is off for this experiment", "experiment", e.ID, "metric", metric)
			plan.Futility = schemas.FutilityOff
		}
		optional = func(sl schemas.Slot) bool { return lookOf[sl.Task] > 1 }
	}
	for i := range m.Slots {
		sl := &m.Slots[i]
		if !sl.Counted {
			continue
		}
		if optional(*sl) {
			sl.Optional = true
			plan.Optional++
		} else {
			plan.Required++
		}
	}
	return plan, nil
}
