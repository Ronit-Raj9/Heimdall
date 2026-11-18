package schemas

import "time"

// EventSchemaVersion is the version of the additive common event fields.
const EventSchemaVersion = "1.0.0"

// EventStatus describes the observed state of an event.
type EventStatus string

const (
	EventStatusUnknown EventStatus = "unknown"
	EventStatusStarted EventStatus = "started"
	EventStatusSuccess EventStatus = "success"
	EventStatusError   EventStatus = "error"
)

// EventType classifies a normalized event.
type EventType string

// Event types.
const (
	EventModelRequest  EventType = "model_request"
	EventModelResponse EventType = "model_response"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventRetry         EventType = "retry"
	EventFallback      EventType = "fallback"
	EventFault         EventType = "fault"
	EventClaim         EventType = "claim"
	EventGrade         EventType = "grade"
	EventGate          EventType = "gate"
	EventCompaction    EventType = "compaction"
	EventError         EventType = "error"
	EventIntegrity     EventType = "integrity"
)

// EventSource names the record an event came from.
type EventSource string

// Event sources.
const (
	SourceGateway      EventSource = "gateway"
	SourceProxyLedger  EventSource = "proxy_ledger"
	SourceAgentStream  EventSource = "agent_stream"
	SourceShimLog      EventSource = "shim_log"
	SourceGrader       EventSource = "grader"
	SourceEngine       EventSource = "engine"
	SourceToolRunner   EventSource = "tool_runner"
	SourceWorkspace    EventSource = "workspace_diff"
	SourceProcess      EventSource = "process_result"
	SourceAdapterFault EventSource = "adapter_fault_controller"
)

// Tokens holds four disjoint token buckets.
type Tokens struct {
	Fresh      int64 `json:"fresh"` // input tokens neither read from nor written to cache
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Out        int64 `json:"out"` // output, including reasoning
}

// Event is one normalized event of a run (DESIGN.md 16.4). Event ids are
// "<run_id>:<source>:<seq>" so independent sources never collide.
type Event struct {
	EventVersion     string           `json:"event_version,omitempty"`
	EventID          string           `json:"event_id"`
	RunID            string           `json:"run_id"`
	SlotID           string           `json:"slot_id"`
	PairID           string           `json:"pair_id"`
	Seq              int              `json:"seq"` // per source
	Sequence         int              `json:"sequence,omitempty"`
	TurnIndex        int              `json:"turn_index"`
	TS               time.Time        `json:"ts"`
	Timestamp        time.Time        `json:"timestamp,omitzero"`
	Type             EventType        `json:"type"`
	Source           EventSource      `json:"source"`
	Evidence         EvidenceLevel    `json:"evidence"`
	EvidenceLevel    EvidenceLevel    `json:"evidence_level,omitempty"`
	EvidenceSource   EvidenceOrigin   `json:"evidence_source,omitempty"`
	EvidenceStrength EvidenceStrength `json:"evidence_strength,omitempty"`
	Status           EventStatus      `json:"status,omitempty"`
	ParserVersion    string           `json:"parser_version,omitempty"`
	RequestID        string           `json:"request_id,omitempty"` // Bifrost row id
	LogicalStepID    string           `json:"logical_step_id,omitempty"`
	AttemptID        string           `json:"attempt_id,omitempty"`
	AttemptNo        *int             `json:"attempt_no,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ParentEventID    string           `json:"parent_event_id,omitempty"`
	ToolName         string           `json:"tool_name,omitempty"`
	Target           string           `json:"target,omitempty"` // file path, command or url the action is about; used to align A and B
	ArgsHash         string           `json:"args_hash,omitempty"`
	ResultHash       string           `json:"result_hash,omitempty"`
	DurationMS       *int64           `json:"duration_ms,omitempty"`
	ExitStatus       *int             `json:"exit_status,omitempty"`
	IsError          bool             `json:"is_error,omitempty"`
	Provider         string           `json:"provider,omitempty"`
	ModelReq         string           `json:"model_requested,omitempty"`
	ModelServed      string           `json:"model_served,omitempty"`
	Tokens           *Tokens          `json:"tokens,omitempty"`
	USD              *Value           `json:"usd,omitempty"`
	Summary          string           `json:"summary,omitempty"` // short inert text for display
	// Agent-reported events (agentevents.go): the type the agent named, its
	// own ids and the channel the event arrived on. All agent-chosen text.
	ReportedType   string `json:"reported_type,omitempty"`
	ReportedID     string `json:"reported_id,omitempty"`
	ReportedParent string `json:"reported_parent,omitempty"`
	Channel        string `json:"channel,omitempty"`
	// Multi-agent attribution (agents.go). AgentID is the agent the event
	// belongs to or, for an agent span or subagent_start, the agent it
	// names; AgentName and AgentRole describe that agent; AgentTarget is the
	// receiving agent of a handoff. Set from a reported event's attributes
	// (gen_ai.agent.id, gen_ai.agent.name, agent_id, from_agent, to_agent,
	// ...) or from a transcript (a Claude Code subagent line). Empty
	// otherwise.
	AgentID     string `json:"agent_id,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
	AgentRole   string `json:"agent_role,omitempty"`
	AgentTarget string `json:"agent_target,omitempty"`
}
