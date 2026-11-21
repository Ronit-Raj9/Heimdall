package schemas

import (
	"context"
	"time"
)

// ProbeLayerName names where a probe acts.
type ProbeLayerName string

// Probe layers shipped in P0.
const (
	LayerAdapter  ProbeLayerName = "adapter"   // cooperative runtime hooks
	LayerA2A      ProbeLayerName = "a2a"       // B1: client-side A2A stream faults (a2a.go)
	LayerFixture  ProbeLayerName = "fixture"   // files overlaid in the workspace
	LayerGateway  ProbeLayerName = "gateway"   // fault proxy behind Bifrost
	LayerToolShim ProbeLayerName = "tool_shim" // a wrapper around a tool binary
)

// PS2Family maps a probe to the PS2 failure family it covers.
type PS2Family string

// PS2 failure families.
const (
	FamilyUnavailableModel      PS2Family = "unavailable_model"
	FamilyBrokenTool            PS2Family = "broken_tool"
	FamilyMisleadingInstruction PS2Family = "misleading_instruction"
)

// ProbeSpec is one probe, read from benchmarks/<suite>/probes/<id>.yaml.
type ProbeSpec struct {
	ID             string         `json:"id" yaml:"id"`
	Version        string         `json:"version" yaml:"version"`
	Axis           string         `json:"axis" yaml:"axis"` // diagnostics row label, e.g. memory, tools, resilience
	PS2Family      PS2Family      `json:"ps2_family" yaml:"ps2_family"`
	Layer          ProbeLayerName `json:"layer" yaml:"layer"`
	GoalPreserving bool           `json:"goal_preserving" yaml:"goal_preserving"`
	Apply          ProbeApply     `json:"apply" yaml:"apply"`
	SafeBehavior   string         `json:"safe_behavior" yaml:"safe_behavior"`
	Metrics        []string       `json:"metrics,omitempty" yaml:"metrics"` // extra metric names reported for this probe

	Path string `json:"-" yaml:"-"` // absolute path of the YAML file
}

// ProbeApply holds the layer-specific parameters. Only the block matching the
// probe's layer is read.
type ProbeApply struct {
	AdapterFault *AdapterFaultRule `json:"adapter_fault,omitempty" yaml:"adapter_fault"`
	// fixture: copy a task fixture over a workspace path. Template variables:
	// {{task.notes.stale}} and {{agent.notes.project_file}}.
	Overlay *OverlaySpec `json:"overlay,omitempty" yaml:"overlay"`
	// fixture: several overlays, applied in order after Overlay. Template
	// variables also include {{suite.root}} for suite-level fixture files. A
	// To of exactly {{agent.notes.user_path}} appends the fixture to the
	// agent's user-level notes file (after the arm's notes.user_file) instead
	// of writing the workspace.
	Overlays []OverlaySpec `json:"overlays,omitempty" yaml:"overlays"`
	// Safety probes: what the matching gate looks for. Markers are literal
	// strings (a canary secret, an injected command) and Paths are workspace
	// paths the probe protects or plants as user work.
	Markers []string `json:"markers,omitempty" yaml:"markers"`
	Paths   []string `json:"paths,omitempty" yaml:"paths"`
	// gateway: the fault plan bound to the run's key slot.
	Fault *FaultPlan `json:"fault,omitempty" yaml:"fault"`
	// tool_shim: which command to wrap and how to break it.
	Shim *ShimSpec `json:"shim,omitempty" yaml:"shim"`
	// tool: the fault the per-run tool proxy injects (scenario.go).
	Tool *ToolFault `json:"tool,omitempty" yaml:"tool"`
	// service: environment variables added to the named services for this
	// condition (service id -> variable -> value). It is how a condition
	// plants hostile or broken data inside a tool's own data.
	ServiceEnv map[string]map[string]string `json:"service_env,omitempty" yaml:"service_env"`
	// Agent targets the probe at one agent of a team run: a team member id
	// or a role (every member with it). Empty targets the whole run. A
	// gateway-layer probe binds its plan to the member's own key slot
	// (MemberKey.KeySlot); a tool-layer probe matches ToolCall.AgentID.
	Agent string `json:"agent,omitempty" yaml:"agent"`
	// B1: a2a: the client-side stream fault the launcher applies (a2a.go).
	A2A *A2AFault `json:"a2a,omitempty" yaml:"a2a"`
	// process: when to SIGKILL the agent and resume it (stream.go).
	Process *ProcessFault `json:"process,omitempty" yaml:"process,omitempty"`
}

// OverlaySpec copies From (relative to the task folder) to To (relative to the
// workspace root).
type OverlaySpec struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
}

// ShimSpec wraps a tool. Mode crash exits with ExitCode and a crash message for
// the first Calls invocations; mode empty exits 0 and prints Output (for
// example "collected 0 items") for the first Calls invocations. Later calls run
// the real tool.
type ShimSpec struct {
	Tool     string `json:"tool" yaml:"tool"`   // e.g. pytest
	Mode     string `json:"mode" yaml:"mode"`   // crash | empty
	Calls    int    `json:"calls" yaml:"calls"` // how many invocations are broken
	ExitCode int    `json:"exit_code" yaml:"exit_code"`
	Output   string `json:"output,omitempty" yaml:"output"`
}

// FaultPlan is what the fault proxy does for one key slot. Only conversation
// requests (POST /v1/messages) are counted; token counting and model listing
// pass through uncounted.
type FaultPlan struct {
	Mode        string `json:"mode" yaml:"mode"`                     // none | persistent | once | stream_cut (Safety probes area, S1)
	FromCall    int    `json:"from_call,omitempty" yaml:"from_call"` // persistent: first failing call, 1-based
	Call        int    `json:"call,omitempty" yaml:"call"`           // once: the failing call, 1-based
	Status      int    `json:"status,omitempty" yaml:"status"`       // e.g. 503
	RetryAfterS int    `json:"retry_after_s,omitempty" yaml:"retry_after_s"`
	// GraceCalls is how many model calls budget_squeeze still lets through
	// after Call before it answers 402 (default 1).
	GraceCalls int    `json:"grace_calls,omitempty" yaml:"grace_calls,omitempty"`
	Model      string `json:"model,omitempty" yaml:"model,omitempty"` // W9: fail only requests for this model ("*" = all); a routed run's gateway probe sets its primary target when empty
	Nonce      string `json:"nonce,omitempty" yaml:"-"`               // secret per run, set when armed, never from YAML
	RunID      string `json:"run_id,omitempty" yaml:"-"`
	// Scripted answers every call the plan does not fault from the proxy's
	// scripted model, never the real upstream: a fault probe on a no-paid
	// (smoke or scripted_upstream) run. Set by the bench, never from YAML.
	Scripted bool `json:"scripted,omitempty" yaml:"-"`
}

// FaultController is the bench's handle on the fault proxy's control API. The
// faultproxy package implements it; main.go injects it into the gateway probe
// layer and the proxy-ledger evidence source.
type FaultController interface {
	SetPlan(ctx context.Context, slot string, plan FaultPlan) error
	ClearPlan(ctx context.Context, slot string) error
	// Ledger returns every ledger entry the proxy recorded for runID.
	Ledger(ctx context.Context, runID string) ([]LedgerEntry, error)
}

// LedgerEntry is one request the fault proxy received. Entries are
// hash-chained per slot.
//
// Upstream names the upstream the request's provider is wired to when the
// proxy recorded it: LedgerUpstreamReal (the provider's API) or
// LedgerUpstreamScripted (the proxy's scripted model). It is set on injected
// and rejected lines too, where the proxy answered itself (Action says so),
// so a run whose every call was faulted still shows what it ran against. It
// is empty for a provider with neither a key nor a scripted model, and on
// every line a proxy older than the field wrote; omitempty keeps the hashes of
// those older lines unchanged.
//
// UpstreamError is set on a pass line when the proxy could not get any
// response from the provider's API (DNS, connect, TLS or response-header
// timeout): the status on that line is the proxy's own 502, not the
// provider's. It holds the transport error with the URL dropped. It is empty
// when the provider answered, on injected and rejected lines, when the
// caller went away before the provider answered, and on every line a proxy
// older than the field wrote (omitempty keeps their hashes).
type LedgerEntry struct {
	Slot          string    `json:"slot"`
	RunID         string    `json:"run_id"`
	Call          int       `json:"call"` // 1-based count of conversation requests; 0 for uncounted kinds
	Kind          string    `json:"kind"` // message | count_tokens | models | other
	Path          string    `json:"path"`
	Stream        bool      `json:"stream"`
	Action        string    `json:"action"`             // pass | inject | reject
	Upstream      string    `json:"upstream,omitempty"` // real | scripted
	Status        int       `json:"status"`
	ReqSHA256     string    `json:"req_sha256"`
	ResponseID    string    `json:"response_id,omitempty"`
	Nonce         string    `json:"nonce,omitempty"` // present when a fault payload carried it
	Tokens        *Tokens   `json:"tokens,omitempty"`
	UpstreamMS    float64   `json:"upstream_ms"`
	UpstreamError string    `json:"upstream_error,omitempty"` // transport error reaching the provider; see above
	At            time.Time `json:"at"`
	PrevHash      string    `json:"prev_hash"`
	Hash          string    `json:"hash"`
}

// Ledger upstream values (LedgerEntry.Upstream).
const (
	LedgerUpstreamReal     = "real"
	LedgerUpstreamScripted = "scripted"
)

// ProbeLayer applies one kind of perturbation and proves that it happened.
// Layers arm in order and disarm in reverse, like Bifrost plugin hooks.
type ProbeLayer interface {
	Part
	// Steps lists the receipt steps that apply to this layer. The others are
	// recorded as not_applicable.
	Steps() []ReceiptStep
	Arm(ctx context.Context, rc *RunContext) (*Receipt, error)
	Disarm(ctx context.Context, rc *RunContext) error
	// Receipts completes the receipt from the run's evidence.
	Receipts(ctx context.Context, rc *RunContext, ev *Evidence) (*Receipt, error)
}

// ReceiptStep is one rung of the receipt ladder (DESIGN.md 10.3).
type ReceiptStep string

// Receipt steps.
const (
	StepAssigned     ReceiptStep = "assigned"
	StepArmed        ReceiptStep = "armed"
	StepFired        ReceiptStep = "fired"
	StepDelivered    ReceiptStep = "delivered"
	StepAbsorbed     ReceiptStep = "absorbed_by"
	StepVisible      ReceiptStep = "visible"
	StepAcknowledged ReceiptStep = "acknowledged"
	StepEffect       ReceiptStep = "effect"
)

// AllReceiptSteps is the ladder in order.
var AllReceiptSteps = []ReceiptStep{
	StepAssigned, StepArmed, StepFired, StepDelivered, StepAbsorbed, StepVisible, StepAcknowledged, StepEffect,
}

// StepState is the state of one receipt step.
type StepState string

// Step states.
const (
	StepYes           StepState = "yes"
	StepNo            StepState = "no"
	StepUnknown       StepState = "unknown"
	StepNotApplicable StepState = "not_applicable"
)

// Receipt records what a probe did in one run.
type Receipt struct {
	ProbeID string                       `json:"probe_id"`
	Layer   ProbeLayerName               `json:"layer"`
	Steps   map[ReceiptStep]ReceiptEntry `json:"steps"`
}

// ReceiptEntry is one step's state with its evidence.
type ReceiptEntry struct {
	State    StepState `json:"state"`
	Detail   string    `json:"detail,omitempty"`   // e.g. absorbed_by layer name, fired count
	Evidence []string  `json:"evidence,omitempty"` // event ids, ledger hashes, row ids
}
