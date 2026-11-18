package schemas

import (
	"context"
	"io"
	"time"
)

// Agent event protocol identifiers. Agents may emit these records as NDJSON
// on stdout; the bench stamps run identity and normalizes them into Event.
const (
	AgentEventSchema  = "bifrost.agent.event"
	AgentEventVersion = "1"
)

// TraceSource identifies the source and parser used for agent-stream detail.
type TraceSource string

const (
	TraceSourceProtocol TraceSource = "protocol"
	TraceSourceNative   TraceSource = "native_parser"
	TraceSourceGeneric  TraceSource = "generic_parser"
	TraceSourceTail     TraceSource = "stdout_tail"
	TraceSourceNone     TraceSource = "none"
)

// TraceQuality records how much structured agent-stream evidence was usable.
type TraceQuality string

const (
	TraceQualityComplete    TraceQuality = "complete"
	TraceQualityPartial     TraceQuality = "partial"
	TraceQualityDegraded    TraceQuality = "degraded"
	TraceQualityClaimOnly   TraceQuality = "claim_only"
	TraceQualityUnavailable TraceQuality = "unavailable"
)

// ParseDiagnostics describes parser coverage without turning malformed agent
// output into a failed benchmark run.
type ParseDiagnostics struct {
	Source        TraceSource  `json:"source,omitempty"`
	Quality       TraceQuality `json:"quality,omitempty"`
	Parser        string       `json:"parser,omitempty"`
	ParserVersion string       `json:"parser_version,omitempty"`
	Bytes         int64        `json:"bytes,omitempty"`
	Lines         int          `json:"lines,omitempty"`
	Recognized    int          `json:"recognized,omitempty"`
	Emitted       int          `json:"emitted,omitempty"`
	Malformed     int          `json:"malformed,omitempty"`
	Oversized     int          `json:"oversized,omitempty"`
	Unrecognized  int          `json:"unrecognized,omitempty"`
	Truncated     bool         `json:"truncated,omitempty"`
	Reason        string       `json:"reason,omitempty"`
}

// AgentEventRecord is the framework-neutral line protocol. Identity fields
// such as run_id and event_id are assigned by the collector, not the agent.
type AgentEventRecord struct {
	Schema        string      `json:"schema"`
	Version       string      `json:"version"`
	Type          EventType   `json:"type"`
	Timestamp     time.Time   `json:"timestamp,omitzero"`
	ID            string      `json:"id,omitempty"`
	ToolCallID    string      `json:"tool_call_id,omitempty"`
	ParentEventID string      `json:"parent_event_id,omitempty"`
	ToolName      string      `json:"tool,omitempty"`
	Target        string      `json:"target,omitempty"`
	ArgsSHA256    string      `json:"args_sha256,omitempty"`
	ResultSHA256  string      `json:"result_sha256,omitempty"`
	Summary       string      `json:"summary,omitempty"`
	Status        EventStatus `json:"status,omitempty"`
	IsError       bool        `json:"is_error,omitempty"`
	Tokens        *Tokens     `json:"tokens,omitempty"`
	Claim         *Claim      `json:"claim,omitempty"`
}

// AgentConfig is the system under test, read from agents/<id>.yaml and then
// modified by an arm's `set:` overrides. Its content hash identifies it.
type AgentConfig struct {
	ID           string               `json:"id" yaml:"id"`
	Adapter      string               `json:"adapter" yaml:"adapter"` // command | scripted
	Image        string               `json:"image" yaml:"image"`
	Build        *ImageSpec           `json:"build,omitempty" yaml:"build"` // optional: how to build Image
	Argv         []string             `json:"argv" yaml:"argv"`
	Env          map[string]string    `json:"env" yaml:"env"`
	Files        map[string]string    `json:"files,omitempty" yaml:"files"` // container path -> content template
	Model        string               `json:"model" yaml:"model"`
	Notes        AgentNotes           `json:"notes" yaml:"notes"`
	Parser       string               `json:"parser,omitempty" yaml:"parser"` // empty = gateway-level evidence only
	AgentVersion string               `json:"agent_version,omitempty" yaml:"agent_version,omitempty"`
	ImageDigest  string               `json:"image_digest,omitempty" yaml:"image_digest,omitempty"`
	Capabilities *AdapterCapabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Budgets      Budgets              `json:"budgets" yaml:"budgets"`
	Gateway      GatewayPolicy        `json:"gateway" yaml:"gateway"`
	// Scripted is read only by the scripted adapter.
	Scripted *ScriptedSpec `json:"scripted,omitempty" yaml:"scripted"`

	// Scenario fields (scenario.go). All optional.
	MCP    *AgentMCP    `json:"mcp,omitempty" yaml:"mcp"`       // how the agent is told about MCP servers
	Skills *AgentSkills `json:"skills,omitempty" yaml:"skills"` // agent skills and where they are installed
	Memory *AgentMemory `json:"memory,omitempty" yaml:"memory"` // paths kept between sessions
	Remote *RemoteAgent `json:"remote,omitempty" yaml:"remote"` // read only by the remote_http adapter

	// Multi-agent fields (agents.go, docs/multi-agent.md). Team runs one
	// container per member, each on its own gateway key, under the run-wide
	// cap Budgets.USD; Topology labels the arm for topology comparisons.
	Team     []TeamMember `json:"team,omitempty" yaml:"team"`
	Topology string       `json:"topology,omitempty" yaml:"topology"` // single | best_of_n | orchestrator_worker | team | swarm
	Limits   *AgentLimits `json:"limits,omitempty" yaml:"limits"`     // spawn caps, frozen in the manifest; the over_spawn gate compares the tree with them
}

// AgentNotes says which notes files the agent reads (DESIGN.md 8.6).
type AgentNotes struct {
	ProjectFile string `json:"project_file" yaml:"project_file"`     // repo file the agent reads; the stale-notes probe overwrites it
	UserFile    string `json:"user_file,omitempty" yaml:"user_file"` // optional host file installed as user-level instructions
	UserPath    string `json:"user_path,omitempty" yaml:"user_path"` // where UserFile is installed, outside the workspace
}

// Budgets are identical across arms unless they are the declared variable.
type Budgets struct {
	WallS        int     `json:"wall_s" yaml:"wall_s"`
	Turns        int     `json:"turns,omitempty" yaml:"turns"`
	CPUs         float64 `json:"cpus" yaml:"cpus"`
	MemGB        float64 `json:"mem_gb" yaml:"mem_gb"`
	PidsLimit    int     `json:"pids_limit,omitempty" yaml:"pids_limit"`
	USD          float64 `json:"usd,omitempty" yaml:"usd"` // per-run virtual key budget, a backstop only
	MaxRetries   int     `json:"max_retries,omitempty" yaml:"max_retries"`
	MaxToolCalls int     `json:"max_tool_calls,omitempty" yaml:"max_tool_calls"`
}

// GatewayPolicy is how the run's virtual key routes. A fallback is allowed only
// when declared here.
type GatewayPolicy struct {
	Provider   string   `json:"provider" yaml:"provider"`       // e.g. anthropic
	MaxRetries int      `json:"max_retries" yaml:"max_retries"` // provider retries recorded in the manifest
	Fallbacks  []string `json:"fallbacks,omitempty" yaml:"fallbacks"`
}

// ScriptedSpec selects a scripted control agent.
type ScriptedSpec struct {
	Kind string `json:"kind" yaml:"kind"` // oracle | noop | exploit | refuse | toy
}

// AgentAdapter turns an agent config into a container run plan. It starts
// nothing itself; the Environment executes the plan.
type AgentAdapter interface {
	Part
	Plan(ctx context.Context, rc *RunContext) (*RunPlan, error)
}

// RunPlan is a fully resolved container invocation for one run.
type RunPlan struct {
	Image        string               `json:"image"`
	Argv         []string             `json:"argv"`
	Env          map[string]string    `json:"env"`   // secrets are redacted when the plan is written to disk
	Files        map[string]string    `json:"files"` // container path -> resolved content
	Workdir      string               `json:"workdir"`
	WallS        int                  `json:"wall_s"`
	CPUs         float64              `json:"cpus"`
	MemGB        float64              `json:"mem_gb"`
	Pids         int                  `json:"pids"`
	Network      string               `json:"network"` // the internal evaluation network, or none
	User         string               `json:"user"`
	Capabilities *AdapterCapabilities `json:"capabilities,omitempty"`
	// Dirs maps a container path to a host folder copied into the container,
	// read-only for the agent, before it starts. It carries what is too large
	// for Files, such as agent skills.
	Dirs map[string]string `json:"dirs,omitempty"`
	// Egress lists host:port destinations outside the evaluation network the
	// container may reach, each through a per-run forwarder that connects
	// only there (utils/remoteegress.go). Only the remote_http adapter sets
	// it; the run records it and the egress gate reports it.
	Egress []string `json:"egress,omitempty"`
}

// Parser converts an agent's raw output into normalized events and its final
// claim. It is pure and optional.
type Parser interface {
	Part
	Parse(ctx context.Context, rc *RunContext, raw []byte) (*ParseResult, error)
}

// ReaderParser is an optional streaming extension. Legacy Parser and
// AdapterResultParser implementations keep their byte-slice entry points.
type ReaderParser interface {
	Parser
	ParseReader(ctx context.Context, rc *RunContext, raw io.Reader) (*ParseResult, error)
}

// ParseResult is what a parser extracts from an agent's raw output.
type ParseResult struct {
	Events      []Event           `json:"events"`
	Claim       *Claim            `json:"claim,omitempty"`
	Diagnostics *ParseDiagnostics `json:"diagnostics,omitempty"`
}

// Claim is the agent's statement about its own result.
type Claim struct {
	Made    bool          `json:"made"`              // a success claim was made
	Text    string        `json:"text,omitempty"`    // final message excerpt
	Source  EvidenceLevel `json:"source"`            // native, or outcome when read from stdout's tail
	Matched string        `json:"matched,omitempty"` // the phrase that counted as a claim
}
