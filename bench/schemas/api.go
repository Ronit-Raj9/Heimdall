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

	JudgeCalibration *JudgeCalibrationVerdict `json:"judge_calibration,omitempty"` // W8 judge calibration: judged experiments only (Analysis.JudgeCalibration)
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
	// Synthetic is the arm's own provenance by the board's rules
	// (leaderboard.SyntheticArms): a smoke experiment, a scripted control
	// agent, or a proxy ledger showing the scripted model. It is independent
	// of the server's mode, so a scripted pair on a live server says so.
	Synthetic bool `json:"synthetic"`
	// Provenance is this run's own model evidence (leaderboard.Provenance*):
	// scripted, model, mixed, unobserved (no recorded model call) or
	// unavailable (its ledger could not be read). Only model names a run a
	// real model is recorded to have played (QC2-27-06).
	Provenance string `json:"provenance"`
	// ProvenanceNote says why provenance is unavailable; empty otherwise.
	ProvenanceNote string `json:"provenance_note,omitempty"`
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
	PairID string `json:"pair_id"`
	ExpID  string `json:"exp_id"`
	Task   string `json:"task"`
	Suite  string `json:"suite,omitempty"` // the experiment's suite id, so the task links to its evidence
	// SuiteVersion is the suite version the experiment froze, so a task link
	// names the exact revision its verifier ran (QC2-27-09).
	SuiteVersion    string           `json:"suite_version,omitempty"`
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
	// Superseded marks a run an operator requeued (run control): kept, never counted.
	Superseded  bool                 `json:"superseded,omitempty"`
	Grade       *Grade               `json:"grade"`
	GradeSource string               `json:"grade_source"` // "original": the grade the run was sealed with
	Metrics     map[string]Value     `json:"metrics"`
	Gates       []GateResult         `json:"gates"`
	Receipts    []Receipt            `json:"receipts"`
	Evidence    *RunEvidenceContract `json:"evidence_contract,omitempty"`
	Canonical   *CanonicalRun        `json:"canonical,omitempty"`
	SealError   string               `json:"seal_error,omitempty"` // why a sealed run's files do not match its seal; empty when they do or the run is not sealed

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

	// DebugAllowed says which debug actions the run allows, by the rule the
	// debug routes apply (fix wave 2, N133), so the page shows the reason
	// instead of hiding Replay, Fork and Debug.
	DebugAllowed *RunDebugAllowed `json:"debug_allowed,omitempty"`
}

// RunDebugAllowed says which debug actions a run allows (RunDetail
// debug_allowed). Reason says why one is refused; empty when both are allowed.
type RunDebugAllowed struct {
	Replay bool   `json:"replay"`
	Fork   bool   `json:"fork"`
	Reason string `json:"reason,omitempty"`
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
	// UI regroup (U2): the suite and arm ids from the manifest, and on the
	// list only the decision of the newest analysis on disk.
	Suite string `json:"suite,omitempty"`
	// SuiteVersion is the suite version the manifest froze, so task links
	// name the revision the evidence ran (QC2-27-09).
	SuiteVersion string   `json:"suite_version,omitempty"`
	Arms         []string `json:"arms,omitempty"`
	Decision     Decision `json:"decision,omitempty"`
	// EvaluationID is the evaluation that created the experiment, empty for
	// one a person started, so lists can tell an evaluation's benchmarks
	// from comparisons.
	EvaluationID string `json:"evaluation_id,omitempty"`
	// Debug is the debug mode of a replay, fork or step run (the manifest's
	// experiment.debug.mode) and DebugFromRun the run it debugs, so lists can
	// hide debug runs and tag them (fix wave 2, N29).
	Debug        string `json:"debug,omitempty"`
	DebugFromRun string `json:"debug_from_run,omitempty"`
	// MultiAgent: an arm runs a team or a multi-agent topology, so blame
	// between agents applies (N63).
	MultiAgent bool `json:"multi_agent,omitempty"`
	// Setups names each arm's agent, agent file and model, sorted by arm
	// (N66).
	Setups []ArmSetup `json:"setups,omitempty"`
	// ArmsDiffer says what differs between exactly two arms: "model" (the
	// same agent version on another model or provider), "none" (an A/A
	// check) or "agent" (N165). Empty for any other arm count.
	ArmsDiffer string `json:"arms_differ,omitempty"`
	// Problem is why no run could start (a worker refusal, a slot that
	// failed before its run) while nothing sealed, and RunJobID the
	// experiment's newest run job, so the page can link its log (N151).
	Problem  string `json:"problem,omitempty"`
	RunJobID string `json:"run_job_id,omitempty"`
}

// ArmSetup is one arm's setup on ExperimentStatus.
type ArmSetup struct {
	Arm       string `json:"arm"`
	Agent     string `json:"agent"`      // the agent config id
	AgentFile string `json:"agent_file"` // the agent file the arm was built from
	Model     string `json:"model"`
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
	// Launch says whether this server plans and starts experiments. False on
	// an offline (replay-only) server; absent where the router does not know.
	Launch *bool `json:"launch,omitempty"`
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
