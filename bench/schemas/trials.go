package schemas

import (
	"encoding/json"
	"math"
)

// W7 adaptive trials and suite tiers (docs/adaptive-trials.md,
// docs/suite-tiers.md). Every type here is optional on the experiment, the
// suite, the manifest and the verdict, so a manifest without them hashes as
// it always did.

// Trials modes and adaptive rules.
const (
	TrialsFixed    = "fixed"
	TrialsAdaptive = "adaptive"

	// AdaptiveTask runs Min trials of every task and more, up to Max, only on
	// tasks whose arms disagree or whose trials are mixed within an arm.
	AdaptiveTask = "task"
	// AdaptiveSequential runs the tasks in a fixed number of looks and stops
	// early for a clear result or when no later look could decide.
	AdaptiveSequential = "sequential"

	// FutilityCurtail stops a sequential experiment only when no completion
	// of the remaining looks could reach a directional verdict (exact).
	FutilityCurtail = "curtail"
	// FutilityNonBinding also stops when a look shows no trend at all: the
	// sign-flip p at both missingness bounds is at least NonBindingP. Any
	// futility stop only removes chances to reject, so it never raises the
	// false-alarm rate; this one costs some power (docs/adaptive-trials.md).
	FutilityNonBinding = "nonbinding"
	// FutilityOff never stops for futility.
	FutilityOff = "off"

	// NonBindingP is the no-trend threshold of FutilityNonBinding.
	NonBindingP = 0.5

	// AlphaSplitEqual gives every look alpha/looks; AlphaSplitOBF gives each
	// look the increment of the Lan-DeMets O'Brien-Fleming spending function,
	// used as a Bonferroni split (the increments sum to alpha).
	AlphaSplitEqual = "equal"
	AlphaSplitOBF   = "obf"

	// ExtensionInverseNormal combines the first experiment's one-sided
	// sign-flip p-value with the extension's own, weighted by pre-declared
	// weights (docs/adaptive-trials.md, "Buy more evidence").
	ExtensionInverseNormal = "inverse_normal"
	// ExtensionExploratory marks an extension whose parent declared no
	// combination rule: the pooled analysis is exploratory and decides nothing.
	ExtensionExploratory = "exploratory"
)

// TrialsPolicy is the experiment's pre-registered trials design. Nil, or mode
// fixed, is the classic design: every slot runs.
type TrialsPolicy struct {
	Mode string `json:"mode" yaml:"mode"`                     // fixed (default) | adaptive
	Rule string `json:"rule,omitempty" yaml:"rule,omitempty"` // adaptive only: task | sequential

	// Rule task: Min trials of every task always run; Max is the planned
	// maximum (every adaptive condition's trials must equal it; 0 takes the
	// condition's trials). Conditions lists the conditions the rule applies
	// to; empty means the decision conditions (primary and no_regression
	// guards). Other conditions run every planned trial.
	Min        int      `json:"min,omitempty" yaml:"min,omitempty"`
	Max        int      `json:"max,omitempty" yaml:"max,omitempty"`
	Conditions []string `json:"conditions,omitempty" yaml:"conditions,omitempty"`

	// Rule sequential: the primary condition's tasks are split into Looks
	// groups drawn from the seed; after each look the primary effect is
	// tested at that look's alpha. AlphaSplit is equal | obf (default obf) or
	// Alphas lists one alpha per look explicitly (sum at most verdict.alpha).
	Looks      int       `json:"looks,omitempty" yaml:"looks,omitempty"`
	AlphaSplit string    `json:"alpha_split,omitempty" yaml:"alpha_split,omitempty"`
	Alphas     []float64 `json:"alphas,omitempty" yaml:"alphas,omitempty"`
	Futility   string    `json:"futility,omitempty" yaml:"futility,omitempty"` // curtail (default) | nonbinding | off

	// Extension pre-declares how a later `extend` follow-up is analysed with
	// this experiment. With it, this experiment's own verdict decides at
	// AlphaFirst and the rest of alpha is kept for the combined analysis.
	Extension *ExtensionRule `json:"extension,omitempty" yaml:"extension,omitempty"`
}

// ExtensionRule is a pre-declared combination rule for one follow-up.
type ExtensionRule struct {
	Rule        string  `json:"rule" yaml:"rule"`                                     // inverse_normal
	AlphaFirst  float64 `json:"alpha_first,omitempty" yaml:"alpha_first,omitempty"`   // default alpha/2
	WeightFirst float64 `json:"weight_first,omitempty" yaml:"weight_first,omitempty"` // squared weight of the first stage, default 0.5
}

// ExtendsRef links a follow-up experiment to the experiment it extends.
type ExtendsRef struct {
	ExpID        string `json:"exp_id" yaml:"exp_id"`
	ManifestHash string `json:"manifest_hash" yaml:"manifest_hash"`
	Kind         string `json:"kind" yaml:"kind"` // tasks | trials
	Rule         string `json:"rule" yaml:"rule"` // inverse_normal | exploratory, copied from the parent's pre-declared rule
}

// AdaptivePlan is the frozen adaptive design in the manifest: the maximum
// design is planned and hashed up front, and Optional counts the slots that
// run only when the rule releases them.
type AdaptivePlan struct {
	Rule       string     `json:"rule"`
	Conditions []string   `json:"conditions"`
	Min        int        `json:"min,omitempty"`
	Max        int        `json:"max,omitempty"`
	Metric     string     `json:"metric"`
	Looks      [][]string `json:"looks,omitempty"`       // sequential: task ids per look
	LookAlphas []float64  `json:"look_alphas,omitempty"` // sequential: alpha per look
	Alpha      float64    `json:"alpha"`
	Futility   string     `json:"futility,omitempty"`
	Optional   int        `json:"optional"`
	Required   int        `json:"required"`
}

// TierPlan is the suite tier a manifest was planned in, with the tier's task
// ids as resolved at plan time.
type TierPlan struct {
	Name  string   `json:"name"`
	Tasks []string `json:"tasks"`
}

// Slot states of an adaptive design, as the replay of the rule finds them.
const (
	SlotRequired    = "required"     // always planned to run
	SlotReleased    = "released"     // optional, released by the rule
	SlotNotRequired = "not_required" // optional, never released: not missing
)

// AdaptiveReport is what the verdict and the UI show about an adaptive
// design: trials used against the maximum, runs saved, and why it stopped.
type AdaptiveReport struct {
	Rule          string              `json:"rule"`
	MaxSlots      int                 `json:"max_slots"`      // counted slots of the maximum design
	RequiredSlots int                 `json:"required_slots"` // always-run plus released optional slots
	RunSlots      int                 `json:"run_slots"`      // required slots with a counted sealed run
	RunsSaved     int                 `json:"runs_saved"`     // optional slots never released
	StopReason    string              `json:"stop_reason"`
	StopLook      int                 `json:"stop_look,omitempty"`
	Looks         int                 `json:"looks,omitempty"`
	DecisionAlpha float64             `json:"decision_alpha"` // alpha the primary decision used
	Tasks         []AdaptiveTaskState `json:"tasks,omitempty"`
	LookResults   []AdaptiveLook      `json:"look_results,omitempty"`
	Excluded      []string            `json:"excluded,omitempty"` // runs of optional slots the rule did not release; never analysed
	Notes         []string            `json:"notes,omitempty"`
	Conditions    []string            `json:"conditions,omitempty"`
	SlotStates    map[string]string   `json:"-"` // slot id -> required | released | not_required
}

// AdaptiveTaskState is one (condition, task) of the task rule.
type AdaptiveTaskState struct {
	Condition string `json:"condition"`
	Task      string `json:"task"`
	Trials    int    `json:"trials"` // trials per arm the rule asked for
	Max       int    `json:"max"`
	Reason    string `json:"reason"` // concordant | max_reached | waiting
}

// AdaptiveLook is one look of the sequential rule.
type AdaptiveLook struct {
	Look      int      `json:"look"`
	Tasks     int      `json:"tasks"` // cumulative tasks analysed
	Alpha     float64  `json:"alpha"`
	PLow      *float64 `json:"p_low,omitempty"`  // sign-flip p at the lower missingness bound
	PHigh     *float64 `json:"p_high,omitempty"` // and at the upper bound
	DBar      float64  `json:"d_bar"`
	Outcome   string   `json:"outcome"` // continue | efficacy | futility | final | not_reached
	BestCaseP *float64 `json:"best_case_p,omitempty"`
}

// MoreEvidence is the verdict page's "buy more evidence" line: tasks a new
// experiment needs to detect TargetPP and what they would cost.
type MoreEvidence struct {
	TargetPP    float64  `json:"target_pp"`
	Tasks       int      `json:"tasks"`       // more tasks, on top of those already run
	TasksTotal  int      `json:"tasks_total"` // tasks the new experiment needs in all
	Over        bool     `json:"over,omitempty"` // more than TasksTotal (the sizing cap) tasks needed; Tasks, Runs and CostUSD are then not estimated
	SD          float64  `json:"sd"`
	SDAssumed   bool     `json:"sd_assumed"`
	Runs        int      `json:"runs"` // tasks x trials x arms
	CostUSD     *float64 `json:"cost_usd,omitempty"`
	CostBasis   string   `json:"cost_basis"`
	Command     string   `json:"command,omitempty"`
	Combination string   `json:"combination"` // inverse_normal | exploratory
	AlphaKept   float64  `json:"alpha_kept,omitempty"`
}

// ExtensionResult is the analysis of a follow-up together with the
// experiment it extends (`extend status`).
type ExtensionResult struct {
	Parent      string   `json:"parent"`
	Child       string   `json:"child"`
	Kind        string   `json:"kind"` // tasks | trials
	Rule        string   `json:"rule"` // inverse_normal | exploratory
	Decision    Decision `json:"decision"`
	Exploratory bool     `json:"exploratory"`
	Alpha       float64  `json:"alpha"`        // alpha of the combined test (verdict alpha minus the first stage's)
	WeightFirst float64  `json:"weight_first"` // squared weight of the first stage
	// One-sided p-values per stage in the improving direction and the
	// regressing direction, and the combined z, at both missingness bounds.
	Stages      []ExtensionStage `json:"stages"`
	ZImproveLow float64          `json:"z_improve_low"`
	ZImproveHi  float64          `json:"z_improve_high"`
	ZRegressLow float64          `json:"z_regress_low"`
	ZRegressHi  float64          `json:"z_regress_high"`
	ZCritical   float64          `json:"z_critical"`
	PooledDBar  float64          `json:"pooled_d_bar"` // descriptive only
	PooledTasks int              `json:"pooled_tasks"`
	Reasons     []string         `json:"reasons,omitempty"`
}

// extensionResultJSON is ExtensionResult on the wire: a combined z that is
// -Inf (a stage whose p in that direction is 1 blocks the direction) has no
// JSON number, so it travels as null and its field name is listed in
// z_blocked (audit 1-B-04).
type extensionResultJSON struct {
	Parent      string           `json:"parent"`
	Child       string           `json:"child"`
	Kind        string           `json:"kind"`
	Rule        string           `json:"rule"`
	Decision    Decision         `json:"decision"`
	Exploratory bool             `json:"exploratory"`
	Alpha       float64          `json:"alpha"`
	WeightFirst float64          `json:"weight_first"`
	Stages      []ExtensionStage `json:"stages"`
	ZImproveLow *float64         `json:"z_improve_low"`
	ZImproveHi  *float64         `json:"z_improve_high"`
	ZRegressLow *float64         `json:"z_regress_low"`
	ZRegressHi  *float64         `json:"z_regress_high"`
	ZBlocked    []string         `json:"z_blocked,omitempty"`
	ZCritical   float64          `json:"z_critical"`
	PooledDBar  float64          `json:"pooled_d_bar"`
	PooledTasks int              `json:"pooled_tasks"`
	Reasons     []string         `json:"reasons,omitempty"`
}

// MarshalJSON encodes a blocked (non-finite) combined z as null and names it
// in z_blocked instead of failing the whole encoding.
func (r ExtensionResult) MarshalJSON() ([]byte, error) {
	w := extensionResultJSON{
		Parent: r.Parent, Child: r.Child, Kind: r.Kind, Rule: r.Rule,
		Decision: r.Decision, Exploratory: r.Exploratory, Alpha: r.Alpha,
		WeightFirst: r.WeightFirst, Stages: r.Stages, ZCritical: r.ZCritical,
		PooledDBar: r.PooledDBar, PooledTasks: r.PooledTasks, Reasons: r.Reasons,
	}
	for _, f := range []struct {
		name string
		v    float64
		dst  **float64
	}{
		{"z_improve_low", r.ZImproveLow, &w.ZImproveLow},
		{"z_improve_high", r.ZImproveHi, &w.ZImproveHi},
		{"z_regress_low", r.ZRegressLow, &w.ZRegressLow},
		{"z_regress_high", r.ZRegressHi, &w.ZRegressHi},
	} {
		if math.IsInf(f.v, 0) || math.IsNaN(f.v) {
			w.ZBlocked = append(w.ZBlocked, f.name)
			continue
		}
		v := f.v
		*f.dst = &v
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads a null combined z back as -Inf (a blocked direction).
func (r *ExtensionResult) UnmarshalJSON(b []byte) error {
	var w extensionResultJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	z := func(p *float64) float64 {
		if p == nil {
			return math.Inf(-1)
		}
		return *p
	}
	*r = ExtensionResult{
		Parent: w.Parent, Child: w.Child, Kind: w.Kind, Rule: w.Rule,
		Decision: w.Decision, Exploratory: w.Exploratory, Alpha: w.Alpha,
		WeightFirst: w.WeightFirst, Stages: w.Stages, ZCritical: w.ZCritical,
		ZImproveLow: z(w.ZImproveLow), ZImproveHi: z(w.ZImproveHi),
		ZRegressLow: z(w.ZRegressLow), ZRegressHi: z(w.ZRegressHi),
		PooledDBar: w.PooledDBar, PooledTasks: w.PooledTasks, Reasons: w.Reasons,
	}
	return nil
}

// ExtensionStage is one stage of a combined analysis.
type ExtensionStage struct {
	ExpID       string   `json:"exp_id"`
	Decision    Decision `json:"decision"`
	Tasks       int      `json:"tasks"`
	DBar        float64  `json:"d_bar"`
	PImproveLow float64  `json:"p_improve_low"`
	PImproveHi  float64  `json:"p_improve_high"`
	PRegressLow float64  `json:"p_regress_low"`
	PRegressHi  float64  `json:"p_regress_high"`
}
