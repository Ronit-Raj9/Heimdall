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
	Label   string `json:"label"`
	Agent   string `json:"agent"`
	Version int    `json:"version,omitempty"`
	Model   string `json:"model"`
	// Provider is the gateway provider the model is served through.
	Provider string `json:"provider,omitempty"`
	// LatestAtLaunch is set by the server when the arm asked for the
	// latest version: Version is then the version that was latest when the
	// evaluation was created, recorded so the report card and the agent
	// leaderboard name the exact version that ran.
	LatestAtLaunch bool `json:"latest_at_launch,omitempty"`
}

// EvaluationSpec is the body of POST /api/bench/evaluations.
type EvaluationSpec struct {
	Name              string          `json:"name"`
	Mode              string          `json:"mode"`       // score | versions | models | opponent
	Arms              []EvaluationArm `json:"arms"`       // 1 arm for score, 2 for A/B
	Benchmarks        []string        `json:"benchmarks"` // suite ids; or Pack
	Pack              string          `json:"pack,omitempty"`
	TasksPerBenchmark int             `json:"tasks_per_benchmark"` // default 8 (the fewest that can reach significance after Holm over a few benchmarks), admitted tasks only
	Trials            int             `json:"trials"`              // repeats per task, default 2
	Faults            []string        `json:"faults"`              // "clean" always; plus model-down, broken-tool, misleading-instruction
	BudgetUSD         float64         `json:"budget_usd,omitempty"`
	// Split is the task split the evaluation runs: public (the default) or
	// held_out, an explicit opt-in for a held-out evaluation. Tasks of the
	// other split never run in it.
	Split string          `json:"split,omitempty"`
	Gate  *EvaluationGate `json:"gate,omitempty"` // checked automatically when the evaluation finishes
	// Worker is the connected worker (its pool) the evaluation runs on;
	// "" is the org's default worker, "shared" the shared worker. The saved
	// evaluation holds the resolved pool ("" for the shared worker).
	Worker string `json:"worker,omitempty"`
	// Stream carries the agent's memory from task to task inside each
	// benchmark (stream.go); in score mode the hidden repeat arm is the
	// fresh control.
	Stream *StreamSpec `json:"stream,omitempty"`
	// DiscloseBudget tells the agent its budget and deadline.
	DiscloseBudget bool `json:"disclose_budget,omitempty"`
}

// EvaluationGate is what an evaluation must show to pass. Scores are
// verified success rates (0 to 1) over clean runs, per capability.
type EvaluationGate struct {
	// BaselineVersion is the agent version the candidate is compared with;
	// 0 is the agent's live version (the last one released).
	BaselineVersion int `json:"baseline_version,omitempty"`
	// MinScore is the lowest score the candidate may have per capability.
	MinScore map[string]float64 `json:"min_score,omitempty"`
	// MaxRegression is the largest score drop from the baseline allowed on
	// any capability, as a fraction (0.05 is 5 points). 0 is no check.
	MaxRegression float64 `json:"max_regression,omitempty"`
}

// Gate states.
const (
	GatePass       = "pass"
	GateFail       = "fail"
	GateNoBaseline = "no_baseline" // a regression limit is set but no baseline run exists to compare with
)

// GateCheck is one rule of a gate and its outcome.
type GateCheck struct {
	Capability string   `json:"capability"`
	Rule       string   `json:"rule"` // min_score | max_regression
	Threshold  float64  `json:"threshold"`
	Observed   float64  `json:"observed"`
	Baseline   *float64 `json:"baseline,omitempty"`
	Pass       bool     `json:"pass"`
	Summary    string   `json:"summary"`
}

// GateBaseline names what the candidate was compared with.
type GateBaseline struct {
	Agent        string `json:"agent"`
	Version      int    `json:"version,omitempty"`
	Arm          string `json:"arm,omitempty"`           // the arm of this evaluation, when the baseline ran in it
	EvaluationID string `json:"evaluation_id,omitempty"` // the evaluation the baseline numbers come from
	Live         bool   `json:"live"`                    // the baseline is the agent's live version
}

// EvaluationGateResult is the gate's decision, written once when the
// evaluation finishes.
type EvaluationGateResult struct {
	State            string        `json:"state"` // pass | fail | no_baseline
	Agent            string        `json:"agent"`
	Candidate        string        `json:"candidate"` // the arm label judged
	CandidateVersion int           `json:"candidate_version,omitempty"`
	Baseline         *GateBaseline `json:"baseline,omitempty"`
	Checks           []GateCheck   `json:"checks"`
	Summary          string        `json:"summary"`
	DecidedAt        time.Time     `json:"decided_at"`
}

// EvaluationSkippedTask is one task left out of a benchmark that still runs
// (a quarantined task), with the reason.
type EvaluationSkippedTask struct {
	Task   string `json:"task"`
	Reason string `json:"reason"`
}

// EvaluationChild is one benchmark of an evaluation and its experiment.
type EvaluationChild struct {
	Benchmark    string `json:"benchmark"`
	ExperimentID string `json:"experiment_id"`
	Status       string `json:"status"`
	Skipped      string `json:"skipped,omitempty"`
	// SkippedTasks are tasks left out of this benchmark alone (quarantined);
	// the rest of the benchmark runs.
	SkippedTasks []EvaluationSkippedTask `json:"skipped_tasks,omitempty"`
	// FaultOf maps each condition id of the child experiment to the fault
	// it stands for (clean, model-down, broken-tool, misleading-instruction),
	// fixed at launch from faults.Conditions; the report card reads it.
	FaultOf map[string]string `json:"fault_of,omitempty"`
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
	// Schedule names the schedule that started this evaluation, if any.
	Schedule string `json:"schedule,omitempty"`
	// GateResult is set once the evaluation is done and its spec has a gate.
	GateResult *EvaluationGateResult `json:"gate_result,omitempty"`
	// RerunOf names the evaluation whose failures this one runs again (run
	// control, "Rerun failures"). Such an evaluation is exploratory: its
	// children never join the parent's verdicts, report card or a
	// leaderboard.
	RerunOf string `json:"rerun_of,omitempty"`
	// Paused is true while every running child is paused (drain).
	Paused bool `json:"paused,omitempty"`
	// NotifiedAt is when the finish of this evaluation was sent to the
	// org's notifications (webhooks, chat, email), so it is sent once.
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
}
