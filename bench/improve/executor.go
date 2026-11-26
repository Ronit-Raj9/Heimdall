package improve

import (
	"context"

	"github.com/maximhq/bifrost/bench/agentversions"
)

// Phases of an improvement run's experiments (ScoreRequest.Phase).
const (
	PhaseBaseline = "baseline" // version 0 on dev before round 1: the first evidence
	PhaseScreen   = "screen"   // incumbent vs candidate on dev
	PhaseAudit    = "audit"    // incumbent vs candidate on audit, plus honeypots
	PhaseSealed   = "sealed"   // incumbent on sealed, read only by the report
	PhaseControl  = "control"  // control arms on sealed
	PhaseMonitor  = "monitor"  // channel parity reads the screen's runs

	PhaseFinal     = "final"     // version 0, final and null final on sealed: the claim
	PhaseExtension = "extension" // the second look of a continued run
	PhaseRetries   = "retries"   // version 0 retried on visible failures
)

// Version is one agent version the loop runs: its pinned reference, the
// frozen YAML, the editable bundle and the version number in the agent's
// version store (0 when not stored, such as a library agent's file).
type Version struct {
	Ref        string `json:"ref"`
	Number     int    `json:"number"`
	ConfigHash string `json:"config_hash"`
	BundleHash string `json:"bundle_hash"`
	Doc        []byte `json:"doc,omitempty"`
	Bundle     Bundle `json:"bundle,omitempty"`
	// BundleObj and PlaybookObj are the version's bundle and playbook
	// objects (A1.2); BundleHash keeps its old meaning.
	BundleObj   string `json:"bundle_obj,omitempty"`
	PlaybookObj string `json:"playbook_obj,omitempty"`
}

// TaskInfo is one task of the pool.
type TaskInfo struct {
	ID         string `json:"id"`
	Impossible bool   `json:"impossible,omitempty"`
}

// Protected is what the improver must never learn, read once at creation
// from the audit and sealed tasks: task ids, secret texts (verifier files,
// expected answers, hidden test names) and the suites' canary GUIDs. The
// integrity gates search the candidate bundle for them.
type Protected struct {
	TaskIDs  []string `json:"-"`
	Secrets  []string `json:"-"`
	Canaries []string `json:"-"`
}

// EvidenceItem is one dev task result the improver may read: outcome and
// gate names, never verifier content.
type EvidenceItem struct {
	Task    string   `json:"task"`
	Trial   int      `json:"trial"`
	Outcome string   `json:"outcome"`
	Success float64  `json:"success"`
	Gates   []string `json:"gates,omitempty"`

	// The fields below are the evidence builder's (A3.1); all are optional.
	RunRef         string             `json:"run_ref,omitempty"`
	Arm            string             `json:"arm,omitempty"`
	Version        int                `json:"version,omitempty"`
	Instruction    string             `json:"instruction,omitempty"`
	Checks         []EvidenceCheck    `json:"checks,omitempty"`
	TestsRan       int                `json:"tests_ran,omitempty"`
	TestsFailed    int                `json:"tests_failed,omitempty"`
	StopReason     string             `json:"stop_reason,omitempty"`
	Steps          int                `json:"steps,omitempty"`
	CostUSD        float64            `json:"cost_usd,omitempty"`
	Tokens         *EvidenceTokens    `json:"tokens,omitempty"`
	WallS          float64            `json:"wall_s,omitempty"`
	ToolCalls      []EvidenceToolCall `json:"tool_calls,omitempty"`
	FailingStep    *EvidenceStep      `json:"failing_step,omitempty"`
	Errors         []string           `json:"errors,omitempty"`
	Final          *EvidenceFinal     `json:"final,omitempty"`
	Opened         []string           `json:"opened,omitempty"`
	ItemsInContext []string           `json:"items_in_context,omitempty"`
	Cluster        string             `json:"cluster,omitempty"`
	Noisy          bool               `json:"noisy,omitempty"`
	Flip           string             `json:"flip,omitempty"`
	Digest         string             `json:"digest,omitempty"`
	Transcript     string             `json:"transcript,omitempty"`
}

// EvidenceCheck is one check name and whether it passed; never its feedback text.
type EvidenceCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// EvidenceTokens are a run's token counts.
type EvidenceTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cached int `json:"cached"`
}

// EvidenceToolCall is one tool call of a run, args clipped.
type EvidenceToolCall struct {
	I     int     `json:"i"`
	Tool  string  `json:"tool"`
	Args  string  `json:"args,omitempty"`
	Exit  int     `json:"exit,omitempty"`
	S     float64 `json:"s,omitempty"`
	Error bool    `json:"error,omitempty"`
}

// EvidenceStep is the step that produced the last error before the stop.
type EvidenceStep struct {
	I         int    `json:"i"`
	Tool      string `json:"tool"`
	Args      string `json:"args,omitempty"`
	ErrorText string `json:"error_text,omitempty"`
}

// EvidenceFinal is a run's final diff or answer, clipped.
type EvidenceFinal struct {
	Kind string `json:"kind"` // diff | answer
	Text string `json:"text"`
}

// ImproveRequest is one improver phase. Null asks for the null improver:
// the same budget, no evidence.
type ImproveRequest struct {
	ImprovementID string
	Spec          *Spec
	Round         int
	Incumbent     Version
	Evidence      []EvidenceItem
	BudgetUSD     float64
	WallS         int
	Null          bool
	// The fields below are A3.3's. The evidence document of A3.1 is not a
	// field yet: Evidence already names the item list above.
	Kind     string // mutation | merge | restart | null
	Second   *Version
	Model    string
	Provider string
	Seed     int64
	Limits   Limits
}

// ImproveResult is the improver's output: the full new bundle (every path
// it returns, editable or not, is checked), its spend, the run that wrote
// it, and flags the run raised (oversight_disabled, egress).
type ImproveResult struct {
	Bundle  Bundle   `json:"-"`
	CostUSD float64  `json:"cost_usd"`
	RunID   string   `json:"run_id,omitempty"`
	ExpID   string   `json:"exp_id,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	// Edits is the parsed edits.json when the improver wrote one; Reasons
	// and Summary come with a bundle.json (A Part 0 D3).
	Edits   *EditsFile        `json:"edits,omitempty"`
	Reasons map[string]string `json:"reasons,omitempty"`
	Summary string            `json:"summary,omitempty"`
}

// MaxScoreArms is how many versions one scoring experiment may hold
// (letters A to F).
const MaxScoreArms = 6

// ScoreRequest is one scoring experiment: up to MaxScoreArms arms (A, B,
// then C to F) on tasks, trials each.
type ScoreRequest struct {
	ImprovementID string
	Spec          *Spec
	Round         int
	Phase         string
	Arms          []Version
	Tasks         []string
	Trials        int
}

// RunScore is one counted run of a scoring experiment.
type RunScore struct {
	Arm     int      `json:"arm"` // index into ScoreRequest.Arms
	Task    string   `json:"task"`
	Trial   int      `json:"trial"`
	Success float64  `json:"success"` // in [0, 1]
	Known   bool     `json:"known"`   // false: no outcome (infra failure), left out of every test
	Outcome string   `json:"outcome"`
	Vetoes  []string `json:"vetoes,omitempty"` // integrity gates that vetoed the run
	CostUSD float64  `json:"cost_usd"`
	// Stop is the run's outcome when it is a visible failure (timeout,
	// budget, crash, refusal), else empty (A2.6).
	Stop  string `json:"stop,omitempty"`
	RunID string `json:"run_id,omitempty"`
}

// ScoreResult is a scoring experiment's counted runs, spend and the event
// channels each arm emitted (for monitor parity).
type ScoreResult struct {
	ExpID    string     `json:"exp_id"`
	Runs     []RunScore `json:"runs"`
	CostUSD  float64    `json:"cost_usd"`
	Channels [][]string `json:"channels,omitempty"` // per arm
}

// Executor runs the loop's experiments; cmd/bifrost-bench implements it
// with the planner, the scheduler and the run store.
type Executor interface {
	// Load resolves an agent reference to a frozen version.
	Load(ctx context.Context, ref string, ed Editable) (Version, error)
	// Freeze stores doc as a new version derived from parent (author
	// agent) and returns it pinned. A doc equal to a stored version
	// returns that version.
	Freeze(ctx context.Context, parent Version, doc []byte, lin Lineage) (Version, error)
	// Tasks lists the suite's task pool (spec.Tasks narrows it).
	Tasks(ctx context.Context, spec *Spec) ([]TaskInfo, error)
	// Protect reads the protected material of the audit and sealed tasks.
	Protect(ctx context.Context, spec *Spec, tasks []string) (Protected, error)
	// Improve runs one improver phase.
	Improve(ctx context.Context, req ImproveRequest) (*ImproveResult, error)
	// Score runs one scoring experiment.
	Score(ctx context.Context, req ScoreRequest) (*ScoreResult, error)
	// CheckImprover refuses a spec whose improver task cannot run (A1.6).
	CheckImprover(ctx context.Context, spec *Spec) error
	// Runs returns the run refs (with folders) of a scoring experiment.
	Runs(ctx context.Context, expID string) ([]RunRef, error)
}

// Lineage is what Freeze records on a derived version.
type Lineage struct {
	ImprovementID string
	Round         int
	BundleHash    string
	ProducedByRun string
	Note          string
	// Editable is the spec's editable set: Freeze reads the candidate's
	// bundle from the derived document with it (A1.3).
	Editable Editable
}

// NewVersion builds a Version from a frozen agent YAML document: its
// config hash (as the planner computes it) and its editable bundle.
func NewVersion(ref string, number int, doc []byte, ed Editable) (Version, error) {
	h, err := agentversions.ConfigHash(doc)
	if err != nil {
		return Version{}, err
	}
	b, err := ExtractBundle(doc, ed)
	if err != nil {
		return Version{}, err
	}
	return Version{Ref: ref, Number: number, ConfigHash: h, BundleHash: b.Hash(), Doc: doc, Bundle: b}, nil
}
