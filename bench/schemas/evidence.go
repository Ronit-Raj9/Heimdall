package schemas

import "context"

// Evidence is everything collected about one run. Evidence sources fill their
// part during the collect stage; later stages read it.
type Evidence struct {
	AdapterFault *AdapterFaultEvidence `json:"adapter_fault,omitempty"`
	Contract     *RunEvidenceContract  `json:"evidence_contract,omitempty"`
	Status       *EvidenceStatus       `json:"evidence_status,omitempty"`
	Exec         *ExecResult           `json:"exec,omitempty"`
	Diff         *Diff                 `json:"diff,omitempty"`
	Requests     *RequestLog           `json:"requests,omitempty"` // gateway rows
	Ledger       []LedgerEntry         `json:"ledger,omitempty"`   // fault proxy
	Events       []Event               `json:"events,omitempty"`
	Claim        *Claim                `json:"claim,omitempty"`
	AgentTrace   *ParseDiagnostics     `json:"agent_trace,omitempty"`
	Cost         []CostRow             `json:"cost,omitempty"`
	ShimLog      []ShimCall            `json:"shim_log,omitempty"`
	Grade        *Grade                `json:"grade,omitempty"`
	Gates        []GateResult          `json:"gates,omitempty"`
	LogicalSteps []LogicalRequest      `json:"logical_steps,omitempty"`
	Attempts     []AttemptRecord       `json:"attempts,omitempty"`
	Retries      []RetryRecord         `json:"retries,omitempty"`
	Fallbacks    []FallbackRecord      `json:"fallbacks,omitempty"`
	// RequestedTools are the tool calls the model asked for, read from the
	// gateway rows. Evidence.ToolCalls is the tool proxy ledger: what ran.
	RequestedTools []ToolCallRecord  `json:"requested_tool_calls,omitempty"`
	Protect        map[string]string `json:"protected_after,omitempty"` // sha256 of protected files after the run

	// Scenario evidence (scenario.go).
	ToolCalls []ToolCall      `json:"tool_calls,omitempty"` // tool proxy ledger
	Sessions  []SessionResult `json:"sessions,omitempty"`   // one per session of a multi-session run
	Answer    *Answer         `json:"answer,omitempty"`     // the exported answer
	State     []StateFile     `json:"state,omitempty"`      // exported service state
	Services  []ServiceResult `json:"services,omitempty"`   // services that ran beside the agent
	Snapshot  string          `json:"snapshot,omitempty"`   // image reference of the frozen agent container, for snapshot grading
	Artifacts []ArtifactFile  `json:"artifacts,omitempty"`  // exported artifact files of an artifact task

	// Agent steps evidence (steps.go, docs/steps-and-evidence.md): the
	// trusted observers' records and the unified step timeline.
	Processes  []ProcessRecord  `json:"processes,omitempty"`   // process observer
	FileEvents []FileEvent      `json:"file_events,omitempty"` // workspace observer
	Observers  []ObserverStatus `json:"observers,omitempty"`   // one per observer and session
	Steps      []StepRecord     `json:"steps,omitempty"`       // unified timeline, also steps.jsonl

	// AgentEvents says what the agent reported about itself (agentevents.go);
	// the events are in Events with source agent_reported.
	AgentEvents *AgentEventStatus `json:"agent_events,omitempty"`

	// Remote is a remote agent's channel summary and self-report (remote.go).
	Remote *RemoteEvidence `json:"remote,omitempty"`
}

// EvidenceQuality describes how completely a run's required evidence was observed.
type EvidenceQuality string

const (
	EvidenceQualityUnknown  EvidenceQuality = "unknown"
	EvidenceQualityComplete EvidenceQuality = "complete"
	EvidenceQualityPartial  EvidenceQuality = "partial"
)

// RunEvidenceContract declares required and observed evidence for one run.
type RunEvidenceContract struct {
	Required        []string         `json:"required,omitempty"`
	Available       []string         `json:"available,omitempty"`
	Missing         []string         `json:"missing,omitempty"`
	Quality         EvidenceQuality  `json:"quality"`
	Sources         []EvidenceOrigin `json:"sources,omitempty"`
	OptionalMissing []string         `json:"optional_missing,omitempty"`
	Conflicts       []ConflictRecord `json:"conflicts,omitempty"`
}

// EvidenceSource reads one record of what happened and adds it to ev.
type EvidenceSource interface {
	Part
	Collect(ctx context.Context, rc *RunContext, ev *Evidence) error
}

// CostRow is one physical upstream attempt priced from the pinned snapshot.
// Bifrost's own cost is kept for reconciliation and never summed with it.
type CostRow struct {
	RequestID   string   `json:"request_id"`
	Attempt     int      `json:"attempt"`
	Model       string   `json:"model"`
	Tokens      Tokens   `json:"tokens"`
	PriceRef    string   `json:"price_ref"` // snapshot id plus sha256
	USD         Value    `json:"usd"`
	BifrostUSD  *float64 `json:"bifrost_usd,omitempty"`
	FaultInject bool     `json:"fault_inject"` // an injected failure; billed as zero by the provider
}

// ShimCall is one invocation recorded by a tool shim.
type ShimCall struct {
	Tool        string `json:"tool"`
	Argv        string `json:"argv"`
	Call        int    `json:"call"`
	Action      string `json:"action"` // crash | empty | pass
	Nonce       string `json:"nonce,omitempty"`
	At          string `json:"at"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
}

// Grade is the hidden verifier's result for one diff.
type Grade struct {
	Passed           bool     `json:"passed"`
	InvalidEvaluator bool     `json:"invalid_evaluator"` // the grader could not decide; maps to unknown
	ExitCode         int      `json:"exit_code"`
	TestsRan         int      `json:"tests_ran"`
	Failures         int      `json:"failures"`
	Errors           int      `json:"errors"`
	Skipped          int      `json:"skipped"`
	MissingRequired  []string `json:"missing_required,omitempty"`
	FailedTests      []string `json:"failed_tests,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	SuiteVersion     string   `json:"suite_version"`
	GraderVersion    string   `json:"grader_version"`
	DiffSHA256       string   `json:"diff_sha256"`
	ParentSeal       string   `json:"parent_seal,omitempty"` // set on regrades

	// ResultFormat says how the verifier reported: junit (default), reward or
	// exit_code (SuiteVerifier.Result). Reward is the verifier's own number
	// when it wrote one, and Dimensions its named sub-scores (correctness,
	// safety, performance, ...). Dimensions are reported side by side and are
	// never blended into Passed.
	ResultFormat string             `json:"result_format,omitempty"`
	Reward       *float64           `json:"reward,omitempty"`
	Dimensions   map[string]float64 `json:"dimensions,omitempty"`

	// Mode is how the grade was reached when it is not a clean-room diff
	// grade: snapshot, answer, state or artifact. Judged is true when a judge model
	// decided Passed, and Judge records what it rested on. Category is the
	// task's answer category, for breakdowns.
	Mode     string       `json:"mode,omitempty"`
	Judged   bool         `json:"judged,omitempty"`
	Judge    *JudgeRecord `json:"judge,omitempty"`
	Category string       `json:"category,omitempty"`

	Visual *VisualGrade `json:"visual,omitempty"` // X1 vision grading: captures, per-check numbers, judge report (visual.go)
}

// Grader decides the outcome from the exported diff, never from the agent's
// container. The task carries the verifier tests and the suite carries the
// command, image and restore list.
type Grader interface {
	Part
	Grade(ctx context.Context, rc *RunContext, diff *Diff) (*Grade, error)
}

// GateEffect is what a fired gate does.
type GateEffect string

// Gate effects.
const (
	GateVeto       GateEffect = "veto"       // the run's outcome becomes integrity_violation (0 plus a flag)
	GateInvalidate GateEffect = "invalidate" // the experiment is INVALID
	GateReport     GateEffect = "report"     // reported only; never changes an outcome
	GateAdmission  GateEffect = "admission"  // checked at task admission
)

// Gate is one deterministic integrity check.
type Gate interface {
	Part
	Effect() GateEffect
	// Baseline gates cannot be switched off by an experiment.
	Baseline() bool
	Check(ctx context.Context, rc *RunContext, ev *Evidence) (*GateResult, error)
}

// GateResult is one gate's verdict on one run.
type GateResult struct {
	Gate     string     `json:"gate"`
	Version  string     `json:"version"`
	Effect   GateEffect `json:"effect"`
	Fired    bool       `json:"fired"`
	Detail   string     `json:"detail,omitempty"`
	Evidence []string   `json:"evidence,omitempty"`
}

// ValueKind selects the UI renderer and the aggregation for a metric.
type ValueKind string

// Value kinds with renderers in the UI.
const (
	KindRate     ValueKind = "rate" // 0..1 per run (pass/fail), shown as a percentage over runs
	KindCount    ValueKind = "count"
	KindTokens   ValueKind = "tokens"
	KindUSD      ValueKind = "usd"
	KindDuration ValueKind = "duration" // seconds
)

// MetricDescriptor describes a metric to the catalog and the UI.
type MetricDescriptor struct {
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	Label          string        `json:"label"`
	Kind           ValueKind     `json:"kind"`
	HigherIsBetter bool          `json:"higher_is_better"`
	MinEvidence    EvidenceLevel `json:"min_evidence"`
	Description    string        `json:"description"`
	PS2            bool          `json:"ps2"` // one of the six metrics PS2 requires
}

// Metric computes one value for one run. A value it cannot observe is unknown,
// never zero.
type Metric interface {
	Part
	Descriptor() MetricDescriptor
	Compute(ctx context.Context, rc *RunContext, ev *Evidence) (Value, error)
}

// DerivedMetric is a metric that also yields values under names known only at
// run time, such as one value per reward dimension a verifier reported
// (reward_<name>). The measure stage stores them beside the registered
// metrics; a derived name never replaces a registered one and never feeds
// the primary endpoint.
type DerivedMetric interface {
	Metric
	ComputeDerived(ctx context.Context, rc *RunContext, ev *Evidence) (map[string]Value, error)
}
