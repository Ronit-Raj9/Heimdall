package schemas

import (
	"context"
	"time"
)

// B2 Bifrost features under test (docs/bifrost-features.md). An arm can turn
// on a Bifrost feature that v2.5.1 applies per virtual key or per request, so
// two arms that differ only in gateway.bifrost measure the feature with the
// same tasks, trials, faults, verdict and gates as any other experiment.
// Additive: an agent config without gateway.bifrost hashes as before.

// Prompt cache settings (BifrostFeatures.PromptCache): the value of the
// per-request x-bf-prompt-cache-auto-inject header the agent is asked to
// send. Bifrost honours it only on a provider whose config has a
// prompt_cache block.
const (
	PromptCacheAutoInject = "auto_inject" // header true: Bifrost marks the first cacheable block
	PromptCacheOff        = "off"         // header false: Bifrost leaves the request alone
)

// Semantic cache settings (BifrostSemanticCache).
const (
	SemanticCacheDirect   = "direct"   // exact request hash only, no embedding call
	SemanticCacheSemantic = "semantic" // embedding similarity (needs an embedding provider)

	SemanticCacheScopeTask       = "task"       // one cache key per arm and task: a reply can replay only within the task
	SemanticCacheScopeArm        = "arm"        // one cache key per arm: a reply can replay across the arm's tasks
	SemanticCacheScopeExperiment = "experiment" // one cache key for the experiment: replay across arms and tasks
)

// BifrostFeatures are the Bifrost features an arm turns on for its own runs.
type BifrostFeatures struct {
	// MCP serves the task's MCP servers to the agent through Bifrost's MCP
	// gateway instead of directly: the bench creates one Bifrost MCP client
	// per server per run (in front of the run's tool proxy, which stays the
	// trusted witness), grants it to the run's virtual key only, tells the
	// agent the gateway's per-server endpoint, and removes the client at
	// cleanup.
	MCP *BifrostMCP `json:"mcp,omitempty" yaml:"mcp,omitempty"`
	// PromptCache asks the agent to send x-bf-prompt-cache-auto-inject on
	// every model request: auto_inject or off. Empty sends nothing.
	PromptCache string `json:"prompt_cache,omitempty" yaml:"prompt_cache,omitempty"`
	// SemanticCache asks the agent to send Bifrost's cache headers
	// (x-bf-cache-key, x-bf-cache-type, x-bf-cache-ttl,
	// x-bf-cache-threshold); nil sends none, and with no cache key Bifrost's
	// semantic cache plugin skips the request.
	SemanticCache *BifrostSemanticCache `json:"semantic_cache,omitempty" yaml:"semantic_cache,omitempty"`
	// Prompt makes Bifrost's prompts plugin prepend one Prompt Repository
	// version to every model request of the arm's runs (W4 feature 2); the
	// bench creates a copy of it on the run's gateway per run.
	Prompt *BifrostPrompt `json:"prompt,omitempty" yaml:"prompt,omitempty"`
}

// BifrostMCP is MCP through Bifrost for one arm.
type BifrostMCP struct {
	// CodeMode makes the run's Bifrost MCP clients Code Mode clients: the
	// agent sees listToolFiles, readToolFile, getToolDocs and
	// executeToolCode instead of the servers' own tools.
	CodeMode bool `json:"code_mode" yaml:"code_mode"`
	// Tools are, per service, the tools the run's MCP key may call:
	// "<service id>/<tool>" or "<service id>/*"; empty grants every tool, as
	// before.
	Tools []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	// Forbidden are tools, "<service id>/<tool>", the bench itself calls
	// through Bifrost before the agent starts, expecting a refusal: the
	// filter check; none may be in Tools.
	Forbidden []string `json:"forbidden,omitempty" yaml:"forbidden,omitempty"`
}

// BifrostSemanticCache is the semantic cache for one arm.
type BifrostSemanticCache struct {
	Mode      string  `json:"mode" yaml:"mode"`                               // direct | semantic
	Scope     string  `json:"scope" yaml:"scope"`                             // task | arm | experiment: who shares a cache key
	TTL       string  `json:"ttl,omitempty" yaml:"ttl,omitempty"`             // x-bf-cache-ttl, e.g. 1h
	Threshold float64 `json:"threshold,omitempty" yaml:"threshold,omitempty"` // x-bf-cache-threshold, semantic mode only
}

// BifrostMCPEvidence is what Bifrost's MCP gateway did in one run: the
// clients the bench created for it and the tool executions Bifrost logged
// for the run's virtual key (mcp_tool_logs, GET /api/mcp-logs). The tool
// proxy ledger (Evidence.ToolCalls) stays the trusted witness; these rows
// are the second witness that must agree with it.
type BifrostMCPEvidence struct {
	CodeMode bool               `json:"code_mode"`
	KeyID    string             `json:"key_id"` // the run's MCP key (never its value): the agent's credential on the MCP endpoints, granted the clients only
	Clients  []BifrostMCPClient `json:"clients"`
	Calls    []BifrostMCPCall   `json:"calls"`
	Complete bool               `json:"complete"`         // every page of the run's rows was read
	Reason   string             `json:"reason,omitempty"` // why the rows are incomplete
	// FilterChecks are the bench's own calls of the arm's forbidden tools.
	FilterChecks []BifrostMCPFilterCheck `json:"filter_checks,omitempty"`
}

// BifrostMCPClient is one MCP client the bench created for a run.
type BifrostMCPClient struct {
	Service  string `json:"service"`  // the task's service id
	Name     string `json:"name"`     // the Bifrost client name (server_label in its rows)
	Slug     string `json:"slug"`     // endpoint slug: the agent reached /mcp/<slug>
	Upstream string `json:"upstream"` // where Bifrost reached the tool proxy
	Tools    int    `json:"tools"`    // tools Bifrost discovered on connect
}

// BifrostMCPCall is one tool execution in Bifrost's MCP log.
type BifrostMCPCall struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Service    string    `json:"service"` // the task's service id, from the client name
	Tool       string    `json:"tool"`
	Status     string    `json:"status"`
	ArgsSHA256 string    `json:"args_sha256"` // sha256 of the canonical arguments JSON
	LatencyMS  float64   `json:"latency_ms,omitempty"`
}

// MCPUpstream is one task MCP server as Bifrost must reach it: the run's tool
// proxy route for the service, on an address the gateway can resolve.
type MCPUpstream struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

// BifrostMCPGateway is implemented by gateways that can serve a run's MCP
// servers to its agent (gateways/bifrost/bifrostmcp.go).
type BifrostMCPGateway interface {
	ServeMCP(ctx context.Context, rc *RunContext, ups []MCPUpstream, codeMode bool) ([]BifrostMCPClient, *RunKey, error)
	RemoveMCP(ctx context.Context, runID string) error
	MCPCalls(ctx context.Context, keyID string, clients []BifrostMCPClient) ([]BifrostMCPCall, bool, string)
}

// BifrostMCPEnvironment is implemented by environments that can put a run's
// tool proxy where the gateway reaches it and point the agent at the
// gateway's MCP endpoints (environments/bifrostmcp.go). ServeMCP runs after
// the services started and the run's key was minted; RemoveMCP runs first at
// cleanup.
type BifrostMCPEnvironment interface {
	ServeMCP(ctx context.Context, rc *RunContext, codeMode bool) (*BifrostMCPEvidence, error)
	RemoveMCP(ctx context.Context, rc *RunContext) error
	CollectMCP(ctx context.Context, rc *RunContext, ev *BifrostMCPEvidence, ledger []ToolCall)
}

// BifrostPrompt is one Prompt Repository version under test, copied into the
// arm when it was picked, so the run never depends on the source repository.
type BifrostPrompt struct {
	Name        string           `json:"name" yaml:"name"`                               // the prompt's name in the source repository
	Version     int              `json:"version" yaml:"version"`                         // its version_number there
	SourceID    string           `json:"source_id,omitempty" yaml:"source_id,omitempty"` // the prompt id in the source repository
	Messages    []map[string]any `json:"messages" yaml:"messages"`                       // the version's messages[].message, in order_index order
	ModelParams map[string]any   `json:"model_params,omitempty" yaml:"model_params,omitempty"`
	SHA256      string           `json:"sha256" yaml:"sha256"` // hex sha256 of utils.HashJSON-canonical {"messages": Messages, "model_params": ModelParams}
}

// BifrostMCPFilterCheck is the bench's own call of one forbidden tool through
// Bifrost, made with the run's MCP key before the agent started.
type BifrostMCPFilterCheck struct {
	Service string `json:"service"`
	Tool    string `json:"tool"`
	Listed  bool   `json:"listed"`  // tools/list through Bifrost showed the tool
	Refused bool   `json:"refused"` // tools/call answered a JSON-RPC error or isError
	Marker  string `json:"marker"`  // the value of the call's only argument, heimdall_filter_check; the tool proxy ledger shows it only if the call got through
	Detail  string `json:"detail,omitempty"`
}
