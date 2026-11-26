package improve

import "context"

// RunRef names one counted run of a scoring experiment.
type RunRef struct {
	ExpID string `json:"exp_id"`
	RunID string `json:"run_id"`
	Task  string `json:"task"`
	Trial int    `json:"trial"`
	Arm   int    `json:"arm"`
	Dir   string `json:"-"` // sealed run folder on disk
}

// EvidenceLimits cap what the improver reads (A3.1).
type EvidenceLimits struct {
	DigestTokensPerRun int // 6000
	DigestTokensTotal  int // 60000
	RawBytesPerRun     int // 2 << 20
	RawBytesTotal      int // 50 << 20
	Clusters           int // 6
	Exemplars          int // 2
}

type EvidenceRequest struct {
	ImprovementID string
	Round         int
	Runs          []RunRef // dev runs only; the runner never passes another split
	Prot          Protected
	Limits        EvidenceLimits
	History       []HistoryEntry // earlier candidates: edits, screen deltas, round sentence
	Playbook      PlaybookObject
}

// EvidenceDoc is what is mounted under /heimdall/improve/ (A3.1).
type EvidenceDoc struct {
	EvidenceJSON []byte            // evidence.json
	Files        map[string][]byte // relative path under /heimdall/improve/ -> content (runs/<task>/<trial>/digest.md, transcript.jsonl, diff.patch)
	Items        []EvidenceItem
}

type EvidenceBuilder interface {
	Build(ctx context.Context, req EvidenceRequest) (*EvidenceDoc, error)
}

// ProposalSpec is one candidate slot of a round (A3.4).
type ProposalSpec struct {
	Slot   int
	Kind   string  // mutation | merge | restart | null
	Source Version // the version edited (mutation, restart: version 0)
	Second *Version
	Seed   int64
}

type Proposer interface {
	Plan(rec *Record, round int) []ProposalSpec
}

// Screener is Sequential Halving on dev (A2.3).
type Screener interface {
	Levels(k int, dev []string, seed int64) [][]string
	Cut(in CutInput) CutResult
}
type CutInput struct {
	Diffs map[string][]float64 // candidate id -> per-task differences (candidate minus incumbent) so far
	Words map[string]int       // candidate id -> bundle word count
	Cost  map[string]float64   // candidate id -> screen spend so far
	Keep  int                  // survivors wanted
}
type CutResult struct {
	Keep    []string          // in rank order
	Dropped map[string]string // candidate id -> plain reason
}

type CommitDecision struct {
	Commit   bool    `json:"commit"`
	P        float64 `json:"p"` // one-sided, candidate better
	Level    float64 `json:"level"`
	Mean     float64 `json:"mean"` // mean per-task difference
	N        int     `json:"n"`    // tasks with a result on both arms
	NNonzero int     `json:"n_nonzero"`
	Missing  int     `json:"missing"` // gate tasks with no result on an arm
	Skipped  bool    `json:"skipped"` // the test did not run; no alpha spent
	Why      string  `json:"why"`
}

type CommitRule interface {
	// Level is the level of the next look given the looks so far (A2.2).
	Level(spec *Spec, looks []LookRecord) float64
	Decide(d []float64, missing, total int, level float64) CommitDecision
}

type ClaimRule interface {
	Decide(in ClaimInput) *Verdict
}

type EstimateInput struct {
	Spec        *Spec
	Pool        int
	Splits      SplitCounts
	PerRunUSD   float64
	RunSource   string // forecast | history | assumed
	PerRunMin   float64
	P0          float64 // incumbent pass rate; 0.5 when unknown
	W           float64 // within-task Bernoulli variance; 0 when unknown (then p0(1-p0)(1-rho))
	Rho         float64 // 0.5 when unknown
	HistoryRuns int
}
type Estimator interface {
	Estimate(in EstimateInput) Estimate
}

type TransferSpec struct {
	Suite  string `json:"suite,omitempty" yaml:"suite"`
	Model  string `json:"model,omitempty" yaml:"model"`
	Trials int    `json:"trials" yaml:"trials"`
}
type ReplicationPlan struct {
	Loops       int           `json:"loops" yaml:"loops"`
	Seeds       []int64       `json:"seeds" yaml:"seeds"`
	Repartition bool          `json:"repartition" yaml:"repartition"`
	Transfer    *TransferSpec `json:"transfer,omitempty" yaml:"transfer"`
}

// Defaults: the implementations the runner uses when a field is nil.
// DefaultScreener and DefaultEstimator are nil in this file: the halving
// screener and the formula estimator live in the files of the runner and the
// estimator, whose init functions set them.
var (
	DefaultScreener   Screener
	DefaultCommitRule CommitRule = fallbackSignFlip{}
	DefaultClaimRule  ClaimRule  = signFlipClaim{}
	DefaultEstimator  Estimator
)

// ClaimInput is what the claim rule reads: the final experiment's per-task
// means of version 0 (A) and the final version (B) on the held-out tasks,
// and the facts of the loop that decide the first two verdict rules (A2.5).
type ClaimInput struct {
	Spec          *Spec
	Rec           *Record            // passed to Wording; may be nil
	A, B          map[string]float64 // per-task mean success, from TaskMeans
	Tasks         []string           // the held-out tasks, sorted
	Missing       int                // held-out tasks with no known run on an arm
	Committed     bool               // at least one round kept a new version
	Approved      bool               // a person approved a paused check
	ApprovedRound int                // the first round with an approval
	Level         float64            // a_main; 0 means from Spec.Claim
	Trials        int                // trials per arm per task in the final experiment
	Version       int                // the final version's number, for the wording
}

// RunExtend runs the second look of a done run with more budget (A2.11).
// The runner's final file sets it; nil means this build cannot continue
// improvement runs.
var RunExtend func(r *Runner, ctx context.Context, id string, extraTrials int, newTasks bool, extraUSD float64) (*Record, error)

// SplitCounts are the sizes of a partition, as the estimator reports them.
type SplitCounts struct {
	Practice   int `json:"practice"`
	Gate       int `json:"gate"`
	HeldOut    int `json:"held_out"`
	Impossible int `json:"impossible"`
}

// Estimate is the pre-run estimate of an improvement run (A2.8).
type Estimate struct {
	OK         bool               `json:"ok"`
	Pool       int                `json:"pool"`
	Splits     SplitCounts        `json:"splits"`
	Runs       EstimateRuns       `json:"runs"`
	USD        EstimateUSD        `json:"usd"`
	Minutes    EstimateMinutes    `json:"minutes"`
	Detectable EstimateDetectable `json:"detectable"`
	Assumed    []string           `json:"assumed"`
	Refusals   []EstimateNote     `json:"refusals"`
	Warnings   []EstimateNote     `json:"warnings"`
}

// EstimateRuns are the planned run counts of an estimate.
type EstimateRuns struct {
	Baseline     int `json:"baseline"`
	Improver     int `json:"improver"`
	Practice     int `json:"practice"`
	Gate         int `json:"gate"`
	HeldOutCurve int `json:"held_out_curve"`
	Final        int `json:"final"`
	Total        int `json:"total"`
}

// EstimateUSD is the money of an estimate.
type EstimateUSD struct {
	Low          float64 `json:"low"`
	Expected     float64 `json:"expected"`
	High         float64 `json:"high"`
	Limit        float64 `json:"limit"`
	PerRun       float64 `json:"per_run"`
	PerRunSource string  `json:"per_run_source"`
}

// EstimateMinutes is the wall time range of an estimate.
type EstimateMinutes struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// EstimateDetectable are the smallest detectable gains, in points.
type EstimateDetectable struct {
	GatePoints    float64 `json:"gate_points"`
	HeldOutPoints float64 `json:"held_out_points"`
}

// EstimateNote is one refusal or warning of an estimate.
type EstimateNote struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HistoryEntry is one earlier candidate, as the improver reads it in
// history.json.
type HistoryEntry struct {
	Round        int      `json:"round"`
	Slot         int      `json:"slot"`
	Kind         string   `json:"kind"`
	Edits        []string `json:"edits"`
	PracticeDiff float64  `json:"practice_diff"`
	Sentence     string   `json:"sentence"`
}

// Limits cap what one candidate's edits may do (A3.3).
type Limits struct {
	MaxEdits             int `json:"max_edits"`
	PlaybookWordsPerFile int `json:"playbook_words_per_file"`
	BundleWords          int `json:"bundle_words"`
	Skills               int `json:"skills"`
	SkillWords           int `json:"skill_words"`
	ItemWords            int `json:"item_words"`
}

// DefaultLimits are the caps of A3.3.
var DefaultLimits = Limits{8, 1500, 3000, 12, 600, 120}

// EditsFile is the improver's edits.json (A3.3).
type EditsFile struct {
	Rationale string `json:"rationale"`
	Edits     []Edit `json:"edits"`
	NoEdits   string `json:"no_edits,omitempty"` // why nothing was changed
}

// Edit is one change the improver asks for.
type Edit struct {
	Op            string   `json:"op"`                // add_item | edit_item | delete_item | add_skill | edit_skill | retire_skill | rewrite_file
	ID            string   `json:"id,omitempty"`      // pb-NNNN for edit_item, delete_item
	File          string   `json:"file,omitempty"`    // bundle path, or "@instructions"
	Section       string   `json:"section,omitempty"` // add_item: "Strategies" | "Pitfalls" | "Checks" | any short title
	Path          string   `json:"path,omitempty"`    // skills: .../<name>/SKILL.md under an editable folder
	Text          string   `json:"text,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`       // run refs from evidence.json
	PredictsFixed []string `json:"predicts_fixed,omitempty"` // practice task ids
	Reason        string   `json:"reason"`
}

// AppliedEdit is an edit with what Heimdall did with it.
type AppliedEdit struct {
	Edit
	Result string `json:"result"` // applied | merged_into:<id> | refused
	Why    string `json:"why,omitempty"`
}

// EditApplier applies an edit set to a bundle and its playbook (A3.3).
type EditApplier func(b BundleObject, pb PlaybookObject, e EditsFile, ed Editable, lim Limits, kind string, round int) (BundleObject, PlaybookObject, []AppliedEdit, error)

// Hooks other files of this package set from their init functions. Nil
// means the runner keeps today's behaviour: the improver's bundle is used as
// returned, evidence is the dev result items, and one mutation slot runs.
var (
	DefaultEditApplier     EditApplier
	DefaultEvidenceBuilder EvidenceBuilder
	DefaultProposer        Proposer
)

// Wording writes the verdict's headline and lines (A2.12). The wording file
// sets it; nil leaves the claim's numbers without sentences.
var Wording func(v *Verdict, rec *Record)
