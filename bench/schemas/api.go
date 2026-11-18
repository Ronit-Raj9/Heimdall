package schemas

import "time"

// HTTP API payloads (DESIGN.md 16.7). Every JSON response object carries a
// top-level "mode" field beside these fields; the server adds it. The
// TypeScript mirror is ui/lib/types/bench.ts.

// ConfigChange is one typed config change between arm A and arm B, from the
// arms' `set:` overrides (DESIGN.md 8.6).
type ConfigChange struct {
	Path string `json:"path"` // dotted agent-config path, e.g. notes.user_file
	A    any    `json:"a"`
	B    any    `json:"b"`
}

// GateSummary is one gate over all runs, for the verdict page's checklist.
type GateSummary struct {
	Gate     string     `json:"gate"`
	Version  string     `json:"version"`
	Effect   GateEffect `json:"effect"`
	Baseline bool       `json:"baseline"`
	Fired    Ratio      `json:"fired"` // runs the gate fired on over runs checked
	Detail   string     `json:"detail,omitempty"`
}

// VerdictPayload is GET /experiments/{id}/verdict.
type VerdictPayload struct {
	ExpID       string           `json:"exp_id"`
	AnalysisID  string           `json:"analysis_id"`
	GradeSource string           `json:"grade_source"`
	Suite       string           `json:"suite"`
	Verdict     Verdict          `json:"verdict"`
	Naive       map[string]Ratio `json:"naive"`
	Gates       []GateSummary    `json:"gates"`
	ConfigDiff  []ConfigChange   `json:"config_diff"`
	Policy      *PolicyResult    `json:"policy,omitempty"`

	// Human review counts of the analysis (Analysis.ReviewedRuns).
	ReviewedRuns      int `json:"reviewed_runs,omitempty"`
	ReviewChangedRuns int `json:"review_changed_runs,omitempty"`
}

// DiagnosticsPayload is GET /experiments/{id}/diagnostics.
type DiagnosticsPayload struct {
	AnalysisID  string                      `json:"analysis_id"`
	ExpID       string                      `json:"exp_id"`
	Diagnostics []DiagnosticsRow            `json:"diagnostics"`
	Meters      []Meter                     `json:"meters"`
	ArmMetrics  map[string]map[string]Value `json:"arm_metrics"`
}

// GridCell is one slot in the task x trial grid.
type GridCell struct {
	SlotID  string   `json:"slot_id"`
	PairID  string   `json:"pair_id"`
	RunID   *string  `json:"run_id"` // nil until the slot has a counted run
	Arm     string   `json:"arm"`
	Trial   int      `json:"trial"`
	Outcome *Outcome `json:"outcome"`
	Y       Value    `json:"y"`
}

// GridRow is one (task, condition) row.
type GridRow struct {
	Task       string     `json:"task"`
	Condition  string     `json:"condition"`
	Diff       TaskDiff   `json:"diff"`
	Discordant bool       `json:"discordant"`
	Cells      []GridCell `json:"cells"`
}

// Grid is GET /experiments/{id}/grid: discordant rows first.
type Grid struct {
	ExpID      string    `json:"exp_id"`
	Arms       []string  `json:"arms"`
	Conditions []string  `json:"conditions"`
	Trials     int       `json:"trials"`
	Rows       []GridRow `json:"rows"`
}

// PairRun is one arm's run inside a pair.
type PairRun struct {
	Arm     string           `json:"arm"`
	Slot    Slot             `json:"slot"`
	Run     Run              `json:"run"`
	Metrics map[string]Value `json:"metrics"`
}

// AlignedStep is one aligned position: the event id of each arm at that step,
// or nil where a lane has no event.
type AlignedStep struct {
	Index    int                `json:"index"`
	Type     EventType          `json:"type"`
	Target   string             `json:"target"`
	Events   map[string]*string `json:"events"`
	Diverged bool               `json:"diverged"`
}

// FirstDivergence is an observation, never a root cause (DESIGN.md 11).
type FirstDivergence struct {
	Index int    `json:"index"`
	Note  string `json:"note"`
}

// Pair is GET /pairs/{pair_id}.
type Pair struct {
	PairID          string           `json:"pair_id"`
	ExpID           string           `json:"exp_id"`
	Task            string           `json:"task"`
	Condition       string           `json:"condition"`
	Trial           int              `json:"trial"`
	Runs            []PairRun        `json:"runs"`
	Alignment       []AlignedStep    `json:"alignment"`
	FirstDivergence *FirstDivergence `json:"first_divergence"`
}

// RunEvents is GET /runs/{id}/events.
type RunEvents struct {
	RunID    string  `json:"run_id"`
	Complete bool    `json:"complete"` // false when the gateway could not return every row; shown, never hidden
	Events   []Event `json:"events"`
}

// RunDetail is GET /runs/{id}: one run with its seal, grade, metrics, gates
// and receipts. A file the run has not written yet is null or empty, never
// zeros. SealHash is empty until the run is sealed.
type RunDetail struct {
	AdapterFault *AdapterFaultEvidence `json:"adapter_fault,omitempty"`
	Slot         Slot                  `json:"slot"`
	Run          Run                   `json:"run"`
	Sealed       bool                  `json:"sealed"`
	SealHash     string                `json:"seal_hash"`
	Grade        *Grade                `json:"grade"`
	GradeSource  string                `json:"grade_source"` // "original": the grade the run was sealed with
	Metrics      map[string]Value      `json:"metrics"`
	Gates        []GateResult          `json:"gates"`
	Receipts     []Receipt             `json:"receipts"`
	Evidence     *RunEvidenceContract  `json:"evidence_contract,omitempty"`
	Canonical    *CanonicalRun         `json:"canonical,omitempty"`
	SealError    string                `json:"seal_error,omitempty"` // why a sealed run's files do not match its seal; empty when they do or the run is not sealed

	// Scenario evidence (scenario.go); each is empty for a plain diff run.
	ToolCalls []ToolCall      `json:"tool_calls,omitempty"`
	Sessions  []SessionResult `json:"sessions,omitempty"`
	Answer    *Answer         `json:"answer,omitempty"`
	State     []StateFile     `json:"state,omitempty"`
	Services  []ServiceResult `json:"services,omitempty"`
	Artifacts []ArtifactFile  `json:"artifacts,omitempty"`

	// Agents is the agent tree of a multi-agent run (agents.json); nil for a
	// single-agent run.
	Agents *AgentTree `json:"agents,omitempty"`
}

// RunDiff is GET /runs/{id}/diff. Patch is inert text.
type RunDiff struct {
	RunID string `json:"run_id"`
	Diff  Diff   `json:"diff"`
	Patch string `json:"patch"`
}

// RunReceipts is GET /runs/{id}/receipts.
type RunReceipts struct {
	RunID    string    `json:"run_id"`
	Receipts []Receipt `json:"receipts"`
}

// PlanCondition is one condition's size in a plan.
type PlanCondition struct {
	Condition string `json:"condition"`
	Tasks     int    `json:"tasks"`
	Trials    int    `json:"trials"`
	Slots     int    `json:"slots"`
}

// ExperimentPlan is POST /experiments/plan. Nothing runs.
type ExperimentPlan struct {
	Slots        int             `json:"slots"`
	Pairs        int             `json:"pairs"`
	Conditions   []PlanCondition `json:"conditions"`
	MDE          float64         `json:"mde"` // percentage points on the primary endpoint
	MDEAssumedSD bool            `json:"mde_assumed_sd"`
	SD           float64         `json:"sd"`
	EstWallS     Value           `json:"est_wall_s"`
	EstUSD       Value           `json:"est_usd"`
	Errors       []string        `json:"errors"` // nothing can start while non-empty
	Warnings     []string        `json:"warnings"`
}

// ExperimentCreated is POST /experiments: the manifest is frozen and hashed.
type ExperimentCreated struct {
	ExpID        string    `json:"exp_id"`
	ManifestHash string    `json:"manifest_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

// ExperimentState is an experiment's lifecycle state.
type ExperimentState string

// Experiment states.
const (
	ExperimentCreatedState ExperimentState = "created"
	ExperimentRunning      ExperimentState = "running"
	ExperimentAnalyzing    ExperimentState = "analyzing"
	ExperimentDone         ExperimentState = "done"
	ExperimentFailed       ExperimentState = "failed"
	ExperimentCancelled    ExperimentState = "cancelled"
)

// SlotProgress counts slots by state.
type SlotProgress struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

// ExperimentStatus is GET /experiments/{id} and POST /experiments/{id}/start.
type ExperimentStatus struct {
	ExpID        string          `json:"exp_id"`
	Name         string          `json:"name"`
	State        ExperimentState `json:"state"`
	ManifestHash string          `json:"manifest_hash"`
	FrozenAt     time.Time       `json:"frozen_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
	Slots        SlotProgress    `json:"slots"`
	SpentUSD     Value           `json:"spent_usd"`
	AnalysisID   string          `json:"analysis_id,omitempty"`
}

// ExperimentList is GET /experiments.
type ExperimentList struct {
	Experiments []ExperimentStatus `json:"experiments"`
}

// LiveEvent is one server-sent event on /experiments/{id}/events.
type LiveEvent struct {
	Seq     int       `json:"seq"`
	Kind    string    `json:"kind"` // slot_started | stage | slot_sealed | budget_stop | experiment_done | error | end
	SlotID  string    `json:"slot_id,omitempty"`
	RunID   string    `json:"run_id,omitempty"`
	Stage   Stage     `json:"stage,omitempty"`
	Outcome Outcome   `json:"outcome,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// EventsPoll is GET /experiments/{id}/events for a client that does not ask
// for text/event-stream: the live events after ?since=<seq>, the cursor for
// the next poll, and the experiment's status.
type EventsPoll struct {
	ExpID     string           `json:"exp_id"`
	Events    []LiveEvent      `json:"events"`
	Next      int              `json:"next"`
	Truncated bool             `json:"truncated"` // older events fell out of the server's window
	Status    ExperimentStatus `json:"status"`
}

// Health is GET /health.
type Health struct {
	Status  string `json:"status"` // ok
	Version string `json:"version"`
}

// DoctorStatus is one preflight check's state.
type DoctorStatus string

// Doctor statuses.
const (
	DoctorPass DoctorStatus = "pass"
	DoctorFail DoctorStatus = "fail"
	DoctorWarn DoctorStatus = "warn"
	DoctorSkip DoctorStatus = "skip"
)

// DoctorCheck is one preflight check.
type DoctorCheck struct {
	ID     string       `json:"id"`
	Status DoctorStatus `json:"status"`
	Detail string       `json:"detail"`
}

// Doctor is GET /doctor.
type Doctor struct {
	OK     bool          `json:"ok"`
	Checks []DoctorCheck `json:"checks"`
}

// AuditRow is one task of a published two-arm comparison.
type AuditRow struct {
	Task  string `json:"task"`
	APass int    `json:"a_pass"`
	AN    int    `json:"a_n"`
	BPass int    `json:"b_pass"`
	BN    int    `json:"b_n"`
}

// AuditRequest is the JSON body of POST /audit. A text/csv body with the
// header task,a_pass,a_n,b_pass,b_n is also accepted. The response is a Verdict.
type AuditRequest struct {
	Rows  []AuditRow `json:"rows"`
	Alpha float64    `json:"alpha,omitempty"`
}

// RegradeRequest is POST /regrade (stretch).
type RegradeRequest struct {
	ExpID        string `json:"exp_id"`
	SuiteVersion string `json:"suite_version"`
}

// RegradeResult is the response of POST /regrade.
type RegradeResult struct {
	RegradeID string `json:"regrade_id"`
	ExpID     string `json:"exp_id"`
	Runs      int    `json:"runs"`
}
