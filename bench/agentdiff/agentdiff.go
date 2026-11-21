// Package agentdiff answers "what changed between version A and version B of
// my agent, and which tasks flipped?" (docs/agent-diff.md).
//
// A version is one resolved agent config hash, picked by the hash itself, by
// an experiment arm (<exp_id>:<arm>), or as one of the two newest config
// hashes of an agent id on a suite. The report has two halves. The config
// diff compares the two resolved configs from their manifests field by field
// with dotted paths, groups the changes (model, prompt and notes, tools,
// budgets, gateway policy, image, other), compares laid-down files by hash
// and shows a capped text diff when both contents are at hand. The outcome
// diff compares the tasks both versions ran under one suite version and one
// condition, over verified sealed runs only: per task pass rates, which
// tasks flipped, the exact paired sign-flip test, metric deltas read with
// their direction, and gates that fire in B but not in A.
//
// The package is pure compute over schemas types and the run folder's read
// side (Source). It imports no handler and no command, writes nothing, and
// can be removed by deleting it with its command file, handler file and UI
// folder.
package agentdiff

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrBadQuery marks a selection the diff cannot answer as asked: a malformed
// selector, an unknown hash or arm, an ambiguous prefix, or two versions with
// no common task. The HTTP handler maps it to 400 and the CLI to a usage
// error.
var ErrBadQuery = errors.New("bad agent diff query")

// Defaults of a Query.
const (
	DefaultAlpha = 0.05
	// MaxFileBytes caps each side of a text diff. A larger file is compared
	// by hash only.
	MaxFileBytes = 64 << 10
	// MaxDiffLines caps the lines of one text diff; the rest is cut and the
	// file change says so.
	MaxDiffLines = 400
)

// Change kinds of a config field or a file.
const (
	KindChanged = "changed"
	KindAdded   = "added"
	KindRemoved = "removed"
)

// Config groups, in the order the report lists them.
const (
	GroupModel   = "model"
	GroupPrompt  = "prompt_notes"
	GroupTools   = "tools"
	GroupBudgets = "budgets"
	GroupGateway = "gateway"
	GroupImage   = "image"
	GroupOther   = "other"
)

// GroupOrder is every config group in report order.
var GroupOrder = []string{GroupModel, GroupPrompt, GroupTools, GroupBudgets, GroupGateway, GroupImage, GroupOther}

// Task classes of the outcome diff. A version passes a task stably when
// every counted trial passed and fails it stably when none did; anything in
// between is unstable. Moved classes are a rate change where one side is
// unstable, so it is not a clean flip.
const (
	ClassFlippedToPass  = "flipped_to_pass"
	ClassFlippedToFail  = "flipped_to_fail"
	ClassUnchangedPass  = "unchanged_pass"
	ClassUnchangedFail  = "unchanged_fail"
	ClassUnstableInBoth = "unstable_in_both"
	ClassMovedToPass    = "moved_to_pass"
	ClassMovedToFail    = "moved_to_fail"
)

// Conclusions of the paired sign-flip test.
const (
	ConclusionBetter     = "b_better"
	ConclusionWorse      = "b_worse"
	ConclusionNoClear    = "no_clear_difference"
	ConclusionCannotTell = "cannot_tell"
)

// Metric directions.
const (
	DirectionBetter  = "better"
	DirectionWorse   = "worse"
	DirectionSame    = "same"
	DirectionUnknown = "unknown"
)

// Run is one run as the diff reads it from the run index.
type Run struct {
	Slot       schemas.Slot
	Run        schemas.Run
	Sealed     bool
	Superseded bool // run control: requeued, never counts
}

// Source is the read side of the run folder: the store's index with its seal
// verification. VerifySeal returns nil when a sealed run's files match its
// seal.json and its experiment's seals.log, and an error wrapping
// schemas.ErrSealMismatch when they do not; such a run is left out. Any
// other error fails the diff. It is required, never optional: the diff only
// counts runs it verified.
type Source interface {
	Experiments() []string
	Manifest(expID string) (*schemas.Manifest, bool)
	Runs(expID string) []Run
	// RunFile returns one file of a run folder. A missing file is an error
	// wrapping fs.ErrNotExist or schemas.ErrNotFound.
	RunFile(runID, name string) ([]byte, error)
	VerifySeal(ctx context.Context, runID string) error
}

// DecisionSource is optionally implemented by a Source. LatestDecision
// returns the decision of the experiment's newest analysis; an experiment
// whose verdict is invalid contributes no run, as on the leaderboard.
type DecisionSource interface {
	LatestDecision(expID string) (schemas.Decision, bool)
}

// ContentSource reads a file the bench root holds (a notes user file) for a
// text diff. ok is false when the file cannot be read; the diff then compares
// by hash only. A nil ContentSource reads nothing.
type ContentSource func(relPath string) (data []byte, ok bool)

// Query selects the two versions and the slice of runs to compare.
type Query struct {
	A         string // config hash (or unique prefix of 8+ characters) or <exp_id>:<arm>
	B         string
	Agent     string // with A and B empty: the two newest config hashes of this agent id on Suite
	Suite     string // id@version; empty: the newest suite both versions ran
	Condition string // clean or a probe id; empty: clean
	Synthetic bool   // include synthetic runs (scripted agent, scripted model, smoke experiment)
	Alpha     float64
}

// Report is the whole answer.
type Report struct {
	A         Version     `json:"a"`
	B         Version     `json:"b"`
	Suite     string      `json:"suite"`
	Condition string      `json:"condition"`
	Synthetic bool        `json:"synthetic"`
	Config    ConfigDiff  `json:"config"`
	Outcome   OutcomeDiff `json:"outcome"`
	Summary   string      `json:"summary"` // one plain sentence or three
}

// Version is one side of the diff.
type Version struct {
	Selector    string      `json:"selector"` // as asked
	ConfigHash  string      `json:"config_hash"`
	AgentID     string      `json:"agent_id"`
	Label       string      `json:"label"`
	ExpID       string      `json:"exp_id,omitempty"` // set when picked by <exp_id>:<arm>; only that arm's runs count
	Arm         string      `json:"arm,omitempty"`
	Experiments []string    `json:"experiments"` // experiments whose verified runs entered
	Runs        int         `json:"runs"`        // verified runs on the suite and condition
	Excluded    []Exclusion `json:"excluded"`
}

// Exclusion counts the runs of a version left out for one reason.
type Exclusion struct {
	Reason string `json:"reason"`
	Runs   int    `json:"runs"`
}

// ConfigDiff is the config half of the report.
type ConfigDiff struct {
	Identical bool          `json:"identical"`
	Changes   int           `json:"changes"` // field and file changes over every group
	Groups    []ConfigGroup `json:"groups"`  // non-empty groups in GroupOrder
}

// ConfigGroup is the changes of one group.
type ConfigGroup struct {
	ID     string        `json:"id"`
	Fields []FieldChange `json:"fields"`
	Files  []FileChange  `json:"files"`
}

// FieldChange is one dotted path that differs. Old is null when added, New
// is null when removed. A redacted value reads "[redacted]" on both sides.
type FieldChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Old      any    `json:"old"`
	New      any    `json:"new"`
	Redacted bool   `json:"redacted,omitempty"`
}

// FileChange is one laid-down file whose hash differs. Diff is a unified
// text diff when both contents were at hand and small enough; DiffNote says
// why there is none otherwise.
type FileChange struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	OldHash   string `json:"old_hash,omitempty"`
	NewHash   string `json:"new_hash,omitempty"`
	Diff      string `json:"diff,omitempty"`
	DiffNote  string `json:"diff_note,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// OutcomeDiff is the outcome half of the report.
type OutcomeDiff struct {
	CommonTasks int           `json:"common_tasks"`
	OnlyA       []string      `json:"only_a"` // tasks only A ran (verified) on the suite and condition
	OnlyB       []string      `json:"only_b"`
	PassRateA   float64       `json:"pass_rate_a"` // mean per-task pass rate over the common tasks
	PassRateB   float64       `json:"pass_rate_b"`
	Counts      ClassCounts   `json:"counts"`
	Net         int           `json:"net"` // flipped_to_pass minus flipped_to_fail
	Test        SignTest      `json:"test"`
	Tasks       []TaskOutcome `json:"tasks"` // flips first, then moves, unstable, unchanged; by task id within a class
	Metrics     []MetricDelta `json:"metrics"`
	Gates       []GateChange  `json:"gates"`
}

// ClassCounts counts the common tasks per class.
type ClassCounts struct {
	FlippedToPass  int `json:"flipped_to_pass"`
	FlippedToFail  int `json:"flipped_to_fail"`
	UnchangedPass  int `json:"unchanged_pass"`
	UnchangedFail  int `json:"unchanged_fail"`
	UnstableInBoth int `json:"unstable_in_both"`
	MovedToPass    int `json:"moved_to_pass"`
	MovedToFail    int `json:"moved_to_fail"`
}

// SignTest is the exact paired sign-flip test on per-task pass rate
// differences B minus A. P is null when there is no common task.
type SignTest struct {
	NTasks       int      `json:"n_tasks"`
	NNonzero     int      `json:"n_nonzero"`
	NPositive    int      `json:"n_positive"`
	NNegative    int      `json:"n_negative"`
	P            *float64 `json:"p"`
	Exact        bool     `json:"exact"`
	MinPossibleP float64  `json:"min_possible_p"` // smallest p the changed tasks allow
	Alpha        float64  `json:"alpha"`
	Conclusion   string   `json:"conclusion"`
}

// TaskOutcome is one common task.
type TaskOutcome struct {
	Task  string   `json:"task"`
	Class string   `json:"class"`
	PassA int      `json:"pass_a"`
	NA    int      `json:"n_a"`
	PassB int      `json:"pass_b"`
	NB    int      `json:"n_b"`
	RateA float64  `json:"rate_a"`
	RateB float64  `json:"rate_b"`
	Pair  *RunPair `json:"pair,omitempty"` // representative runs; set for flipped and moved tasks
}

// RunPair is one run of each version on a task. PairID is set when both runs
// belong to the same experiment pair, so the run comparison view shows them
// side by side.
type RunPair struct {
	A      RunRef `json:"a"`
	B      RunRef `json:"b"`
	PairID string `json:"pair_id,omitempty"`
}

// RunRef points at one sealed run.
type RunRef struct {
	RunID   string `json:"run_id"`
	ExpID   string `json:"exp_id"`
	PairID  string `json:"pair_id"`
	Outcome string `json:"outcome"`
}

// MetricDelta is one metric over the common tasks, paired per task: A and B
// are the means over the tasks where both versions observed it.
type MetricDelta struct {
	Metric         string   `json:"metric"`
	HigherIsBetter bool     `json:"higher_is_better"`
	A              *float64 `json:"a"`
	B              *float64 `json:"b"`
	Delta          *float64 `json:"delta"` // B minus A
	Tasks          int      `json:"tasks"` // tasks with an observed value on both sides
	UnknownA       int      `json:"unknown_a"`
	UnknownB       int      `json:"unknown_b"`
	Direction      string   `json:"direction"`
}

// GateChange is one gate that fired in either version on the common tasks.
// New is true when it fired in B and never in A. Tasks lists the tasks where
// it fired in B and not in A.
type GateChange struct {
	Gate   string   `json:"gate"`
	FiredA int      `json:"fired_a"`
	FiredB int      `json:"fired_b"`
	New    bool     `json:"new"`
	Tasks  []string `json:"tasks"`
}

// VersionList is every config hash the run folder knows, for a picker.
type VersionList struct {
	Versions []VersionInfo `json:"versions"`
}

// VersionInfo is one config hash with the arms that ran it.
type VersionInfo struct {
	ConfigHash string    `json:"config_hash"`
	AgentID    string    `json:"agent_id"`
	Label      string    `json:"label"` // newest arm's label
	Suites     []string  `json:"suites"`
	Arms       []ArmRef  `json:"arms"`        // newest first
	SealedRuns int       `json:"sealed_runs"` // sealed runs, not yet seal-verified
	LatestAt   time.Time `json:"latest_at"`   // newest manifest that holds it
}

// ArmRef is one experiment arm that resolved to a config hash.
type ArmRef struct {
	ExpID     string    `json:"exp_id"`
	Arm       string    `json:"arm"`
	Label     string    `json:"label"`
	Suite     string    `json:"suite"`
	CreatedAt time.Time `json:"created_at"`
}

// Diff answers one query: it resolves the two versions, picks the suite,
// reads each version's verified runs on the suite and condition, and
// compares configs and outcomes. Two versions with no common task are an
// error wrapping ErrBadQuery that says what each version ran and what was
// left out, since a config diff alone cannot say which tasks flipped.
func Diff(ctx context.Context, src Source, contents ContentSource, q Query) (*Report, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	arms := collectArms(src)
	a, b, err := resolvePair(arms, q)
	if err != nil {
		return nil, err
	}
	suite, err := pickSuite(arms, a, b, q.Suite)
	if err != nil {
		return nil, err
	}
	a.rec, b.rec = armOnSuite(arms, a, suite), armOnSuite(arms, b, suite)
	ra, err := collectRuns(ctx, src, arms, a, suite, q.Condition, q.Synthetic)
	if err != nil {
		return nil, err
	}
	rb, err := collectRuns(ctx, src, arms, b, suite, q.Condition, q.Synthetic)
	if err != nil {
		return nil, err
	}
	outcome := compareOutcomes(ra, rb, q.Alpha)
	if outcome.CommonTasks == 0 {
		hint := ""
		if !q.Synthetic && (ra.excluded[ReasonSynthetic] > 0 || rb.excluded[ReasonSynthetic] > 0) {
			hint = "; ask for synthetic runs (synthetic=1, CLI -synthetic) to compare them"
		}
		return nil, fmt.Errorf("%w: versions a and b share no task with verified runs on %s under %s (a: %s; b: %s)%s",
			ErrBadQuery, suite, q.Condition, ranText(ra), ranText(rb), hint)
	}
	cfg, err := DiffConfigs(a.rec.arm, b.rec.arm, q.Condition, contents)
	if err != nil {
		return nil, err
	}
	r := &Report{
		A: version(a, ra), B: version(b, rb), Suite: suite, Condition: q.Condition, Synthetic: q.Synthetic,
		Config: cfg, Outcome: outcome,
	}
	r.Summary = Summarize(r)
	return r, nil
}

// Normalize fills the defaults (condition clean, alpha 0.05) and checks the
// suite and condition. Its error wraps ErrBadQuery.
func (q Query) Normalize() (Query, error) {
	q.A, q.B, q.Agent = strings.TrimSpace(q.A), strings.TrimSpace(q.B), strings.TrimSpace(q.Agent)
	q.Suite, q.Condition = strings.TrimSpace(q.Suite), strings.TrimSpace(q.Condition)
	if q.Condition == "" {
		q.Condition = schemas.ConditionClean
	}
	if q.Alpha == 0 {
		q.Alpha = DefaultAlpha
	}
	if id, version, ok := strings.Cut(q.Suite, "@"); q.Suite != "" && (!ok || !validToken(id) || !validToken(version)) {
		return q, fmt.Errorf("%w: suite %q is not id@version", ErrBadQuery, q.Suite)
	}
	if !validToken(q.Condition) {
		return q, fmt.Errorf("%w: condition %q is not a condition id", ErrBadQuery, q.Condition)
	}
	if !(q.Alpha > 0 && q.Alpha < 1) {
		return q, fmt.Errorf("%w: alpha %v is not in (0, 1)", ErrBadQuery, q.Alpha)
	}
	return q, nil
}

// armOnSuite returns the arm whose config the diff shows for a version: its
// own arm when picked by <exp_id>:<arm>, else the newest arm with its hash on
// the suite.
func armOnSuite(arms []*armRecord, r resolved, suite string) *armRecord {
	if r.expID != "" {
		return r.rec
	}
	for _, a := range arms {
		if a.hash == r.hash && a.ref.Suite == suite {
			return a
		}
	}
	return r.rec
}

// version builds one side's header from its selection and runs.
func version(r resolved, runs *versionRuns) Version {
	v := Version{
		Selector: r.selector, ConfigHash: r.hash, AgentID: r.rec.arm.Config.ID, Label: r.rec.ref.Label,
		ExpID: r.expID, Arm: r.arm, Experiments: runs.experiments, Runs: runs.runs, Excluded: []Exclusion{},
	}
	if v.Experiments == nil {
		v.Experiments = []string{}
	}
	for reason, n := range runs.excluded {
		v.Excluded = append(v.Excluded, Exclusion{Reason: reason, Runs: n})
	}
	slices.SortFunc(v.Excluded, func(x, y Exclusion) int { return cmp.Compare(x.Reason, y.Reason) })
	return v
}

// ranText says what a version ran, for the no-common-task error.
func ranText(v *versionRuns) string {
	s := fmt.Sprintf("%d task(s), %d verified run(s)", len(v.byTask), v.runs)
	reasons := make([]string, 0, len(v.excluded))
	for reason := range v.excluded {
		reasons = append(reasons, reason)
	}
	slices.Sort(reasons)
	for _, reason := range reasons {
		s += fmt.Sprintf(", %d left out: %s", v.excluded[reason], reason)
	}
	return s
}

// validToken reports whether s is 1 to 128 letters, digits, '-', '_' or '.'.
func validToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, c := range s {
		ok := c == '-' || c == '_' || c == '.' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}
