// Package modelswitch writes the model switch report of an evaluation that
// ran one agent on several models (mode model_switch, or any evaluation
// with two or more arms of the same agent): quality, cost and latency per
// model with confidence ranges, the cost and latency Pareto frontiers,
// each model against the best one by the exact paired sign-flip test with
// Holm adjustment, the same per benchmark and per segment (task kind,
// capability, tag, difficulty and the tasks' own metadata), and a plain
// recommendation that is never stronger than the statistics.
//
// From the report it then writes a Bifrost routing config: one routing
// rule per segment whose best model differs from the default, a default
// rule with weighted load balancing over the models that cannot be told
// apart from the best, a fallback chain ordered by measured quality and
// availability, and a budget saver rule. The config is a valid Bifrost
// config.json fragment (transports/config.schema.json, governance
// routing_rules) and the same rules can be applied to the org's own
// Bifrost through its routing rules API (apply.go).
//
// Every number comes from the sealed runs of the evaluation's child
// experiments, read through a leaderboard.Source; a number that no run
// measured is unknown with the reason, never zero.
package modelswitch

import (
	"errors"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/schemas"
)

// MaxModels is the most models one model switch evaluation compares.
const MaxModels = 6

// MinSegmentTasks is the fewest tasks a segment value needs before it is
// compared and can get a routing rule.
const MinSegmentTasks = 3

// DefaultAlpha is the significance level of every comparison.
const DefaultAlpha = 0.05

// Comparison results.
const (
	ResultBest       = "best"        // the best model itself
	ResultWorse      = "worse"       // significantly worse than the best after Holm
	ResultCannotTell = "cannot_tell" // cannot be told apart from the best at this size
	ResultNoData     = "no_data"     // no task both models ran
)

// Recommendation actions.
const (
	ActionKeep        = "keep"                // keep the current model
	ActionSwitch      = "switch"              // switch to another model
	ActionNotEnough   = "not_enough_evidence" // too few runs to say
	ActionStillMoving = "still_running"       // the evaluation has not finished
)

// ErrNotModelSwitch marks an evaluation that cannot have a model switch
// report: fewer than two arms, or arms of different agents.
var ErrNotModelSwitch = errors.New("not a model comparison")

// Report is GET /evaluations/{id}/model-switch.
type Report struct {
	Evaluation string `json:"evaluation"`
	Status     string `json:"status"`
	Agent      string `json:"agent"`
	Version    int    `json:"version,omitempty"`
	// Current is the label of the model the agent uses now: the first arm.
	Current string  `json:"current"`
	Alpha   float64 `json:"alpha"`
	// Best is the label of the model with the highest measured quality.
	Best   string  `json:"best"`
	Models []Model `json:"models"` // in rank order: quality, then cost
	// Comparisons hold every other model against the best, Holm-adjusted
	// together.
	Comparisons []Comparison `json:"comparisons"`
	// Pareto is quality against cost per verified success; LatencyPareto
	// quality against the median model call latency.
	Pareto        costexplain.Pareto `json:"pareto"`
	LatencyPareto LatencyPareto      `json:"latency_pareto"`
	Benchmarks    []Group            `json:"benchmarks"`
	Segments      []Group            `json:"segments"`
	// Recommendation is the plain answer.
	Recommendation Recommendation `json:"recommendation"`
	// Missing lists what no run measured: models without runs, benchmarks
	// that were skipped or have no graded run.
	Missing []string `json:"missing"`
	Notes   []string `json:"notes"`
	// PracticeRuns are runs left out because a scripted practice model
	// answered them: they are not findings.
	PracticeRuns int `json:"practice_runs,omitempty"`
}

// Model is one arm: the agent on one model.
type Model struct {
	Label    string `json:"label"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
	// Open marks a model served by the org's own vLLM or SGLang endpoint.
	Open  bool `json:"open,omitempty"`
	Runs  int  `json:"runs"`
	Tasks int  `json:"tasks"`
	// Quality is the verified success rate over the clean runs of every
	// benchmark, with its 95% task-clustered range as bounds.
	Quality        schemas.Value `json:"quality"`
	CostPerRun     schemas.Value `json:"cost_per_run"`     // mean over runs
	CostPerSuccess schemas.Value `json:"cost_per_success"` // total over verified successes
	CostTotal      schemas.Value `json:"cost_total"`
	LatencyP50MS   schemas.Value `json:"latency_p50_ms"` // model call latency from the gateway rows
	LatencyP95MS   schemas.Value `json:"latency_p95_ms"`
	WallP50S       schemas.Value `json:"wall_p50_s"` // median run wall time
	// Availability is the share of model calls the provider answered: the
	// rows that failed with a 5xx, 429 or no status count against it; a
	// 4xx the agent caused does not.
	Availability schemas.Value `json:"availability"`
	ModelCalls   int           `json:"model_calls"`
	CallFailures int           `json:"call_failures"`
	Fallbacks    int           `json:"fallbacks"` // calls a gateway fallback served
	// ServedAsAsked is the share of model calls whose served model is known
	// that were the model asked for; ServedUnknown counts the calls whose
	// served model the gateway rows do not show, and ServedModels the
	// distinct served ids (W4 feature 8).
	ServedAsAsked schemas.Value  `json:"served_as_asked"`
	ServedUnknown int            `json:"served_unknown,omitempty"`
	ServedModels  map[string]int `json:"served_models,omitempty"`
	// Excluded are clean runs left out of the score, by reason.
	Excluded map[string]int `json:"excluded,omitempty"`
	// Frontier says the model is on the cost Pareto frontier; LatencyFrontier
	// the same on the latency plane.
	Frontier        bool `json:"frontier"`
	LatencyFrontier bool `json:"latency_frontier"`
}

// Comparison is one model against the best on the tasks both ran.
type Comparison struct {
	Label     string   `json:"label"`
	Against   string   `json:"against"`
	Tasks     int      `json:"tasks"`
	MeanDiff  float64  `json:"mean_diff"` // this model minus the best, per-task success rate
	DiffLow   *float64 `json:"diff_low"`  // 95% range of MeanDiff; nil with fewer than two tasks
	DiffHigh  *float64 `json:"diff_high"`
	P         *float64 `json:"p"`
	PAdjusted *float64 `json:"p_adjusted"`
	Result    string   `json:"result"`
	Reason    string   `json:"reason"`
}

// Group is one benchmark or one segment value: every model's numbers on
// its tasks and the best model there against the report's default model.
type Group struct {
	Key   string `json:"key"` // benchmark, task_kind, capability, tag, difficulty or a metadata key
	Value string `json:"value"`
	Tasks int    `json:"tasks"`
	// Models are keyed by label.
	Models map[string]GroupModel `json:"models"`
	Best   string                `json:"best"` // label; empty without runs
	// Default is the report's recommended model; VsDefault compares the
	// group's best with it on the group's tasks (Holm across all groups of
	// the section). Empty when the best is the default.
	Default   string      `json:"default"`
	VsDefault *Comparison `json:"vs_default,omitempty"`
	// Small marks a segment with fewer than MinSegmentTasks tasks: shown,
	// never compared.
	Small bool   `json:"small,omitempty"`
	Note  string `json:"note,omitempty"`
}

// GroupModel is one model's numbers in a group.
type GroupModel struct {
	Runs       int           `json:"runs"`
	Tasks      int           `json:"tasks"`
	Quality    schemas.Value `json:"quality"`
	CostPerRun schemas.Value `json:"cost_per_run"`
	LatencyP50 schemas.Value `json:"latency_p50_ms"`
}

// LatencyPareto is quality against median model call latency.
type LatencyPareto struct {
	Points   []LatencyPoint `json:"points"`
	Frontier []string       `json:"frontier"` // labels, fastest first
	Note     string         `json:"note"`
}

// LatencyPoint is one model on the latency plane.
type LatencyPoint struct {
	Label     string        `json:"label"`
	Model     string        `json:"model"`
	LatencyMS schemas.Value `json:"latency_ms"`
	Quality   schemas.Value `json:"quality"`
	Placed    bool          `json:"placed"`
	Frontier  bool          `json:"frontier"`
	Reason    string        `json:"reason,omitempty"`
}

// Recommendation is the plain answer.
type Recommendation struct {
	Action string `json:"action"` // keep | switch | not_enough_evidence | still_running
	// Model is the label of the model to use by default.
	Model string `json:"model"`
	// Strength is "strong" when the statistics separate the choice from
	// the alternatives, "weak" when they only fail to tell them apart.
	Strength string   `json:"strength"`
	Summary  string   `json:"summary"`
	Reasons  []string `json:"reasons"`
	Caveats  []string `json:"caveats"`
	// Tied are the labels that cannot be told apart from the best, the
	// best first: the default routing rule balances over them.
	Tied []string `json:"tied"`
}
