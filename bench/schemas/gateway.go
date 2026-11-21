package schemas

import (
	"context"
	"encoding/json"
	"time"
)

// Gateway is everything the bench needs from an LLM gateway. The bifrost
// package implements it against a stock Bifrost over HTTP.
type Gateway interface {
	Part
	// Preflight checks the gateway and records its version and config hash.
	Preflight(ctx context.Context) (*GatewayManifest, error)
	// MintKey creates the run's virtual key: one provider, one key slot, one
	// model. The key is the agent's only credential.
	MintKey(ctx context.Context, rc *RunContext) (*RunKey, error)
	// RevokeKey deletes the virtual key. Safe to call more than once.
	RevokeKey(ctx context.Context, key *RunKey) error
	// RevokeStale deletes bench virtual keys whose run is not active.
	RevokeStale(ctx context.Context, active map[string]bool) (int, error)
	// FetchRequests pages the request rows for the key, fetches every detail
	// row, and polls until the set is complete or the timeout passes. An
	// incomplete set is returned and marked, never hidden.
	FetchRequests(ctx context.Context, key *RunKey, expect FetchExpectation) (*RequestLog, error)
}

// KeySlotHolder is a Gateway that can revoke a run key while keeping the
// key's slot reserved, in this process and at the fault proxy, until release
// is called. The run's cleanup clears the slot's fault plan in between, so no
// other run can take the slot and bind its own plan before the clear (which
// would wipe that run's plan, or be refused and fail a finished run). release
// is never nil when err is nil and is safe to call more than once.
type KeySlotHolder interface {
	RevokeKeyHold(ctx context.Context, key *RunKey) (release func(), err error)
}

// FetchExpectation tells FetchRequests what a complete set looks like.
type FetchExpectation struct {
	LedgerEntries []LedgerEntry // every row must match exactly one entry and vice versa
	Timeout       time.Duration // default 60s
	PollEvery     time.Duration // default 2s
}

// GatewayManifest is recorded in the experiment manifest.
type GatewayManifest struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	ConfigSHA256 string            `json:"config_sha256"`
	Plugins      []string          `json:"plugins,omitempty"`
	PriceSHA256  string            `json:"price_sha256,omitempty"`
	Settings     map[string]string `json:"settings,omitempty"` // auto tool inject, auth on inference, logging, retries
	CheckedAt    time.Time         `json:"checked_at"`
	Problems     []string          `json:"problems,omitempty"`     // preflight failures
	Hygiene      *GatewayHygiene   `json:"hygiene,omitempty"`      // features that could change a run's traffic; any violation refuses the plan
	DroppedLogs  *int64            `json:"dropped_logs,omitempty"` // Bifrost's global drop counter at preflight, the experiment's baseline

	// DeferredToRunner marks a manifest planned on a hosted server, which
	// runs no agent and opens no gateway: the gateway preflight (hygiene,
	// egress probe, drop counter) is the runner's, run before every slot
	// and uploaded with each run (remote/worker.json "gateway"); the
	// gateway_hygiene instrument reads those records (Tenancy area,
	// docs/platform-design.md).
	DeferredToRunner bool `json:"deferred_to_runner,omitempty"`
	// PartVersion is the bench gateway part a runner ran with,
	// "gateway/<name>@<version>", set only on the runner's per-run record of
	// a deferred manifest (whose part versions name no gateway).
	PartVersion string `json:"part_version,omitempty"`
}

// Deferred reports whether the gateway preflight is the runners' (a hosted
// manifest). Nil is not deferred.
func (g *GatewayManifest) Deferred() bool { return g != nil && g.DeferredToRunner }

// GatewayHygiene records the live gateway features that could change what a
// run's model requests carry or where they go, as preflight read them. The
// planner refuses an experiment with any violation, and the values are frozen
// in the manifest so the verdict page shows what was checked.
type GatewayHygiene struct {
	MCPClients           int      `json:"mcp_clients"`                  // MCP clients configured on the gateway
	MCPAutoToolInjectOff bool     `json:"mcp_disable_auto_tool_inject"` // client_config.mcp_disable_auto_tool_inject
	SemanticCache        bool     `json:"semantic_cache"`               // the semantic cache plugin is enabled
	RoutingRules         int      `json:"routing_rules"`                // enabled routing rules
	MultiTarget          []string `json:"multi_target,omitempty"`       // rules or configs that can send a model to another target
	Violations           []string `json:"violations,omitempty"`
	BenchRouting         []string `json:"bench_routing,omitempty"` // W9: routing rules the bench created for one run's key; expected, not violations
	// B2 Bifrost features: the semantic cache plugin's default_cache_key is
	// set (it then caches requests that send no key, so no arm can opt out),
	// and the semantic cache findings the planner accepted because an arm
	// under test opts in with its own cache key header.
	SemanticCacheDefaultKey bool     `json:"semantic_cache_default_key,omitempty"`
	BenchSemanticCache      []string `json:"bench_semantic_cache,omitempty"`
}

// RunKey is a run's virtual key. Value is a secret and is never written to disk.
type RunKey struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Value    string `json:"-"`
	KeySlot  string `json:"key_slot"` // provider key name, e.g. fp-slot-3
	SlotAuth string `json:"-"`        // the provider key value the fault proxy sees for this slot
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// W9 routing policy under test: the Bifrost routing rule created for the
	// run's key, deleted with it.
	RoutingRuleID string `json:"routing_rule_id,omitempty"`
	// Members are a team run's per-member keys on the same slot
	// (TeamGateway); empty for every other run.
	Members []MemberKey `json:"members,omitempty"`
}

// RequestLog is the gateway's record of one run.
type RequestLog struct {
	Requests []GatewayRequest `json:"requests"`
	Complete bool             `json:"complete"`
	Reason   string           `json:"reason,omitempty"` // why it is incomplete
	RowIDs   []string         `json:"row_ids"`
	Dropped  *int64           `json:"dropped_global,omitempty"` // Bifrost's global drop counter at collect time; analysis checks it rose by 0 over the experiment
}

// GatewayTally is the part of a run's gateway log that analysis reads: how
// many model requests carry usage, and Bifrost's global drop counter. The
// store derives it from evidence.json when it loads a run, so runs sealed
// before it existed carry it too.
type GatewayTally struct {
	Requests  int    `json:"requests"`                 // message rows that should carry usage: every one except error rows
	WithUsage int    `json:"with_usage"`               // of those, the rows with token counts
	Complete  bool   `json:"complete"`                 // the log was complete when collected
	Dropped   *int64 `json:"dropped_global,omitempty"` // nil when the counter was not read
}

// Tally counts the log's usage completeness. Only rows of kind message are
// model requests. An error row is left out: a refused or failed request has
// no usage to record, and an injected fault is billed as zero. A processing
// row stays in and has no usage, because its usage never arrived.
func (l *RequestLog) Tally() *GatewayTally {
	if l == nil {
		return nil
	}
	t := &GatewayTally{Complete: l.Complete, Dropped: l.Dropped}
	for _, r := range l.Requests {
		if r.Kind != "message" || r.Status == "error" {
			continue
		}
		t.Requests++
		if r.Tokens != nil {
			t.WithUsage++
		}
	}
	return t
}

// GatewayRequest is one Bifrost log row with its detail fields.
type GatewayRequest struct {
	ID             string          `json:"id"`
	ParentID       string          `json:"parent_id,omitempty"`
	Timestamp      time.Time       `json:"timestamp"`
	Status         string          `json:"status"` // success | error | processing
	Kind           string          `json:"kind"`   // message | count_tokens | models | other
	Provider       string          `json:"provider"`
	ModelRequested string          `json:"model_requested"`
	ModelServed    string          `json:"model_served"`
	FallbackIndex  int             `json:"fallback_index"`
	Stream         bool            `json:"stream"`
	LatencyMS      float64         `json:"latency_ms"`
	Tokens         *Tokens         `json:"tokens,omitempty"` // nil when usage was not recorded
	BifrostUSD     *float64        `json:"bifrost_usd,omitempty"`
	CacheHit       bool            `json:"cache_hit"`
	ResponseID     string          `json:"response_id,omitempty"` // provider message id when stored
	ErrorStatus    int             `json:"error_status,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	Attempts       []Attempt       `json:"attempts,omitempty"`
	ToolCalls      []ToolCallRef   `json:"tool_calls,omitempty"`      // tool calls in the response
	ToolResultIDs  []string        `json:"tool_result_ids,omitempty"` // tool results in the request input
	Raw            json.RawMessage `json:"raw,omitempty"`
	// W9 routing policy under test: the routing rule Bifrost matched for this
	// row and the routing engines it logged (routing-rule, governance, ...).
	RoutingRuleID   string   `json:"routing_rule_id,omitempty"`
	RoutingRuleName string   `json:"routing_rule_name,omitempty"`
	RoutingEngines  []string `json:"routing_engines,omitempty"`
	// Multi-agent attribution (agents.go). AgentID is the team member whose
	// virtual key the row came in on; AgentLabel is the agent name the
	// caller declared in x-heimdall-agent (or x-bf-lh-heimdall-agent) and
	// Bifrost logged in the row's metadata. Both are empty otherwise.
	AgentID    string `json:"agent_id,omitempty"`
	AgentLabel string `json:"agent_label,omitempty"`
	// B2 Bifrost features: Bifrost's own split of the row's latency
	// (overhead_latency and upstream_latency on v2.5.1 detail rows, in ms):
	// time spent inside the gateway, and time waiting for the upstream. Nil
	// when the row does not carry them. Complexity tier and semantic cache
	// hit type, when Bifrost logged them (docs/bifrost-features.md).
	OverheadMS     *float64 `json:"overhead_ms,omitempty"`
	UpstreamMS     *float64 `json:"upstream_ms,omitempty"`
	ComplexityTier string   `json:"complexity_tier,omitempty"`
	CacheHitType   string   `json:"cache_hit_type,omitempty"`
	CacheID        string   `json:"cache_id,omitempty"` // the semantic cache entry a row stored (miss) or was served from (hit)
}

// Attempt is one physical upstream try of a request, from Bifrost's attempt
// trail.
type Attempt struct {
	N            int    `json:"n"`
	KeyName      string `json:"key_name,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	FailReason   string `json:"fail_reason,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
	RetryAfterMS int    `json:"retry_after_ms,omitempty"`
}

// ToolCallRef is a tool call the model requested.
type ToolCallRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ArgsHash string `json:"args_hash"`
	Target   string `json:"target,omitempty"`
	// Matching hints the timeline links executions with, set only where
	// Target does not already carry them: the shell command of a tool whose
	// command is a "cmd" field or an argv array, and the file paths of a tool
	// whose paths are a "filePath" or "notebook_path" field or a patch, one
	// per line (a string, so the struct stays comparable).
	Command string `json:"command,omitempty"`
	Paths   string `json:"paths,omitempty"`
}

// GatewayWrapper is implemented by gateways that decorate another one (the
// Kubernetes driver's egress wrapper, for example). Optional capabilities
// such as the prompt-cache probe live on the inner gateway, so callers look
// them up with GatewayAs instead of a plain type assertion.
type GatewayWrapper interface {
	UnwrapGateway() Gateway
}

// GatewayAs reports whether gw, or any gateway it wraps, implements T, and
// returns the outermost one that does.
func GatewayAs[T any](gw Gateway) (T, bool) {
	for gw != nil {
		if t, ok := gw.(T); ok {
			return t, true
		}
		w, ok := gw.(GatewayWrapper)
		if !ok {
			break
		}
		gw = w.UnwrapGateway()
	}
	var zero T
	return zero, false
}
