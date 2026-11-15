package schemas

import "context"

// AdapterMetadata is the optional metadata contract for an agent adapter.
// The existing Part methods remain valid for adapters that do not implement it.
type AdapterMetadata interface {
	Part
	Metadata() AdapterMetadataInfo
}

// AdapterValidator checks an adapter and its resolved run before execution.
type AdapterValidator interface {
	Part
	Validate(ctx context.Context, rc *RunContext) error
}

// AdapterRunner builds an execution plan. Process lifecycle remains owned by
// Environment, which enforces isolation, limits, timeouts, and cleanup.
type AdapterRunner interface {
	Part
	BuildExecutionPlan(ctx context.Context, rc *RunContext) (*ExecutionPlan, error)
}

// AdapterEventSource converts adapter output into normalized events when the
// adapter can provide structured event data.
type AdapterEventSource interface {
	Part
	CollectEvents(ctx context.Context, rc *RunContext, raw []byte) ([]Event, error)
}

// AdapterResultParser converts adapter output into a common result and claim.
type AdapterResultParser interface {
	Part
	ParseExecutionOutput(ctx context.Context, rc *RunContext, raw []byte) (*ParseResult, error)
}

// AdapterCleanup removes adapter-owned temporary files after a run.
type AdapterCleanup interface {
	Part
	Cleanup(ctx context.Context, rc *RunContext) error
}

// AdapterMetadataInfo identifies the adapter and the harness it starts. Empty
// values mean that the current adapter cannot observe that value.
type AdapterMetadataInfo struct {
	AdapterVersion string `json:"adapter_version" yaml:"adapter_version"`
	AgentVersion   string `json:"agent_version,omitempty" yaml:"agent_version,omitempty"`
	ImageDigest    string `json:"image_digest,omitempty" yaml:"image_digest,omitempty"`
	ParserVersion  string `json:"parser_version,omitempty" yaml:"parser_version,omitempty"`
	ConfigHash     string `json:"config_hash,omitempty" yaml:"config_hash,omitempty"`
}

// AdapterCapabilities declares optional adapter support. A false value or an
// empty list means that support is not declared; it does not change legacy
// adapter behavior.
type AdapterCapabilities struct {
	FaultProtocolVersion     string             `json:"fault_protocol_version,omitempty" yaml:"fault_protocol_version,omitempty"`
	FaultHooks               []AdapterFaultHook `json:"fault_hooks,omitempty" yaml:"fault_hooks,omitempty"`
	SupportsStreaming        bool               `json:"supports_streaming,omitempty" yaml:"supports_streaming,omitempty"`
	SupportsStructuredEvents bool               `json:"supports_structured_events,omitempty" yaml:"supports_structured_events,omitempty"`
	SupportsToolEvents       bool               `json:"supports_tool_events,omitempty" yaml:"supports_tool_events,omitempty"`
	SupportsNotesFile        bool               `json:"supports_notes_file,omitempty" yaml:"supports_notes_file,omitempty"`
	SupportsModelOverride    bool               `json:"supports_model_override,omitempty" yaml:"supports_model_override,omitempty"`
	SupportsCancellation     bool               `json:"supports_cancellation,omitempty" yaml:"supports_cancellation,omitempty"`
	SupportsResume           bool               `json:"supports_resume,omitempty" yaml:"supports_resume,omitempty"`
	SupportsGatewayHeaders   bool               `json:"supports_gateway_headers,omitempty" yaml:"supports_gateway_headers,omitempty"`
	TaskTypes                []string           `json:"task_types,omitempty" yaml:"task_types,omitempty"`
	EvidenceLevels           []EvidenceLevel    `json:"evidence_levels,omitempty" yaml:"evidence_levels,omitempty"`
}

// ExecutionPlan is the additive name for the existing RunPlan. The alias
// keeps old Environment implementations and serialized plans compatible.
type ExecutionPlan = RunPlan
