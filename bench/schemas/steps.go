package schemas

import "time"

// Run folder files of the step timeline and the trusted observers
// (docs/steps-and-evidence.md). A multi-session run also writes
// processes.<n>.jsonl and fsevents.<n>.jsonl per session (n 1-based); the
// unindexed files hold every session's records in session order.
const (
	StepsFile      = "steps.jsonl"     // the unified, ordered step timeline
	ProcessesFile  = "processes.jsonl" // one ProcessRecord per line
	FSEventsFile   = "fsevents.jsonl"  // one FileEvent per line
	ObserversFile  = "observers.json"  // []ObserverStatus, one per observer and session
	ObserverProc   = "process"         // ObserverStatus.Observer of the process observer
	ObserverFS     = "workspace"       // ObserverStatus.Observer of the workspace observer
	ObserverOn     = "observed"        // the observer ran for the whole agent session
	ObserverOff    = "off"             // switched off in config, or not applicable to the agent
	ObserverFailed = "missing"         // the observer was on but could not run or stopped early
)

// Step sources: the independent witnesses a timeline row came from.
const (
	StepSourceGateway   = "gateway"            // Bifrost request log (trusted)
	StepSourceToolProxy = "tool_proxy"         // per-run tool proxy ledger (trusted)
	StepSourceProcess   = "process_observer"   // process observer beside the agent (trusted)
	StepSourceFS        = "workspace_observer" // workspace activity observer (trusted)
	StepSourceAgent     = "agent_stream"       // the agent's own stream (agent-reported)
	StepSourceShim      = "shim"               // tool shim log (boundary)
	StepSourceNetwork   = "network_observer"   // passive network observer (trusted)
)

// Step kinds.
const (
	StepModelTurn     = "model_turn"     // one logical model request the model answered
	StepModelAttempt  = "model_attempt"  // one physical attempt: a retry or a fallback row
	StepToolRequest   = "tool_request"   // a tool call the model requested
	StepToolExecution = "tool_execution" // a proxied tool call, a shim call or an agent-reported tool result
	StepCommand       = "command"        // a process the agent started
	StepFileChange    = "file_change"    // a file created, modified, deleted or renamed in the workspace
	StepNetwork       = "network"        // a connection or request the network observer saw
	StepAgentEvent    = "agent_event"    // another agent-reported event
)

// Execution states of a requested tool call (ToolCallRecord.Proof and
// StepRecord.Execution).
const (
	ExecutedWithProof     = "executed_with_proof"     // a trusted witness saw it run
	ExecutedAgentReported = "executed_agent_reported" // only the agent's own stream says it ran
	ExecutionNoWitness    = "no_witness"              // nothing shows it ran
)

// ProcessRecord is one process the process observer saw in the agent
// container's PID namespace. Start comes from the kernel's start time, so it
// is exact to the clock tick even when the observer found the process late.
// End is the first poll that no longer saw the process, so it is an upper
// bound. Argv is capped and has secrets redacted.
type ProcessRecord struct {
	PID           int       `json:"pid"`
	PPID          int       `json:"ppid"`
	Session       int       `json:"session,omitempty"`
	UID           int       `json:"uid"`
	Comm          string    `json:"comm,omitempty"`
	Argv          []string  `json:"argv,omitempty"`
	ArgvTruncated bool      `json:"argv_truncated,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end,omitzero"`
	ExitCode      *int      `json:"exit_code,omitempty"`
	RunningAtEnd  bool      `json:"running_at_end,omitempty"` // still running when the session ended
}

// FileEvent is one change the workspace observer saw on the workspace
// volume. Path is relative to the workspace root; From is the old path of a
// rename. Size is the size after the change when it could be read.
type FileEvent struct {
	TS      time.Time `json:"ts"`
	Op      string    `json:"op"` // create | modify | delete | rename
	Path    string    `json:"path"`
	From    string    `json:"from,omitempty"`
	Size    *int64    `json:"size,omitempty"`
	Dir     bool      `json:"dir,omitempty"`
	Session int       `json:"session,omitempty"`
}

// File event operations.
const (
	FileCreate = "create"
	FileModify = "modify"
	FileDelete = "delete"
	FileRename = "rename"
)

// ObserverStatus says whether one observer ran for one session, how, and
// what it could not see. A missing observer is evidence marked missing with
// a reason, never zero activity.
type ObserverStatus struct {
	Observer   string `json:"observer"` // process | workspace
	Session    int    `json:"session,omitempty"`
	State      string `json:"state"` // observed | off | missing
	Reason     string `json:"reason,omitempty"`
	Mode       string `json:"mode,omitempty"`        // proc_poll | inotify
	IntervalMS int    `json:"interval_ms,omitempty"` // process observer poll interval
	Coverage   string `json:"observer_coverage,omitempty"`
	Records    int    `json:"records"`
	Dropped    int    `json:"dropped,omitempty"`
	// Process observer: PIDs the kernel handed out in the agent's namespace
	// since it was created (ns_last_pid, so the moments before the observer
	// joined are covered too), and how many of them it saw as a process or a
	// thread. The difference bounds the processes it missed.
	PIDsAllocated int `json:"pids_allocated,omitempty"`
	PIDsSeen      int `json:"pids_seen,omitempty"`
}

// StepRecord is one row of the unified step timeline (steps.jsonl): one
// observation by one witness, ordered by time. Turn is the logical model
// step whose response came before the row (0 before the first response).
// Counted marks the rows that make up agent_steps; a row seen by a second
// witness links to the counted row instead (WitnessOf) and is not counted
// again. Every string is inert display text.
type StepRecord struct {
	Seq         int              `json:"seq"`
	TS          time.Time        `json:"ts"`
	Kind        string           `json:"kind"`
	Source      string           `json:"source"`
	Proof       EvidenceStrength `json:"proof"`
	Turn        int              `json:"turn"`
	Session     int              `json:"session,omitempty"`
	Summary     string           `json:"summary"`
	RequestID   string           `json:"request_id,omitempty"`
	Attempt     int              `json:"attempt,omitempty"`
	ToolCallID  string           `json:"tool_call_id,omitempty"`
	ToolName    string           `json:"tool_name,omitempty"`
	PID         int              `json:"pid,omitempty"`
	PPID        int              `json:"ppid,omitempty"`
	Path        string           `json:"path,omitempty"`
	Op          string           `json:"op,omitempty"`
	ExitCode    *int             `json:"exit_code,omitempty"`
	DurationMS  *int64           `json:"duration_ms,omitempty"`
	IsError     bool             `json:"is_error,omitempty"`
	Execution   string           `json:"execution,omitempty"`    // tool_request rows: executed_with_proof | executed_agent_reported | no_witness
	WitnessedBy []int            `json:"witnessed_by,omitempty"` // tool_request rows: seq of the rows that witness its execution
	WitnessOf   int              `json:"witness_of,omitempty"`   // seq of the counted row this row witnesses
	Counted     bool             `json:"counted,omitempty"`
	Conflict    string           `json:"conflict,omitempty"` // witnesses disagree about this row
	// Agent-reported rows (agentevents.go, docs/agent-events.md).
	EventType     string `json:"event_type,omitempty"`    // the reported type: plan, tool_call, subagent_start, ...
	Channel       string `json:"channel,omitempty"`       // otlp | native | file | stdout
	ReportID      string `json:"report_id,omitempty"`     // the agent's own id for the event
	ParentSeq     int    `json:"parent_seq,omitempty"`    // seq of the reported parent row (nesting)
	Corroboration string `json:"corroboration,omitempty"` // corroborated | agent_reported_only | conflicts
	// ProofClass is the strongest witness of a tool_request row's execution
	// (ProofProxy and the other proof classes below).
	ProofClass string `json:"proof_class,omitempty"`
	// AgentID is the agent of a multi-agent run the row belongs to, and
	// AgentSource how that is known (agents.go: gateway_key, header,
	// transcript, otlp, self_report). Both are empty in a single-agent run
	// and on a row no source could place.
	AgentID     string `json:"agent_id,omitempty"`
	AgentSource string `json:"agent_source,omitempty"`
}

// Proof classes of a requested tool call: the strongest witness of its
// execution, in this order. Every call has exactly one class. The first three
// are trusted: the bench observed them outside the agent's container. A shim
// call is recorded inside the agent's container. ProofResultReturned only
// proves that a result for the call reached the model (a later model request
// carried it, or the agent's stream reported one), not that the tool ran.
const (
	ProofProxy          = "proxy_witnessed"       // a tool proxy ledger row
	ProofProcess        = "process_witnessed"     // a command tree the process observer saw
	ProofFileEffect     = "file_effect_witnessed" // a workspace change on the requested path
	ProofShim           = "shim_witnessed"        // a tool shim call with the same tool-call id
	ProofResultReturned = "result_returned"       // a result reached the model; nothing shows the tool ran
	ProofNone           = "no_witness"            // nothing at all
)
