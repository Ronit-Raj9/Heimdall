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
}

// ScoreRequest is one scoring experiment: one or two arms (A, then B) on
// tasks, trials each.
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
}

// Lineage is what Freeze records on a derived version.
type Lineage struct {
	ImprovementID string
	Round         int
	BundleHash    string
	ProducedByRun string
	Note          string
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
