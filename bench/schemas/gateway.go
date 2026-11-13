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
	Problems     []string          `json:"problems,omitempty"` // preflight failures
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
}

// RequestLog is the gateway's record of one run.
type RequestLog struct {
	Requests []GatewayRequest `json:"requests"`
	Complete bool             `json:"complete"`
	Reason   string           `json:"reason,omitempty"` // why it is incomplete
	RowIDs   []string         `json:"row_ids"`
	Dropped  *int64           `json:"dropped_global,omitempty"` // Bifrost's global drop counter, a warning signal only
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
}
