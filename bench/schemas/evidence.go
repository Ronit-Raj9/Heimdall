package schemas

import "context"

// Evidence is everything collected about one run. Evidence sources fill their
// part during the collect stage; later stages read it.
type Evidence struct {
	Exec     *ExecResult       `json:"exec,omitempty"`
	Diff     *Diff             `json:"diff,omitempty"`
	Requests *RequestLog       `json:"requests,omitempty"` // gateway rows
	Ledger   []LedgerEntry     `json:"ledger,omitempty"`   // fault proxy
	Events   []Event           `json:"events,omitempty"`
	Claim    *Claim            `json:"claim,omitempty"`
	Cost     []CostRow         `json:"cost,omitempty"`
	ShimLog  []ShimCall        `json:"shim_log,omitempty"`
	Grade    *Grade            `json:"grade,omitempty"`
	Gates    []GateResult      `json:"gates,omitempty"`
	Protect  map[string]string `json:"protected_after,omitempty"` // sha256 of protected files after the run
}

// EvidenceSource reads one record of what happened and adds it to ev.
type EvidenceSource interface {
	Part
	Collect(ctx context.Context, rc *RunContext, ev *Evidence) error
}

// CostRow is one physical upstream attempt priced from the pinned snapshot.
// Bifrost's own cost is kept for reconciliation and never summed with it.
type CostRow struct {
	RequestID   string   `json:"request_id"`
	Attempt     int      `json:"attempt"`
	Model       string   `json:"model"`
	Tokens      Tokens   `json:"tokens"`
	PriceRef    string   `json:"price_ref"` // snapshot id plus sha256
	USD         Value    `json:"usd"`
	BifrostUSD  *float64 `json:"bifrost_usd,omitempty"`
	FaultInject bool     `json:"fault_inject"` // an injected failure; billed as zero by the provider
}

// ShimCall is one invocation recorded by a tool shim.
type ShimCall struct {
	Tool   string `json:"tool"`
	Argv   string `json:"argv"`
	Call   int    `json:"call"`
	Action string `json:"action"` // crash | empty | pass
	Nonce  string `json:"nonce,omitempty"`
	At     string `json:"at"`
}

// Grade is the hidden verifier's result for one diff.
type Grade struct {
	Passed           bool     `json:"passed"`
	InvalidEvaluator bool     `json:"invalid_evaluator"` // the grader could not decide; maps to unknown
	ExitCode         int      `json:"exit_code"`
	TestsRan         int      `json:"tests_ran"`
	Failures         int      `json:"failures"`
	Errors           int      `json:"errors"`
	Skipped          int      `json:"skipped"`
	MissingRequired  []string `json:"missing_required,omitempty"`
	FailedTests      []string `json:"failed_tests,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	SuiteVersion     string   `json:"suite_version"`
	GraderVersion    string   `json:"grader_version"`
	DiffSHA256       string   `json:"diff_sha256"`
	ParentSeal       string   `json:"parent_seal,omitempty"` // set on regrades
}

// Grader decides the outcome from the exported diff, never from the agent's
// container. The task carries the verifier tests and the suite carries the
// command, image and restore list.
type Grader interface {
	Part
	Grade(ctx context.Context, rc *RunContext, diff *Diff) (*Grade, error)
}

// GateEffect is what a fired gate does.
type GateEffect string

// Gate effects.
const (
	GateVeto       GateEffect = "veto"       // the run's outcome becomes integrity_violation (0 plus a flag)
	GateInvalidate GateEffect = "invalidate" // the experiment is INVALID
	GateReport     GateEffect = "report"     // reported only; never changes an outcome
	GateAdmission  GateEffect = "admission"  // checked at task admission
)

// Gate is one deterministic integrity check.
type Gate interface {
	Part
	Effect() GateEffect
	// Baseline gates cannot be switched off by an experiment.
	Baseline() bool
	Check(ctx context.Context, rc *RunContext, ev *Evidence) (*GateResult, error)
}

// GateResult is one gate's verdict on one run.
type GateResult struct {
	Gate     string     `json:"gate"`
	Version  string     `json:"version"`
	Effect   GateEffect `json:"effect"`
	Fired    bool       `json:"fired"`
	Detail   string     `json:"detail,omitempty"`
	Evidence []string   `json:"evidence,omitempty"`
}

// ValueKind selects the UI renderer and the aggregation for a metric.
type ValueKind string

// Value kinds with renderers in the UI.
const (
	KindRate     ValueKind = "rate" // 0..1 per run (pass/fail), shown as a percentage over runs
	KindCount    ValueKind = "count"
	KindTokens   ValueKind = "tokens"
	KindUSD      ValueKind = "usd"
	KindDuration ValueKind = "duration" // seconds
)

// MetricDescriptor describes a metric to the catalog and the UI.
type MetricDescriptor struct {
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	Label          string        `json:"label"`
	Kind           ValueKind     `json:"kind"`
	HigherIsBetter bool          `json:"higher_is_better"`
	MinEvidence    EvidenceLevel `json:"min_evidence"`
	Description    string        `json:"description"`
	PS2            bool          `json:"ps2"` // one of the six metrics PS2 requires
}

// Metric computes one value for one run. A value it cannot observe is unknown,
// never zero.
type Metric interface {
	Part
	Descriptor() MetricDescriptor
	Compute(ctx context.Context, rc *RunContext, ev *Evidence) (Value, error)
}
