package schemas

import (
	"context"
	"encoding/json"
	"time"
)

// AdapterFaultProtocol is the version of the opt-in runtime hook protocol.
const AdapterFaultProtocol = "1"

// AdapterFaultRule selects exact operation names, across sessions by default.
type AdapterFaultRule struct {
	Kind      string `json:"kind" yaml:"kind"`
	Name      string `json:"name" yaml:"name"`
	Mode      string `json:"mode" yaml:"mode"`
	Session   string `json:"session,omitempty" yaml:"session,omitempty"`
	FromCall  int    `json:"from_call,omitempty" yaml:"from_call,omitempty"`
	Calls     int    `json:"calls,omitempty" yaml:"calls,omitempty"`
	Message   string `json:"message,omitempty" yaml:"message,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Result    any    `json:"result,omitempty" yaml:"result,omitempty"`
}

// AdapterFaultHook declares cooperative instrumentation, not tool coverage.
type AdapterFaultHook struct {
	Kind  string   `json:"kind" yaml:"kind"`
	Name  string   `json:"name" yaml:"name"`
	Modes []string `json:"modes" yaml:"modes"`
}

// AdapterFaultInjector is optional. Unsupported adapters are refused before runs.
type AdapterFaultInjector interface {
	ValidateFault(*AgentConfig, AdapterFaultRule) error
	BindFaultRuntime(*RunContext, *RunPlan) error
}

// AdapterFaultEnvironment marks environments that isolate runtime controllers.
type AdapterFaultEnvironment interface{ SupportsAdapterFaults() bool }

// AdapterFaultController owns the private sidecar and exports its ledger.
type AdapterFaultController interface {
	Part
	Arm(context.Context, *RunContext) error
	Freeze(context.Context, *RunContext) error
	Cleanup(context.Context, *RunContext) error
}

// AdapterFaultRuntime holds ephemeral credentials, never persisted in evidence.
type AdapterFaultRuntime struct {
	URL, Token, Network, Nonce string
}

// AdapterFaultConfig is a private controller file, inaccessible to the agent.
type AdapterFaultConfig struct {
	RunID      string             `json:"run_id"`
	Token      string             `json:"token"`
	AdminToken string             `json:"admin_token"`
	Nonce      string             `json:"nonce"`
	Rule       AdapterFaultRule   `json:"rule"`
	Hooks      []AdapterFaultHook `json:"hooks"`
	WallMS     int                `json:"wall_ms"`
	Sessions   []string           `json:"sessions"`
}

// AdapterFaultRequest is a versioned, idempotent runtime control message.
type AdapterFaultRequest struct {
	Version     string             `json:"version"`
	RunID       string             `json:"run_id"`
	Session     string             `json:"session"`
	OperationID string             `json:"operation_id,omitempty"`
	Kind        string             `json:"kind,omitempty"`
	Name        string             `json:"name,omitempty"`
	Phase       string             `json:"phase"` // register, before, applied, completed, finish
	Hooks       []AdapterFaultHook `json:"hooks,omitempty"`
	Success     bool               `json:"success,omitempty"`
	Healthy     bool               `json:"healthy,omitempty"`
}

// AdapterFaultDecision is returned only to the hook invoking this operation.
type AdapterFaultDecision struct {
	Mode      string          `json:"mode"` // pass, error, timeout, replace
	Message   string          `json:"message,omitempty"`
	TimeoutMS int             `json:"timeout_ms,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// AdapterFaultEntry records decisions and cooperative acknowledgements separately.
// It deliberately excludes callback arguments, memory contents and browser data.
type AdapterFaultEntry struct {
	Seq         int       `json:"seq"`
	At          time.Time `json:"at"`
	RunID       string    `json:"run_id"`
	Session     string    `json:"session"`
	OperationID string    `json:"operation_id,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Name        string    `json:"name,omitempty"`
	Phase       string    `json:"phase"`
	Mode        string    `json:"mode,omitempty"`
	Message     string    `json:"message,omitempty"`
	Success     bool      `json:"success,omitempty"`
	PrevHash    string    `json:"prev_hash"`
	Hash        string    `json:"hash"`
}

// AdapterFaultEvidence is frozen before collection and sealed with the run.
type AdapterFaultEvidence struct {
	Version    string              `json:"version"`
	RunID      string              `json:"run_id"`
	RuleHash   string              `json:"rule_hash"`
	Complete   bool                `json:"complete"`
	Reason     string              `json:"reason,omitempty"`
	LedgerHash string              `json:"ledger_hash"`
	Entries    []AdapterFaultEntry `json:"entries"`
}
