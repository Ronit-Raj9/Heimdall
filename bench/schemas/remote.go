package schemas

// Remote coverage (docs/remote-execution.md). A remote agent is a deployed
// service the bench does not control. Three optional channels make its run
// measurable, each with its own evidence strength:
//
//   - model traffic through this gateway (remote.model_traffic gateway):
//     tokens, cost and model turns are trusted gateway evidence;
//   - tools through this bench (remote.tools expose): the run's tool proxy is
//     published behind a per-run bearer token, so every tool call lands in
//     the trusted tool ledger and state tasks can run;
//   - self-reported telemetry in the agent's response (usage, events,
//     artifacts, version): agent_reported, kept apart from trusted metrics,
//     never deciding an outcome.
//
// What none of them can show: the deployment's own local tools, files and
// internal steps, and whether it is the version its owner claims.

// Values of RemoteAgent.Tools and RemoteAgent.Artifacts.
const (
	RemoteToolsExpose     = "expose"
	RemoteToolsOff        = "off"
	RemoteArtifactsInline = "inline"
)

// Remote run folder records.
const (
	// RemoteReportType is the "type" of the launcher's stdout line that
	// carries what the deployment reported about itself.
	RemoteReportType = "remote_report"
	// RemoteToolsFile records a published tool proxy in the run folder
	// (address and expiry; never the token).
	RemoteToolsFile = "remote_tools.json"
)

// Channel states in RemoteChannels.
const (
	RemoteChannelOn      = "on"
	RemoteChannelOff     = "off"
	RemoteChannelMissing = "missing" // on in config, but nothing arrived
)

// RemoteToolsAccess is a published tool proxy of one run: where a remote
// agent reaches it and the bearer token it must present. Only the engine
// holds it, in memory; it dies with the run.
type RemoteToolsAccess struct {
	BaseURL  string   // public base URL, without the /r/<run id> prefix
	RunID    string   // the run the gate serves; every path starts /r/<run id>/
	Token    string   // per-run bearer token, 32 random bytes, hex
	Services []string // proxied service ids reachable at /s/<id>/
	MCP      []string // the subset that are MCP services (/s/<id>/<mcp path>)
	MCPPaths map[string]string
	External []string // external API ids reachable at /x/<id>/
}

// RemoteToolsRecord is remote_tools.json in the run folder: what was
// published and until when. It never holds the token.
type RemoteToolsRecord struct {
	Listen   string   `json:"listen"`   // bench-host address the gate was published on
	BaseURL  string   `json:"base_url"` // what the deployment was told
	Gate     string   `json:"gate"`     // gate container and its network
	Services []string `json:"services,omitempty"`
	External []string `json:"external,omitempty"`
	Until    string   `json:"until"` // RFC 3339; the gate refuses every call after it
}

// RemoteEvidence says which channels of a remote agent's run were active and
// what is therefore trusted, agent-reported or unknown.
type RemoteEvidence struct {
	Channels      RemoteChannels `json:"channels"`
	Trusted       []string       `json:"trusted,omitempty"`        // measurements a bench witness backs
	AgentReported []string       `json:"agent_reported,omitempty"` // measurements only the agent's word backs
	Unknown       []string       `json:"unknown,omitempty"`        // measurements nothing backs
	Reported      *RemoteReport  `json:"reported,omitempty"`
}

// RemoteChannels is the state of each channel: on, off or missing.
type RemoteChannels struct {
	ModelTraffic string `json:"model_traffic"` // on: gateway; off: external
	Tools        string `json:"tools"`
	SelfReport   string `json:"self_report"`
}

// RemoteReport is what the deployment said about itself in its response.
// Every field is the agent's own word (agent_reported): shown, counted in
// reported_* metrics, never in tokens or cost_usd, never in an outcome.
// Strings are capped and redacted by the launcher and again on read.
type RemoteReport struct {
	InputTokens  *int64   `json:"input_tokens,omitempty"`
	OutputTokens *int64   `json:"output_tokens,omitempty"`
	TotalTokens  *int64   `json:"total_tokens,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
	Model        string   `json:"model,omitempty"`
	Version      string   `json:"version,omitempty"` // self-declared
	Build        string   `json:"build,omitempty"`   // self-declared
	Events       int      `json:"events,omitempty"`  // events passed on as agent-reported events
	Dropped      int      `json:"events_dropped,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"` // workspace paths written from the response
	Async        bool     `json:"async,omitempty"`     // the answer came through status polling
	Notes        []string `json:"notes,omitempty"`     // what was refused or capped
}
