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
	Counted      bool   `json:"counted"`            // false for compatibility runs; they never enter the verdict
	Optional     bool   `json:"optional,omitempty"` // W7 adaptive trials: runs only when the frozen rule releases it (trials.go)
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

// ExcludedOutOfMemory is the exclusion reason of a run the out-of-memory
// killer ended (RunAgentInfo.OOMKilled): infrastructure, never the agent's.
const ExcludedOutOfMemory = "out_of_memory"

// ExcludedReason is why a sealed run is left out of scores: out_of_memory
// for a run the out-of-memory killer ended, else the outcome itself
// (infra_error, invalid_evaluator, unknown); "" when the run scores or is
// not counted at all (aborted_infra gets a new run).
func ExcludedReason(r *Run) string {
	if r == nil || r.Outcome == OutcomeAbortedInfra {
		return ""
	}
	if _, ok := r.Outcome.Score(); ok {
		return ""
	}
	if r.Agent.OOMKilled {
		return ExcludedOutOfMemory
	}
	if r.Outcome == "" {
		return string(OutcomeUnknown)
	}
	return string(r.Outcome)
}

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
	// Practice marks a run whose model calls the fault proxy's scripted
	// model answered (a smoke or scripted_upstream experiment, a scripted
	// control agent, or a scripted ledger line): its numbers are not
	// findings, and report cards, leaderboards and verdicts leave it out
	// (X01).
	Practice bool            `json:"practice,omitempty"`
	Hardware *HardwareRecord `json:"hardware,omitempty"` // X7: the GPUs a GPU task's run held (schemas/gpu.go); nil otherwise
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
	// OOMKilled: the out-of-memory killer ended the agent (ExecResult). The
	// run is infra_error and is excluded from scores as "out of memory".
	OOMKilled bool `json:"oom_killed,omitempty"`
}

// RunGatewayInfo links the run to its gateway records.
type RunGatewayInfo struct {
	VirtualKeyID string `json:"virtual_key_id,omitempty"`
	KeySlot      string `json:"key_slot,omitempty"`
	OrgKeyID     string `json:"org_key_id,omitempty"` // the organization model key that served the run (budget accounting)
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
	// NoPaidTraffic is set by the engine on a run whose model calls must
	// never reach a paid provider (scripted fault mode, smoke or
	// scripted_upstream). Parts that would mint their own gateway keys (model
	// judges) refuse instead of calling.
	NoPaidTraffic bool

	// Scenario state (scenario.go), set by the engine as the run proceeds.
	Services     []ServiceHandle // started services, including the tool proxy's view of them
	Session      *SessionSpec    // the session being run; nil for a single-session task
	SessionIndex int             // 0-based index of Session

	// RemoteTools is the published tool proxy of a remote agent's run with
	// remote.tools expose (remote.go); nil otherwise. It holds the run's
	// bearer token and is never written to disk.
	RemoteTools *RemoteToolsAccess
	// B1: the agent gateway registrations of an a2a run (a2a.go), set by
	// the engine before the agent starts.
	A2A *A2ARun

	// TeamRoutes maps each team member id to its tool proxy route token
	// (agents.go): the member reaches services at /m/<token>/s/<id>/ and the
	// proxy stamps ToolCall.AgentID from it. Nil outside team runs; never
	// written to disk.
	TeamRoutes map[string]string

	// GPU is the GPUs this run holds from the scheduler's pool (X7,
	// schemas/gpu.go): only the agent container and the verifier get them.
	// Nil for a run of a task without a GPU request; never written to disk.
	GPU *GPUAssignment

	// Secrets are the org secret values this run names (external api
	// auth_env, agent env {{secret.NAME}}), resolved at prepare
	// (schemas/secrets.go). Nil means the run reads the bench's own
	// environment (no secret source). Held in memory only: never logged,
	// written to disk or sealed.
	Secrets map[string]string `json:"-"`
}

// outcomeScoredMetrics are the metrics whose value is the run's verified
// outcome (metrics.verifiedOutcome). They can only be observed when the run's
// final outcome scores (Y observed).
var outcomeScoredMetrics = map[string]bool{
	"verified_success": true,
	"safe_success":     true,
	"answer_correct":   true,
}

// OutcomeScoredMetric reports whether metric's value is the run's verified
// outcome, so it must agree with the run's final Y.
func OutcomeScoredMetric(metric string) bool {
	return outcomeScoredMetrics[metric]
}

// ReconcileOutcomeMetric returns v, or unknown when metric is outcome-scored
// and the run's final outcome does not score (for example a passing grade
// whose cleanup failed, sealed infra_error with Y unknown). The metric is
// computed before the final outcome is decided, so it must never outvote it.
// An empty outcome (no outcome recorded) leaves v as it is.
func ReconcileOutcomeMetric(metric string, v Value, outcome Outcome) Value {
	if !OutcomeScoredMetric(metric) || outcome == "" || !v.IsObserved() {
		return v
	}
	if y, scores := outcome.Score(); scores {
		if got, _ := v.Float(); y == 0 && got > 0 {
			// QC2-04-03: a scoring failure outcome (an early session crash)
			// outranks a passing grade; the metric never votes a pass.
			return Observed(0, v.Evidence)
		}
		return v
	}
	return Unknown("run outcome "+string(outcome)+" does not score", v.Evidence)
}
