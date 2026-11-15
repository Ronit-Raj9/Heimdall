package schemas

// Agent-reported events (docs/agent-events.md): steps an agent reports about
// itself through the run's event collector (OTLP spans or the native event
// protocol), an events file in the aux volume, or prefixed stdout lines.
// They are the agent's own word: shown and counted separately, never able to
// raise a trusted metric or decide an outcome.
const (
	// AgentEventsFile holds the normalized agent-reported events of a run,
	// one Event per line, in the run folder.
	AgentEventsFile = "agent_events.jsonl"
	// AgentEventsStatusFile holds the run's AgentEventStatus.
	AgentEventsStatusFile = "agent_events.json"
	// AgentEventsStdoutPrefix starts a stdout line that carries one native
	// event as JSON, for agents that can only print.
	AgentEventsStdoutPrefix = "@@heimdall-event "

	// StepSourceReported is the timeline source of agent-reported events.
	StepSourceReported = "agent_reported"

	// SourceAgentReported is the event source of agent-reported events.
	SourceAgentReported EventSource = "agent_reported"
	// EvidenceSourceAgentReported is the evidence origin of agent-reported
	// events.
	EvidenceSourceAgentReported EvidenceOrigin = "agent_reported"
	// EventAgentStep is an agent-reported internal step that is not a tool
	// call or result (a plan, a subagent run, a handoff, a memory operation);
	// Event.ReportedType names which.
	EventAgentStep EventType = "agent_step"
)

// Channels an agent-reported event arrived on.
const (
	ReportChannelOTLP   = "otlp"   // OTLP/HTTP traces to the collector
	ReportChannelNative = "native" // POST /events to the collector
	ReportChannelFile   = "file"   // lines appended to BENCH_EVENTS_FILE
	ReportChannelStdout = "stdout" // prefixed stdout lines
)

// Corroboration of an agent-reported timeline row (StepRecord.Corroboration).
const (
	Corroborated      = "corroborated"        // a trusted witness saw the same action
	ReportedOnly      = "agent_reported_only" // only the agent says it happened
	ReportedConflicts = "conflicts"           // a trusted witness saw the action and disagrees with the report
)

// AgentEventStatus says what the run's agent reported about itself and what
// was kept. Collector is "on" when the event collector ran for the run, "off"
// when it was switched off or not applicable, "missing" when it was on but
// could not run. Dropped counts events refused at a cap or because they did
// not parse; Received counts every event seen, dropped ones included.
type AgentEventStatus struct {
	Collector string         `json:"collector"`
	Reason    string         `json:"reason,omitempty"`
	Received  int            `json:"received"`
	Kept      int            `json:"kept"`
	Dropped   int            `json:"dropped,omitempty"`
	Channels  map[string]int `json:"channels,omitempty"` // kept events per channel
	Kinds     map[string]int `json:"kinds,omitempty"`    // kept events per reported type
}
