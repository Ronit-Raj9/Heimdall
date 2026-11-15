package schemas

import "time"

// CanonicalVersion is the version of the framework-neutral run document.
const CanonicalVersion = "1.0"

// CanonicalSchemaVersion identifies the complete benchmark run schema.
const CanonicalSchemaVersion = "bench-run-v2"

// EvidenceOrigin identifies the record that supports a value. It is separate
// from EventSource so old event source names remain readable.
type EvidenceOrigin string

// Evidence sources used by canonical metrics and joins.
const (
	EvidenceSourceHiddenGrader  EvidenceOrigin = "hidden_grader"
	EvidenceSourceBifrostLog    EvidenceOrigin = "bifrost_log"
	EvidenceSourceFaultProxy    EvidenceOrigin = "fault_proxy"
	EvidenceSourceToolRunner    EvidenceOrigin = "tool_runner"
	EvidenceSourceAgentStream   EvidenceOrigin = "agent_stream"
	EvidenceSourceWorkspaceDiff EvidenceOrigin = "workspace_diff"
	EvidenceSourceProcessResult EvidenceOrigin = "process_result"
	EvidenceSourceInferred      EvidenceOrigin = "inferred"
	EvidenceSourceUnknown       EvidenceOrigin = "unknown"
)

// EvidenceStrength describes how directly a source proves a value.
type EvidenceStrength string

// Evidence strengths are explicit so missing evidence is never confused with
// an observed zero.
const (
	EvidenceVerified EvidenceStrength = "verified"
	EvidenceStrong   EvidenceStrength = "strong"
	EvidencePartial  EvidenceStrength = "partial"
	EvidenceInferred EvidenceStrength = "inferred"
	EvidenceUnknown  EvidenceStrength = "unknown"
)

// CanonicalTaskIdentity identifies the task and matched condition.
type CanonicalTaskIdentity struct {
	SuiteID      string `json:"suite_id"`
	SuiteVersion string `json:"suite_version"`
	TaskID       string `json:"task_id"`
	TaskVersion  string `json:"task_version"`
	TaskHash     string `json:"task_hash"`
	Condition    string `json:"condition"`
	Trial        int    `json:"trial"`
}

// RunInputIdentity identifies the fully resolved input for one run.
type RunInputIdentity struct {
	TaskHash              string `json:"task_hash"`
	RunInputHash          string `json:"run_input_hash"`
	Condition             string `json:"condition"`
	ProbeHash             string `json:"probe_hash,omitempty"`
	ArmConfigHash         string `json:"arm_config_hash"`
	ModelRequested        string `json:"model_requested,omitempty"`
	GatewayProvider       string `json:"gateway_provider,omitempty"`
	GatewayPolicyHash     string `json:"gateway_policy_hash,omitempty"`
	EnvironmentPolicyHash string `json:"environment_policy_hash,omitempty"`
}

// CanonicalArmIdentity identifies the resolved agent arm.
type CanonicalArmIdentity struct {
	AgentConfigID   string               `json:"agent_config_id"`
	AgentConfigHash string               `json:"agent_config_hash"`
	AdapterVersion  string               `json:"adapter_version,omitempty"`
	AgentVersion    string               `json:"agent_version,omitempty"`
	ImageDigest     string               `json:"image_digest,omitempty"`
	ParserVersion   string               `json:"parser_version,omitempty"`
	ModelRequested  string               `json:"model_requested,omitempty"`
	GatewayProvider string               `json:"gateway_provider,omitempty"`
	GatewayPolicy   GatewayPolicy        `json:"gateway_policy"`
	Budgets         Budgets              `json:"budgets"`
	BudgetSnapshot  *BudgetSnapshot      `json:"budget_snapshot,omitempty"`
	Capabilities    *AdapterCapabilities `json:"capabilities,omitempty"`
}

// BudgetSnapshot keeps configured and effective limits together.
type BudgetSnapshot struct {
	Configured Budgets           `json:"configured"`
	Effective  Budgets           `json:"effective"`
	Source     map[string]string `json:"source,omitempty"`
}

// BudgetParity is the planner's comparison of resolved arm limits.
type BudgetParity string

// Budget parity states.
const (
	BudgetParityEqual                BudgetParity = "equal"
	BudgetParityDeclaredDifference   BudgetParity = "declared_difference"
	BudgetParityUnexpectedDifference BudgetParity = "unexpected_difference"
)

// BudgetParityResult carries the status and the differing fields.
type BudgetParityResult struct {
	Status          BudgetParity `json:"status"`
	Diffs           []BudgetDiff `json:"diffs,omitempty"`
	ConfiguredDiffs []BudgetDiff `json:"configured_diffs,omitempty"`
	EffectiveDiffs  []BudgetDiff `json:"effective_diffs,omitempty"`
}

// MetricEvidence describes the evidence attached to one metric value.
type MetricEvidence struct {
	Name          string           `json:"name"`
	Value         Value            `json:"value"`
	Source        EvidenceOrigin   `json:"source"`
	Level         EvidenceStrength `json:"evidence_level"`
	UnknownReason string           `json:"unknown_reason,omitempty"`
}

// LogicalRequest is one logical model request. Physical attempts belong to it.
type LogicalRequest struct {
	LogicalRequestID string `json:"logical_request_id"`
	LogicalStep      int    `json:"logical_step"`
	RequestID        string `json:"request_id,omitempty"`
	Status           string `json:"status,omitempty"`
}

// AttemptRecord is one physical attempt of a logical model request.
type AttemptRecord struct {
	AttemptID        string `json:"attempt_id"`
	LogicalRequestID string `json:"logical_request_id"`
	AttemptNumber    int    `json:"attempt_number"`
	Reason           string `json:"reason,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	Status           string `json:"status,omitempty"`
	RetryType        string `json:"retry_type,omitempty"`
}

// RetryRecord records a retry without increasing logical steps.
type RetryRecord struct {
	AttemptID        string `json:"attempt_id"`
	LogicalRequestID string `json:"logical_request_id"`
	AttemptNumber    int    `json:"attempt_number"`
	Reason           string `json:"reason,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	Status           string `json:"status,omitempty"`
	RetryType        string `json:"retry_type"`
}

// FallbackRecord records a provider or model fallback.
type FallbackRecord struct {
	FallbackIndex     int    `json:"fallback_index"`
	LogicalRequestID  string `json:"logical_request_id"`
	RequestedProvider string `json:"requested_provider,omitempty"`
	ServedProvider    string `json:"served_provider,omitempty"`
	RequestedModel    string `json:"requested_model,omitempty"`
	ServedModel       string `json:"served_model,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// ToolCallRecord separates a requested tool call from execution proof.
type ToolCallRecord struct {
	ToolCallID      string           `json:"tool_call_id"`
	LogicalStep     int              `json:"logical_step"`
	ToolName        string           `json:"tool_name,omitempty"`
	Requested       bool             `json:"requested"`
	Executed        bool             `json:"executed"`
	RequestSource   EvidenceOrigin   `json:"request_source"`
	ExecutionSource EvidenceOrigin   `json:"execution_source"`
	Evidence        EvidenceStrength `json:"evidence_level"`
	ExecutionID     string           `json:"execution_id,omitempty"`
	UnknownReason   string           `json:"unknown_reason,omitempty"`
}

// ConflictRecord records contradictory observations for one joined fact.
type ConflictRecord struct {
	ConflictType string           `json:"conflict_type"`
	Sources      []EvidenceOrigin `json:"sources"`
	Values       []string         `json:"values"`
	Resolution   string           `json:"resolution"`
	Confidence   EvidenceStrength `json:"confidence"`
}

// EvidenceStatus summarizes evidence coverage and join quality.
type EvidenceStatus struct {
	Level          EvidenceStrength `json:"level"`
	Sources        []EvidenceOrigin `json:"sources,omitempty"`
	UnknownReasons []string         `json:"unknown_reasons,omitempty"`
	Conflicts      []ConflictRecord `json:"conflicts,omitempty"`
}

// CanonicalRun is the framework-neutral record written before sealing.
type CanonicalRun struct {
	CanonicalVersion string                `json:"canonical_version"`
	SchemaVersion    string                `json:"schema_version"`
	GeneratedAt      time.Time             `json:"generated_at"`
	RunID            string                `json:"run_id"`
	Experiment       string                `json:"experiment"`
	Task             CanonicalTaskIdentity `json:"task"`
	RunInput         RunInputIdentity      `json:"run_input"`
	Arm              string                `json:"arm"`
	Agent            CanonicalArmIdentity  `json:"agent"`
	Budgets          BudgetSnapshot        `json:"budgets"`
	ModelRequested   string                `json:"model_requested,omitempty"`
	ModelServed      string                `json:"model_served,omitempty"`
	Events           []Event               `json:"events,omitempty"`
	Grade            *Grade                `json:"grade,omitempty"`
	Gates            []GateResult          `json:"gates,omitempty"`
	Metrics          map[string]Value      `json:"metrics,omitempty"`
	EvidenceStatus   EvidenceStatus        `json:"evidence_status"`
	AgentTrace       *ParseDiagnostics     `json:"agent_trace,omitempty"`
	LogicalSteps     []LogicalRequest      `json:"logical_steps,omitempty"`
	Attempts         []AttemptRecord       `json:"attempts,omitempty"`
	Retries          []RetryRecord         `json:"retries,omitempty"`
	Fallbacks        []FallbackRecord      `json:"fallbacks,omitempty"`
	ToolCalls        []ToolCallRecord      `json:"tool_calls,omitempty"`
}
