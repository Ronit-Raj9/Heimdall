package schemas

import "time"

// Evaluations (agent-first benchmarking, contract section 3): one agent, or
// two arms for an A/B, across many benchmarks at once. Each benchmark becomes
// one normal experiment (a child); the evaluation only tracks them.

// Evaluation modes.
const (
	EvaluationModeScore    = "score"    // one arm, a score per benchmark
	EvaluationModeVersions = "versions" // two versions of one agent
	EvaluationModeModels   = "models"   // one agent on two models
	EvaluationModeOpponent = "opponent" // the agent against another agent
)

// Evaluation states.
const (
	EvaluationQueued    = "queued"
	EvaluationRunning   = "running"
	EvaluationDone      = "done"
	EvaluationFailed    = "failed"
	EvaluationCancelled = "cancelled"
)

// EvaluationChildSkipped is the status of a benchmark that never got an
// experiment (incompatible agent, no admitted task, refused plan).
const EvaluationChildSkipped = "skipped"

// EvaluationArm is one agent (at a version) on one model.
type EvaluationArm struct {
	Label    string `json:"label"`
	Agent    string `json:"agent"`
	Version  int    `json:"version,omitempty"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// EvaluationSpec is the body of POST /api/bench/evaluations.
type EvaluationSpec struct {
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`       // score | versions | models | opponent
	Arms              []EvaluationArm `json:"arms"`       // 1 arm for score, 2 for A/B
	Benchmarks        []string        `json:"benchmarks"` // suite ids; or Pack
	Pack              string          `json:"pack,omitempty"`
	TasksPerBenchmark int             `json:"tasks_per_benchmark"` // default 5, admitted tasks only
	Trials            int             `json:"trials"`              // repeats per task, default 2
	Faults            []string        `json:"faults"`              // "clean" always; plus model-down, broken-tool, misleading-instruction
	BudgetUSD         float64         `json:"budget_usd,omitempty"`
}

// EvaluationChild is one benchmark of an evaluation and its experiment.
type EvaluationChild struct {
	Benchmark    string `json:"benchmark"`
	ExperimentID string `json:"experiment_id"`
	Status       string `json:"status"`
	Skipped      string `json:"skipped,omitempty"`
}

// Evaluation is stored as .heimdall/evaluations/<id>.json in the project.
type Evaluation struct {
	ID        string            `json:"id"`
	Org       string            `json:"org,omitempty"`
	Spec      EvaluationSpec    `json:"spec"`
	Status    string            `json:"status"` // queued|running|done|failed|cancelled
	Children  []EvaluationChild `json:"children"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by,omitempty"`
}
