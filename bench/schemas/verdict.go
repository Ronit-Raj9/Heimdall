package schemas

import (
	"context"
	"io"
	"time"
)

// Decision is the verdict on the primary endpoint.
type Decision string

// Decisions.
const (
	DecisionImproved     Decision = "improved"
	DecisionRegressed    Decision = "regressed"
	DecisionInconclusive Decision = "inconclusive"
	DecisionInvalid      Decision = "invalid"
)

// Action is the single action a verdict ends in (DESIGN.md 9.4).
type Action string

// Actions.
const (
	ActionShip       Action = "ship"
	ActionHold       Action = "hold"
	ActionNoDecision Action = "no_decision"
	ActionFixHarness Action = "fix_harness"
)

// GuardStatus is one guard's state.
type GuardStatus string

// Guard statuses. An undetermined guard blocks IMPROVED.
const (
	GuardPass         GuardStatus = "pass"
	GuardFail         GuardStatus = "fail"
	GuardUndetermined GuardStatus = "undetermined"
)

// IntegrityStatus sits beside the verdict. A regression never shows green.
type IntegrityStatus string

// Integrity statuses.
const (
	IntegrityClean     IntegrityStatus = "clean"
	IntegrityAlert     IntegrityStatus = "alert"
	IntegrityRegressed IntegrityStatus = "regressed"
	IntegrityUnknown   IntegrityStatus = "unknown" // no gate data, e.g. an audit of published counts
)

// VerdictPolicy turns sealed runs into an analysis.
type VerdictPolicy interface {
	Part
	Decide(ctx context.Context, m *Manifest, runs []SealedRun) (*Analysis, error)
}

// GradeSourceOriginal names the grade written when the run was sealed.
const GradeSourceOriginal = "original"

// SealedRun is a sealed run as analysis reads it, with the grade source chosen
// (original or a regrade).
type SealedRun struct {
	Slot        Slot             `json:"slot"`
	Run         Run              `json:"run"`
	Dir         string           `json:"dir"`
	SealHash    string           `json:"seal_hash"`
	Grade       *Grade           `json:"grade,omitempty"`
	GradeSource string           `json:"grade_source"` // "original" or a regrade id
	Metrics     map[string]Value `json:"metrics"`
	Gates       []GateResult     `json:"gates"`
	Receipts    []Receipt        `json:"receipts,omitempty"`
	Gateway     *GatewayTally    `json:"gateway,omitempty"`  // from evidence.json; nil when the run has no gateway log
	Problems    []string         `json:"problems,omitempty"` // evidence problems found while loading (seal does not verify, slot has more than one counted run); any makes the verdict INVALID
	Review      *Review          `json:"review,omitempty"`   // the human review whose decision this run's outcome came from, under grade source reviewed only
}

// Effect summarizes paired per-task differences for one condition and metric.
type Effect struct {
	Condition   string      `json:"condition"`
	Metric      string      `json:"metric"`
	ArmA        string      `json:"arm_a"`
	ArmB        string      `json:"arm_b"`
	NTasks      int         `json:"n_tasks"`
	NNonzero    int         `json:"n_nonzero"`
	NPositive   int         `json:"n_positive"`
	NNegative   int         `json:"n_negative"`
	DBar        float64     `json:"d_bar"`
	PValue      *float64    `json:"p_value,omitempty"`  // exact sign-flip, two-sided; nil when descriptive
	Interval    *[2]float64 `json:"interval,omitempty"` // paired t, descriptive only
	PerTask     []TaskDiff  `json:"per_task"`
	LowerBound  *Effect     `json:"lower_bound,omitempty"` // unknowns set against B
	UpperBound  *Effect     `json:"upper_bound,omitempty"` // unknowns set in favour of B
	Descriptive bool        `json:"descriptive"`
}

// TaskDiff is one task's paired difference.
type TaskDiff struct {
	Task     string  `json:"task"`
	MeanA    float64 `json:"mean_a"`
	MeanB    float64 `json:"mean_b"`
	D        float64 `json:"d"`
	NA       int     `json:"n_a"`
	NB       int     `json:"n_b"`
	UnknownA int     `json:"unknown_a"`
	UnknownB int     `json:"unknown_b"`
}

// GuardResult is one guard's evaluation.
type GuardResult struct {
	Spec   GuardSpec   `json:"spec"`
	Status GuardStatus `json:"status"`
	Effect *Effect     `json:"effect,omitempty"`
	Detail string      `json:"detail,omitempty"`
}

// Verdict is the release decision on the primary endpoint.
type Verdict struct {
	Primary           Endpoint          `json:"primary"`
	Decision          Decision          `json:"decision"`
	Action            Action            `json:"action"`
	Effect            *Effect           `json:"effect,omitempty"`
	Alpha             float64           `json:"alpha"`
	MDE               float64           `json:"mde"` // percentage points
	MDEAssumedSD      bool              `json:"mde_assumed_sd"`
	SD                float64           `json:"sd"`
	TasksNeeded       int               `json:"tasks_needed,omitempty"`      // for a new experiment to detect |d_bar|
	TasksNeededOver   int               `json:"tasks_needed_over,omitempty"` // set when even this many tasks would not be enough
	RandomizationUnit string            `json:"randomization_unit"`          // "task"
	TieRule           string            `json:"tie_rule"`
	Guards            []GuardResult     `json:"guards"`
	InvalidReasons    []string          `json:"invalid_reasons,omitempty"`
	Instruments       []InstrumentCheck `json:"instruments,omitempty"` // measured instrument checks; a failed one is also an invalid reason
	Integrity         IntegrityStatus   `json:"integrity"`
	ManifestHash      string            `json:"manifest_hash"`
	FrozenAt          time.Time         `json:"frozen_at"`
}

// InstrumentStatus is one instrument check's state.
type InstrumentStatus string

// Instrument statuses. A failed check makes the experiment INVALID. An
// unknown one is never read as a pass: an IMPROVED verdict holds. A warning
// (a parity check) is shown and changes nothing unless the experiment
// declares the matching guard.
const (
	InstrumentPass          InstrumentStatus = "pass"
	InstrumentFail          InstrumentStatus = "fail"
	InstrumentUnknown       InstrumentStatus = "unknown"
	InstrumentWarn          InstrumentStatus = "warn"
	InstrumentNotApplicable InstrumentStatus = "not_applicable"
)

// InstrumentCheck is one check that the measurement itself worked, with the
// numbers behind it, such as usage completeness or the gateway's dropped log
// rows.
type InstrumentCheck struct {
	Name      string           `json:"name"`
	Status    InstrumentStatus `json:"status"`
	Threshold string           `json:"threshold"`
	PerArm    map[string]Ratio `json:"per_arm,omitempty"` // arm -> count over denominator, when the check is per arm
	Detail    string           `json:"detail"`
}

// DiagnosticsRow is one condition's behaviour per arm. Rows come from the
// catalog, so a new probe adds a row with no UI change.
type DiagnosticsRow struct {
	Condition   string                           `json:"condition"`
	Axis        string                           `json:"axis"`
	NTasks      int                              `json:"n_tasks"`
	PerArm      map[string]Ratio                 `json:"per_arm"` // arm -> successes over runs
	Effect      *Effect                          `json:"effect,omitempty"`
	Descriptive bool                             `json:"descriptive"`
	Receipts    map[string]map[ReceiptStep]Ratio `json:"receipts,omitempty"` // arm -> step -> yes over applicable
}

// Ratio is a count over a denominator with the unknowns kept apart.
type Ratio struct {
	Num     int `json:"num"`
	Den     int `json:"den"`
	Unknown int `json:"unknown"`
}

// Meter is an always-on aggregate (honesty, safety, efficiency).
type Meter struct {
	Name        string           `json:"name"`
	Label       string           `json:"label"`
	Kind        ValueKind        `json:"kind"`
	Numerator   string           `json:"numerator"`   // definition, e.g. "claims among failures"
	Denominator string           `json:"denominator"` // definition
	PerArm      map[string]Value `json:"per_arm"`
}

// Analysis is a derived, recomputable result over sealed runs. It is written
// to analysis/<analysis_id>/ and never into a sealed run folder.
type Analysis struct {
	AnalysisID  string                      `json:"analysis_id"`
	ExpID       string                      `json:"exp_id"`
	CreatedAt   time.Time                   `json:"created_at"`
	GradeSource string                      `json:"grade_source"` // "original" or a regrade id
	Mode        Mode                        `json:"mode"`
	Verdict     Verdict                     `json:"verdict"`
	Naive       map[string]Ratio            `json:"naive"` // arm -> raw passes over runs, the naive scoreboard
	Diagnostics []DiagnosticsRow            `json:"diagnostics"`
	Meters      []Meter                     `json:"meters"`
	ArmMetrics  map[string]map[string]Value `json:"arm_metrics"` // arm -> metric -> median per run
	Policy      *PolicyResult               `json:"policy,omitempty"`

	// ReviewedRuns counts counted runs whose outcome came from a human review
	// (grade source reviewed), and ReviewChangedRuns those of them whose
	// outcome differs from the sealed one. Both are zero, and absent, under
	// any other grade source.
	ReviewedRuns      int `json:"reviewed_runs,omitempty"`
	ReviewChangedRuns int `json:"review_changed_runs,omitempty"`
}

// Reporter writes an analysis in one format.
type Reporter interface {
	Part
	Write(ctx context.Context, a *Analysis, m *Manifest, w io.Writer) error
}
