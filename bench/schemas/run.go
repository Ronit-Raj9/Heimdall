package schemas

import "time"

// Slot is one assigned (task, arm, condition, trial). A slot is never dropped.
type Slot struct {
	SlotID       string `json:"slot_id"`
	PairID       string `json:"pair_id"` // the slots of all arms for one (task, condition, trial)
	ExpID        string `json:"exp_id"`
	Suite        string `json:"suite"`
	Task         string `json:"task"`
	TaskVersion  string `json:"task_version"`
	TaskHash     string `json:"task_hash,omitempty"`
	RunInputHash string `json:"run_input_hash,omitempty"`
	Arm          string `json:"arm"`       // arm id, or "compat" for the compatibility agent
	Condition    string `json:"condition"` // clean or a probe id
	Trial        int    `json:"trial"`     // 1-based
	OrderIndex   int    `json:"order_index"`
	Counted      bool   `json:"counted"` // false for compatibility runs; they never enter the verdict
}

// Stage is one step of the slot pipeline (DESIGN.md 8.2). This list is the
// only order; the run state machine uses the same names.
type Stage string

// Pipeline stages, in order.
const (
	StagePlanned Stage = "planned"
	StageLeased  Stage = "leased"
	StagePrepare Stage = "prepare" // Environment.Create, Gateway.MintKey, arm files installed
	StageArm     Stage = "arm"     // ProbeLayer.Arm
	StageRun     Stage = "run"     // AgentAdapter.Plan, Environment.Exec
	StageDisarm  Stage = "disarm"  // ProbeLayer.Disarm, reverse order
	StageExport  Stage = "export"  // Environment.ExportDiff
	StageCollect Stage = "collect" // EvidenceSource.Collect (agent_stream calls the Parser)
	StageGrade   Stage = "grade"   // Grader.Grade in a fresh container
	StageGate    Stage = "gate"    // Gate.Check
	StageMeasure Stage = "measure" // Metric.Compute
	StageReceipt Stage = "receipt" // ProbeLayer.Receipts
	StageCleanup Stage = "cleanup" // Environment.Destroy, Gateway.RevokeKey; always runs
	StageSeal    Stage = "seal"    // Store.Seal
)

// Stages is the pipeline order after leasing.
var Stages = []Stage{
	StagePrepare, StageArm, StageRun, StageDisarm, StageExport, StageCollect,
	StageGrade, StageGate, StageMeasure, StageReceipt, StageCleanup, StageSeal,
}

// Outcome is a run's result.
type Outcome string

// Outcomes and how the verdict maps them (DESIGN.md 9.3, intention to treat).
const (
	OutcomePass               Outcome = "pass"                // 1
	OutcomeFail               Outcome = "fail"                // 0
	OutcomeTimeout            Outcome = "timeout"             // 0: the agent exceeded its wall budget
	OutcomeBudget             Outcome = "budget"              // 0: the agent exhausted its turn or spend budget
	OutcomeCrash              Outcome = "crash"               // 0: the agent process failed
	OutcomeRefusal            Outcome = "refusal"             // 0
	OutcomeIntegrityViolation Outcome = "integrity_violation" // 0 plus a flag: a veto gate fired
	OutcomeInvalidEvaluator   Outcome = "invalid_evaluator"   // unknown: the grader could not decide
	OutcomeInfraError         Outcome = "infra_error"         // unknown: the bench or gateway failed after the agent started, or the run ended on an unreachable provider
	OutcomeUnknown            Outcome = "unknown"             // unknown
	OutcomeAbortedInfra       Outcome = "aborted_infra"       // not counted: failed before the agent started, or no model call was answered because the provider was unreachable; the slot gets a new run
)

// Score maps an outcome to the verdict's 0/1 scale. ok is false for outcomes
// that count as unknown or are not counted.
func (o Outcome) Score() (y float64, ok bool) {
	switch o {
	case OutcomePass:
		return 1, true
	case OutcomeFail, OutcomeTimeout, OutcomeBudget, OutcomeCrash, OutcomeRefusal, OutcomeIntegrityViolation:
		return 0, true
	default:
		return 0, false
	}
}

// Run is one execution of a slot. A slot has exactly one counted run.
type Run struct {
	RunID          string                `json:"run_id"`
	SlotID         string                `json:"slot_id"`
	PairID         string                `json:"pair_id"`
	ExpID          string                `json:"exp_id"`
	Attempt        int                   `json:"attempt"` // 1-based; >1 only after aborted_infra
	Stage          Stage                 `json:"stage"`   // last completed stage
	ManifestHash   string                `json:"manifest_hash"`
	SuiteLockHash  string                `json:"suite_lock_hash"`
	TaskHash       string                `json:"task_hash,omitempty"`
	RunInputHash   string                `json:"run_input_hash,omitempty"`
	CanonicalHash  string                `json:"canonical_hash,omitempty"`
	Outcome        Outcome               `json:"outcome"`
	Y              Value                 `json:"y"`
	Claim          *Claim                `json:"claim,omitempty"`
	Timing         RunTiming             `json:"timing"`
	Agent          RunAgentInfo          `json:"agent"`
	ArmIdentity    *CanonicalArmIdentity `json:"arm_identity,omitempty"`
	BudgetSnapshot *BudgetSnapshot       `json:"budget_snapshot,omitempty"`
	Gateway        RunGatewayInfo        `json:"gateway"`
	Failures       []PartFailure         `json:"failures,omitempty"`
}

// RunTiming records when the run passed its milestones.
type RunTiming struct {
	Started      time.Time  `json:"started"`
	AgentStarted *time.Time `json:"agent_started,omitempty"`
	AgentExited  *time.Time `json:"agent_exited,omitempty"`
	Sealed       *time.Time `json:"sealed,omitempty"`
}

// RunAgentInfo identifies the system under test for this run.
type RunAgentInfo struct {
	ConfigID         string               `json:"config_id"`
	Adapter          string               `json:"adapter"`
	Parser           string               `json:"parser,omitempty"`
	ConfigHash       string               `json:"config_hash"`
	AgentVersion     string               `json:"agent_version,omitempty"`
	ImageDigest      string               `json:"image_digest,omitempty"`
	ParserVersion    string               `json:"parser_version,omitempty"`
	TraceSource      TraceSource          `json:"trace_source,omitempty"`
	TraceQuality     TraceQuality         `json:"trace_quality,omitempty"`
	TraceDiagnostics *ParseDiagnostics    `json:"trace_diagnostics,omitempty"`
	Capabilities     *AdapterCapabilities `json:"capabilities,omitempty"`
	CapabilityGaps   []string             `json:"capability_gaps,omitempty"`
	ExitCode         *int                 `json:"exit_code,omitempty"`
	TimedOut         bool                 `json:"timed_out"`
}

// RunGatewayInfo links the run to its gateway records.
type RunGatewayInfo struct {
	VirtualKeyID string `json:"virtual_key_id,omitempty"`
	KeySlot      string `json:"key_slot,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
}

// PartFailure records that one part failed for this run. A part failure never
// silently drops the slot.
type PartFailure struct {
	Stage Stage  `json:"stage"`
	Part  string `json:"part"`
	Error string `json:"error"`
}

// TransitionStatus is the status of one stage transition.
type TransitionStatus string

// Transition statuses.
const (
	TransitionStarted   TransitionStatus = "started"
	TransitionCompleted TransitionStatus = "completed"
	TransitionFailed    TransitionStatus = "failed"
)

// Transition is one line of a run's append-only transitions.jsonl. Resume
// reads it to decide what a crashed run may and may not redo.
type Transition struct {
	Stage  Stage            `json:"stage"`
	Status TransitionStatus `json:"status"`
	At     time.Time        `json:"at"`
	Error  string           `json:"error,omitempty"`
}

// Lease is the record that one scheduler owns a slot.
type Lease struct {
	SlotID      string    `json:"slot_id"`
	Owner       string    `json:"owner"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
}

// RunContext is everything a part needs for one run. The engine builds it.
type RunContext struct {
	FaultRuntime  *AdapterFaultRuntime // ephemeral secrets; never serialized
	FaultSessions []string             // sessions actually started
	Experiment    *Experiment
	Manifest      *Manifest
	Suite         *Suite
	Task          *Task
	Slot          *Slot
	Run           *Run
	Agent         *AgentConfig // resolved config for the slot's arm
	Probe         *ProbeSpec   // nil for clean
	Handle        *EnvironmentHandle
	Key           *RunKey
	Exec          *ExecResult
	Dir           string // the run folder
	Logger        Logger

	// Scenario state (scenario.go), set by the engine as the run proceeds.
	Services     []ServiceHandle // started services, including the tool proxy's view of them
	Session      *SessionSpec    // the session being run; nil for a single-session task
	SessionIndex int             // 0-based index of Session

	// RemoteTools is the published tool proxy of a remote agent's run with
	// remote.tools expose (remote.go); nil otherwise. It holds the run's
	// bearer token and is never written to disk.
	RemoteTools *RemoteToolsAccess
}
