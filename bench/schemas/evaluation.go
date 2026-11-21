package schemas

import "time"

// Evaluation types from the agent-first contract (audit/AGENTFIRST_CONTRACT.md,
// section 3). An Evaluation runs one agent (or two arms for an A/B) over many
// benchmarks at once; each benchmark becomes one normal experiment (a child).

// EvaluationArm is one side of an evaluation: an agent at a version with a model.
type EvaluationArm struct {
	Label    string `json:"label"`
	Agent    string `json:"agent"`
	Version  int    `json:"version,omitempty"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// EvaluationSpec is what a user asks for.
type EvaluationSpec struct {
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`       // "score" | "versions" | "models" | "opponent"
	Arms              []EvaluationArm `json:"arms"`       // 1 arm for score, 2 for A/B
	Benchmarks        []string        `json:"benchmarks"` // suite ids; or Pack
	Pack              string          `json:"pack,omitempty"`
	TasksPerBenchmark int             `json:"tasks_per_benchmark"` // default 5, admitted tasks only
	Trials            int             `json:"trials"`              // repeats per task, default 2
	Faults            []string        `json:"faults"`              // "clean" always; plus "model-down","broken-tool","misleading-instruction"
	BudgetUSD         float64         `json:"budget_usd,omitempty"`
}

// EvaluationChild is the experiment one benchmark became.
type EvaluationChild struct {
	Benchmark    string `json:"benchmark"`
	ExperimentID string `json:"experiment_id"`
	Status       string `json:"status"`
	Skipped      string `json:"skipped,omitempty"`
}

// Evaluation is the stored object (.heimdall/evaluations/<id>.json).
type Evaluation struct {
	ID        string            `json:"id"`
	Org       string            `json:"org,omitempty"`
	Spec      EvaluationSpec    `json:"spec"`
	Status    string            `json:"status"` // queued|running|done|failed|cancelled
	Children  []EvaluationChild `json:"children"`
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by,omitempty"`
}

// Evaluation modes.
const (
	EvalModeScore    = "score"
	EvalModeVersions = "versions"
	EvalModeModels   = "models"
	EvalModeOpponent = "opponent"
)

// Evaluation faults. "clean" is always run.
const (
	EvalFaultClean                 = "clean"
	EvalFaultModelDown             = "model-down"
	EvalFaultBrokenTool            = "broken-tool"
	EvalFaultMisleadingInstruction = "misleading-instruction"
)
