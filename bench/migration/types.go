// Package migration writes the model migration report
// (docs/model-migration.md): for one agent moved from one model to one or
// more others on a model matrix experiment, a verdict per pair overall and
// per stratum (condition, task kind, capability, tag, declared and observed
// difficulty) by the exact paired sign-flip test with Holm adjustment, cost
// per verified success with the cost explanation, behaviour differences
// (failure clusters only one model shows, gate firings, false completion,
// rejected requests, refusals, retries and fallbacks, each fault
// condition), the tasks that flipped with their run pairs, and a plain
// recommendation that follows the release policy and is never stronger than
// the statistics.
//
// The package is pure compute over the verified sealed runs of one
// experiment and the files of their folders. It writes nothing; the CLI
// writes the markdown and JSON, the API serves the JSON.
package migration

import (
	"errors"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Recommendation actions.
const (
	ActionMigrate      = "migrate"
	ActionDoNotMigrate = "do_not_migrate"
	ActionNotEnough    = "not_enough_evidence"
)

// Verdict results.
const (
	ResultBetter     = "better"      // the new model succeeds more, significantly after Holm
	ResultWorse      = "worse"       // the new model succeeds less, significantly after Holm
	ResultCannotTell = "cannot_tell" // no significant difference at this size
)

// Stratum dimensions.
const (
	DimCondition          = "condition"
	DimTaskKind           = "task_kind"
	DimCapability         = "capability"
	DimTag                = "tag"
	DimDifficultyDeclared = "difficulty_declared"
	DimDifficultyObserved = "difficulty_observed"
)

// Power is the power the tasks-needed estimate assumes.
const Power = 0.8

// TunedPromptNote is in every report: a migration measured with the old
// model's prompt understates what a prompt tuned for the new model gets.
const TunedPromptNote = "The same prompt and agent config were used for every model: a prompt tuned for the old model is not a tuned prompt for the new one, so this measures the move as it is, not the best the new model can do."

// ErrBadQuery marks a query that cannot be answered as asked.
var ErrBadQuery = errors.New("bad migration query")

// Query selects the models to compare. From and each To are a model name
// (an arm whose config requests it) or an arm id. Empty To compares From
// with every other arm of the same agent. Condition is the condition of the
// overall verdict, the experiment's primary condition when empty.
type Query struct {
	From      string   `json:"from"`
	To        []string `json:"to"`
	Condition string   `json:"condition"`
	Alpha     float64  `json:"alpha"`
}

// Report is the migration report (GET /api/bench/experiments/{id}/migration).
type Report struct {
	ExpID      string              `json:"exp_id"`
	Suite      string              `json:"suite"`
	Condition  string              `json:"condition"`  // the overall verdict's condition
	Conditions []string            `json:"conditions"` // conditions with a graded run
	Alpha      float64             `json:"alpha"`
	Synthetic  bool                `json:"synthetic"`
	Policy     string              `json:"policy"` // release policy name the recommendation follows, empty without one
	From       ArmRef              `json:"from"`
	Pairs      []Pair              `json:"pairs"`
	Models     []ModelSummary      `json:"models"` // every arm of the report, from first
	Pareto     *costexplain.Pareto `json:"pareto"`
	Notes      []string            `json:"notes"`
}

// ArmRef names one arm.
type ArmRef struct {
	Arm      string `json:"arm"`
	Label    string `json:"label"`
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

// Pair is the old model against one new model.
type Pair struct {
	From           ArmRef                   `json:"from"`
	To             ArmRef                   `json:"to"`
	Overall        Verdict                  `json:"overall"`
	Strata         []Verdict                `json:"strata"` // Holm-adjusted together
	Recommendation Recommendation           `json:"recommendation"`
	Behaviour      Behaviour                `json:"behaviour"`
	Flipped        []FlippedTask            `json:"flipped"`
	Cost           *costexplain.Explanation `json:"cost"` // why the new model cost more or less, on the overall condition
}

// Verdict is one paired comparison: the per-task success rate of the new
// model minus the old one's, over the tasks both ran, tested by the exact
// two-sided sign-flip test and Holm-adjusted across Family tests.
type Verdict struct {
	Dimension   string   `json:"dimension"` // empty for the overall verdict
	Value       string   `json:"value"`
	Condition   string   `json:"condition"`
	Tasks       int      `json:"tasks"`
	Better      int      `json:"better"` // tasks where the new model's rate is higher
	Worse       int      `json:"worse"`
	MeanDiff    float64  `json:"mean_diff"` // new minus old, mean over tasks
	P           *float64 `json:"p"`
	PAdjusted   *float64 `json:"p_adjusted"`
	Family      int      `json:"family"` // tests in the Holm family
	Result      string   `json:"result"` // better | worse | cannot_tell
	Reason      string   `json:"reason"`
	MinTasks    int      `json:"min_tasks"`    // fewest tasks with which the test could reach alpha after Holm at all
	TasksNeeded int      `json:"tasks_needed"` // tasks to detect the observed difference with 80% power; 0 when it cannot be estimated

	sd float64 // spread of the per-task differences, for TasksNeeded
}

// Recommendation is the plain answer.
type Recommendation struct {
	Action  string                `json:"action"` // migrate | do_not_migrate | not_enough_evidence
	Reason  string                `json:"reason"`
	Policy  *schemas.PolicyResult `json:"policy,omitempty"`
	Caveats []string              `json:"caveats"`
}

// Behaviour is what changed besides success.
type Behaviour struct {
	NewClusters     []ClusterRef    `json:"new_clusters"`   // failure clusters only the new model shows
	FixedClusters   []ClusterRef    `json:"fixed_clusters"` // failure clusters only the old model shows
	Gates           []GateChange    `json:"gates"`          // gates that fired in either arm
	FalseCompletion Change          `json:"false_completion"`
	Rejected        Change          `json:"rejected_per_run"` // provider 4xx: malformed request or tool call
	Refusals        Change          `json:"refusals_per_run"` // refusal responses and refusal outcomes
	Retries         Change          `json:"retries_per_run"`
	Fallbacks       Change          `json:"fallbacks_per_run"`
	LatencyMS       Change          `json:"latency_ms_per_turn"`
	Conditions      []ConditionDiff `json:"conditions"` // success and behaviour under each condition
}

// Change is one measure for the old and the new model.
type Change struct {
	From schemas.Value `json:"from"`
	To   schemas.Value `json:"to"`
}

// ClusterRef is one failure cluster.
type ClusterRef struct {
	Signature string   `json:"signature"`
	Title     string   `json:"title"`
	Count     int      `json:"count"`
	Tasks     []string `json:"tasks"`
	Examples  []string `json:"examples"`
}

// GateChange is one gate's firings in each arm.
type GateChange struct {
	Gate   string `json:"gate"`
	Effect string `json:"effect"`
	From   int    `json:"from"`   // runs it fired in
	To     int    `json:"to"`     // runs it fired in
	Status string `json:"status"` // new | fixed | both
}

// ConditionDiff is the pair under one condition.
type ConditionDiff struct {
	Condition       string        `json:"condition"`
	FromSuccess     schemas.Value `json:"from_success"` // mean over tasks of the per-task rate
	ToSuccess       schemas.Value `json:"to_success"`
	FromRuns        int           `json:"from_runs"`
	ToRuns          int           `json:"to_runs"`
	FalseCompletion Change        `json:"false_completion"`
	Retries         Change        `json:"retry_amplification"`
}

// FlippedTask is a task whose success rate differs between the models,
// with the runs of both sides.
type FlippedTask struct {
	Task      string   `json:"task"`
	Condition string   `json:"condition"`
	Kind      string   `json:"kind"` // regressed | fixed
	FromRate  float64  `json:"from_rate"`
	ToRate    float64  `json:"to_rate"`
	Runs      []RunRef `json:"runs"`
}

// RunRef is one run of a flipped task; PairID opens the run pair page.
type RunRef struct {
	RunID   string `json:"run_id"`
	PairID  string `json:"pair_id"`
	Arm     string `json:"arm"`
	Trial   int    `json:"trial"`
	Outcome string `json:"outcome"`
}

// ModelSummary is one arm's numbers on the overall condition.
type ModelSummary struct {
	ArmRef
	Success        schemas.Value `json:"success"`     // with its interval
	Reliability    schemas.Value `json:"reliability"` // pass^k
	K              int           `json:"k"`
	CostPerSuccess schemas.Value `json:"cost_per_success"`
	CostPerRun     schemas.Value `json:"cost_per_run"` // median
	Tokens         schemas.Value `json:"tokens"`       // median per run
	WallTimeS      schemas.Value `json:"wall_time_s"`  // median per run
	Steps          schemas.Value `json:"steps"`        // median per run
	LatencyMS      schemas.Value `json:"latency_ms_per_turn"`
	Pareto         string        `json:"pareto"` // frontier | dominated by ... | not placed: ...
}
