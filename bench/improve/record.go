package improve

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/stats"
)

// DefaultDir is where improvement runs live under the runs root.
const DefaultDir = ".improvements"

// Files in an improvement's folder.
const (
	RecordFile    = "improvement.json"
	ProtectedFile = "protected.json" // never served: what the improver must not learn
)

// Improvement statuses.
const (
	StatusPlanned  = "planned"
	StatusRunning  = "running"
	StatusAwaiting = "awaiting_approval"
	StatusDone     = "done"
	StatusStopped  = "stopped"
	StatusFailed   = "failed"
	// StatusInterrupted is a run the server restart left behind; Resume
	// continues it from its last saved step.
	StatusInterrupted = "interrupted"
)

// Round stages and outcomes (Round.Stage).
const (
	StageImprove = "improve"
	StageScreen  = "screen"
	StageAudit   = "audit"
	StageSealed  = "sealed"
	StageDone    = "done"
)

// Round decisions.
const (
	DecisionCommitted = "committed"
	DecisionRejected  = "rejected"
)

// ErrNotFound is returned for an unknown improvement or round.
var ErrNotFound = errors.New("improvement not found")

// ErrBusy reports an improvement that is already running.
var ErrBusy = errors.New("improvement is already running")

// ErrOffline reports a server that cannot launch runs (serve -offline).
var ErrOffline = errors.New("this server cannot launch improvement runs (offline: no gateway)")

// ErrState reports an action the improvement's state does not allow.
var ErrState = errors.New("not allowed in this state")

// ErrBadImprover marks a spec refused at create because its improver
// cannot run (A1.5, A1.6); the error's text is the executor's sentence.
var ErrBadImprover = errors.New("the improver cannot run")

// badImprover wraps an executor's refusal so it reads as its own sentence
// and still matches ErrBadImprover.
type badImprover struct{ err error }

func (e badImprover) Error() string   { return e.err.Error() }
func (e badImprover) Unwrap() []error { return []error{ErrBadImprover, e.err} }

var idRe = regexp.MustCompile(`^imp_[a-z0-9-]{1,64}$`)

// ValidID reports whether id is an improvement id.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Record is an improvement run as stored in improvement.json.
type Record struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Spec       Spec           `json:"spec"`
	SpecHash   string         `json:"spec_hash"`
	Status     string         `json:"status"`
	Reason     string         `json:"reason"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	CreatedBy  string         `json:"created_by"`
	Splits     Splits         `json:"splits"`
	Base       Version        `json:"base"`
	Incumbent  Version        `json:"incumbent"`
	Baseline   *Baseline      `json:"baseline,omitempty"`
	Rounds     []*Round       `json:"rounds"`
	Evidence   []EvidenceItem `json:"evidence"` // the incumbent's latest dev results: the next improver's input
	AuditLooks int            `json:"audit_looks"`
	Controls   *Controls      `json:"controls,omitempty"`
	Verdict    *Verdict       `json:"verdict,omitempty"`
	Error      string         `json:"error,omitempty"`

	// Looks are the gate looks whose test ran, in order (A2.2).
	Looks []LookRecord `json:"looks,omitempty"`
	// Keep is the keep set the next round's proposals draw from (A3.4).
	Keep []KeepEntry `json:"keep,omitempty"`
	// Estimate is the pre-run estimate stored at creation (A2.8), and
	// EstimateHash its utils.HashJSON.
	Estimate     *Estimate `json:"estimate,omitempty"`
	EstimateHash string    `json:"estimate_hash,omitempty"`
	// Final is the claim's final interleaved experiment (A2.4), and
	// Extension the second look when it ran (A2.11).
	Final     *FinalClaim `json:"final,omitempty"`
	Extension *FinalClaim `json:"extension,omitempty"`
	// SpentUSD is the whole run's spend so far, for the hard stop (A2.10).
	SpentUSD float64 `json:"spent_usd,omitempty"`
	// Revision counts continues (A2.11).
	Revision int `json:"revision,omitempty"`
	// Study is the study this run is a loop of; InStudy skips the final
	// experiment and the controls (A4.1).
	Study   string `json:"study,omitempty"`
	InStudy bool   `json:"in_study,omitempty"`
	// SkillsOpened maps each skill of the incumbent to the last round a
	// practice run of the incumbent opened it (or the round it was first
	// seen), for the retire_skill pruning edits of A3.3.
	SkillsOpened map[string]int `json:"skills_opened,omitempty"`
	// StudyLimitUSD is the total limit of the study this run is a loop of,
	// and StudyPriorUSD what the study's earlier loops spent: the hard stop
	// keeps the whole study under its total (Spec.Budget is the loop's share).
	StudyLimitUSD float64 `json:"study_limit_usd,omitempty"`
	StudyPriorUSD float64 `json:"study_prior_usd,omitempty"`
}

// Baseline is version 0 before round 1: dev (the first evidence) and
// sealed (round 0 of the sealed curve).
type Baseline struct {
	Dev       ArmScore `json:"dev"`
	Sealed    ArmScore `json:"sealed"`
	DevExp    string   `json:"dev_exp"`
	SealedExp string   `json:"sealed_exp"`
	CostUSD   float64  `json:"cost_usd"`
	SealedUSD float64  `json:"sealed_usd"`
}

// Round is one improvement round.
type Round struct {
	N         int            `json:"n"`
	Stage     string         `json:"stage"`
	Parent    Version        `json:"parent"`
	Candidate *Version       `json:"candidate,omitempty"`
	Improver  *ImproveResult `json:"improver,omitempty"`
	Diff      []FileDiff     `json:"diff"`
	Gates     []GateResult   `json:"gates"`
	Tripwires []string       `json:"tripwires"`
	Approvals []Approval     `json:"approvals"`
	Screen    *Comparison    `json:"screen,omitempty"`
	Audit     *Comparison    `json:"audit,omitempty"`
	Sealed    *ArmScore      `json:"sealed,omitempty"`
	Decision  string         `json:"decision"`
	Why       string         `json:"why"`
	Cost      RoundCost      `json:"cost"`
	// The screen's dev results per arm: the next improver's evidence,
	// whichever version the audit keeps. Kept so a resumed round has them.
	CandEvidence   []EvidenceItem `json:"cand_evidence,omitempty"`
	ParentEvidence []EvidenceItem `json:"parent_evidence,omitempty"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`

	// ParentObj is the incumbent's candidate object; Measures maps a phase
	// to its measure object, written before the experiment starts (A1.2).
	ParentObj string            `json:"parent_obj,omitempty"`
	Measures  map[string]string `json:"measures,omitempty"`
	// Candidates are the round's ideas (A3.4), Stages the halving levels
	// (A2.3), Winner the id of the candidate that went to the gate.
	Candidates []CandidateRun `json:"candidates,omitempty"`
	Stages     []StageResult  `json:"stages,omitempty"`
	Winner     string         `json:"winner,omitempty"`
	// Commit is the gate decision (A2.2).
	Commit *CommitDecision `json:"commit,omitempty"`
}

// CandidateRun is one idea of a round: its slot, kind, the version it
// froze as, its edits and how far it got.
type CandidateRun struct {
	ID        string        `json:"id"`
	Slot      int           `json:"slot"`
	Kind      string        `json:"kind"`             // mutation | merge | restart | null
	Source    string        `json:"source,omitempty"` // the version edited
	Version   *Version      `json:"version,omitempty"`
	Obj       string        `json:"obj,omitempty"` // candidate object hash
	Edits     []AppliedEdit `json:"edits,omitempty"`
	Gates     []GateResult  `json:"gates,omitempty"`
	Tripwires []string      `json:"tripwires,omitempty"`
	Dropped   string        `json:"dropped,omitempty"` // plain reason it stopped
	DevDiff   float64       `json:"dev_diff,omitempty"`
	DevTasks  int           `json:"dev_tasks,omitempty"`
	CostUSD   float64       `json:"cost_usd,omitempty"`
}

// StageResult is one level of the practice screen.
type StageResult struct {
	Level     int      `json:"level"`
	Tasks     int      `json:"tasks"`
	ExpID     string   `json:"exp_id,omitempty"`
	Survivors []string `json:"survivors,omitempty"`
}

// KeepEntry is one member of the keep set.
type KeepEntry struct {
	Ref     string   `json:"ref"`
	Obj     string   `json:"obj,omitempty"`
	Led     []string `json:"led,omitempty"` // practice tasks where it beats the incumbent
	DevDiff float64  `json:"dev_diff,omitempty"`
}

// FinalClaim is the final (or extension) experiment the claim reads.
type FinalClaim struct {
	ExpID    string     `json:"exp_id,omitempty"`
	Arms     []string   `json:"arms,omitempty"`
	Trials   int        `json:"trials,omitempty"`
	N        int        `json:"n,omitempty"`
	NNonzero int        `json:"n_nonzero,omitempty"`
	Gain     float64    `json:"gain,omitempty"`
	PUpper   float64    `json:"p_upper,omitempty"`
	PLower   float64    `json:"p_lower,omitempty"`
	MDE      float64    `json:"mde,omitempty"`
	Level    float64    `json:"level,omitempty"`
	CI80     [2]float64 `json:"ci80,omitempty"`
	CI95     [2]float64 `json:"ci95,omitempty"`
}

// RetriesControl is version 0 retried on visible failures with up to the
// loop's spend (A2.6).
type RetriesControl struct {
	K        int        `json:"k,omitempty"`
	Attempts int        `json:"attempts,omitempty"`
	LoopUSD  float64    `json:"loop_usd,omitempty"`
	UsedUSD  float64    `json:"used_usd,omitempty"`
	Score    float64    `json:"score,omitempty"`
	Diff     float64    `json:"diff,omitempty"`
	CI80     [2]float64 `json:"ci80,omitempty"`
}

// NullCursor is where the null improver stopped, so it resumes (A1.7).
type NullCursor struct {
	Round   int     `json:"round"`
	Version Version `json:"version"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// ControlLine is one control as the verdict shows it.
type ControlLine struct {
	Score float64    `json:"score,omitempty"`
	Diff  float64    `json:"diff,omitempty"`
	CI80  [2]float64 `json:"ci80,omitempty"`
	Note  string     `json:"note,omitempty"`
}

// ContinueOffer is the second look a done run may buy (A2.11).
type ContinueOffer struct {
	ExtraTrials int        `json:"extra_trials,omitempty"`
	NewTasks    int        `json:"new_tasks,omitempty"`
	Runs        int        `json:"runs,omitempty"`
	USD         [2]float64 `json:"usd,omitempty"`
	CanConfirm  float64    `json:"can_confirm,omitempty"` // points
	Text        string     `json:"text,omitempty"`
}

// Approval is a person's override of a round's tripwires, recorded in red.
type Approval struct {
	Stage  string    `json:"stage"`
	By     string    `json:"by"`
	ByName string    `json:"by_name,omitempty"` // how By reads in an answer (a member's display name); never stored
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// RoundCost is a round's spend by phase.
type RoundCost struct {
	Improver float64 `json:"improver"`
	Screen   float64 `json:"screen"`
	Audit    float64 `json:"audit"`
	Sealed   float64 `json:"sealed"`
}

// Total is the round's spend.
func (c RoundCost) Total() float64 { return c.Improver + c.Screen + c.Audit + c.Sealed }

// ArmScore is one arm's results on one split.
type ArmScore struct {
	Mean    float64            `json:"mean"`
	Runs    int                `json:"runs"`
	Known   int                `json:"known"`
	Vetoed  int                `json:"vetoed"`
	PerTask map[string]float64 `json:"per_task"` // mean success per task
}

// Comparison is a paired incumbent (A) vs candidate (B) experiment.
type Comparison struct {
	ExpID          string            `json:"exp_id"`
	A              ArmScore          `json:"a"`
	B              ArmScore          `json:"b"`
	MeanDiff       float64           `json:"mean_diff"`
	Pairs          int               `json:"pairs"`
	P              *float64          `json:"p,omitempty"`       // screen: two-sided sign-flip over tasks
	EValue         *stats.EValueTest `json:"e_value,omitempty"` // audit: the commit test
	Greedy         bool              `json:"greedy"`            // audit: what keep-if-better would have done
	HoneypotPasses int               `json:"honeypot_passes"`
	ChannelsA      []string          `json:"channels_a,omitempty"`
	ChannelsB      []string          `json:"channels_b,omitempty"`
}

// Controls are the end-of-loop control arms on the sealed split.
type Controls struct {
	BudgetMatched *BudgetMatched `json:"budget_matched,omitempty"`
	Null          *NullControl   `json:"null,omitempty"`
	CostUSD       float64        `json:"cost_usd"`

	Retries    *RetriesControl `json:"retries,omitempty"`
	NullCursor *NullCursor     `json:"null_cursor,omitempty"`
}

// BudgetMatched is version 0 given the loop's spend as extra trials.
type BudgetMatched struct {
	ExpID       string   `json:"exp_id"`
	Trials      int      `json:"trials"`
	LoopUSD     float64  `json:"loop_usd"`
	Score       ArmScore `json:"score"`
	BestOfK     float64  `json:"best_of_k"` // mean over tasks of the best trial
	BandLow     float64  `json:"band_low"`
	BandHigh    float64  `json:"band_high"`
	PerTrialUSD float64  `json:"per_trial_usd"`
	CostUSD     float64  `json:"cost_usd"`
}

// NullControl is the null improver: the same rounds and budget, no
// evidence, every edit kept.
type NullControl struct {
	ExpID   string   `json:"exp_id"`
	Final   Version  `json:"final"`
	Score   ArmScore `json:"score"`
	CostUSD float64  `json:"cost_usd"`

	// Diff is final minus the null final per task, with its 80% interval (A2.7).
	Diff float64    `json:"diff,omitempty"`
	CI80 [2]float64 `json:"ci80,omitempty"`
}

// Verdict is the claim the run supports.
type Verdict struct {
	State       string   `json:"state"` // a Verdict* state; not_supported and inconclusive are old states
	Headline    string   `json:"headline"`
	SealedGain  float64  `json:"sealed_gain"`
	P           *float64 `json:"p,omitempty"`
	DevGain     float64  `json:"dev_gain"`
	GenGap      float64  `json:"generalization_gap"` // dev gain minus sealed gain
	Commits     int      `json:"commits"`
	Rejects     int      `json:"rejects"`
	Tripwires   int      `json:"tripwires"`
	Overrides   int      `json:"overrides"`
	GreedyExtra int      `json:"greedy_extra"` // commits keep-if-better would have made that the gate refused
	TotalUSD    float64  `json:"total_usd"`
	GainPerUSD  *float64 `json:"gain_per_usd,omitempty"`
	Notes       []string `json:"notes"`

	// The claim's numbers (A2.5). SealedGain equals Gain and P points at
	// PUpper, for old readers.
	Gain      float64        `json:"gain,omitempty"`
	CI80      [2]float64     `json:"ci80,omitempty"`
	CI95      [2]float64     `json:"ci95,omitempty"`
	PUpper    float64        `json:"p_upper,omitempty"`
	PLower    float64        `json:"p_lower,omitempty"`
	N         int            `json:"n,omitempty"`
	NNonzero  int            `json:"n_nonzero,omitempty"`
	MDE       float64        `json:"mde,omitempty"`
	Level     float64        `json:"level,omitempty"`
	Teacher   string         `json:"teacher,omitempty"`
	Null      *ControlLine   `json:"null,omitempty"`
	Retries   *ControlLine   `json:"retries,omitempty"`
	Continue  *ContinueOffer `json:"continue,omitempty"`
	Sentences []string       `json:"sentences,omitempty"`
}

// Verdict states.
const (
	VerdictSupported    = "supported"
	VerdictNotSupported = "not_supported"
	VerdictInconclusive = "inconclusive"
	VerdictNoChange     = "no_change"

	VerdictLikelyBetter       = "likely_better"
	VerdictInsufficientSample = "insufficient_sample"
	VerdictNoEvidence         = "no_evidence"
	VerdictWorse              = "worse"
	VerdictNotClaimable       = "not_claimable"
)

func (r *Runner) dir(id string) string { return filepath.Join(r.Root, id) }

func (r *Runner) load(id string) (*Record, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(r.dir(id), RecordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("improvement %s: %w", id, err)
	}
	return &rec, nil
}

func (r *Runner) save(rec *Record) error {
	rec.UpdatedAt = r.now()
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir(rec.ID), RecordFile), append(b, '\n'), 0o644)
}

func (r *Runner) loadProtected(id string) (Protected, error) {
	var p struct {
		TaskIDs  []string `json:"task_ids"`
		Secrets  []string `json:"secrets"`
		Canaries []string `json:"canaries"`
	}
	b, err := os.ReadFile(filepath.Join(r.dir(id), ProtectedFile))
	if err != nil {
		return Protected{}, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return Protected{}, err
	}
	return Protected{TaskIDs: p.TaskIDs, Secrets: p.Secrets, Canaries: p.Canaries}, nil
}

func (r *Runner) saveProtected(id string, p Protected) error {
	b, err := json.Marshal(map[string][]string{"task_ids": p.TaskIDs, "secrets": p.Secrets, "canaries": p.Canaries})
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir(id), ProtectedFile), b, 0o600)
}

func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close(), os.Remove(tmp))
	}
	if err := f.Chmod(perm); err != nil {
		return errors.Join(err, f.Close(), os.Remove(tmp))
	}
	if err := f.Close(); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

// score folds runs of one arm into an ArmScore.
func score(runs []RunScore, arm int) ArmScore {
	s := ArmScore{PerTask: map[string]float64{}}
	sum := map[string]float64{}
	cnt := map[string]int{}
	for _, x := range runs {
		if x.Arm != arm {
			continue
		}
		s.Runs++
		if len(x.Vetoes) > 0 {
			s.Vetoed++
		}
		if !x.Known {
			continue
		}
		s.Known++
		sum[x.Task] += clamp01(x.Success)
		cnt[x.Task]++
	}
	tot := 0.0
	for t, n := range cnt {
		s.PerTask[t] = sum[t] / float64(n)
		tot += s.PerTask[t]
	}
	if len(cnt) > 0 {
		s.Mean = tot / float64(len(cnt))
	}
	return s
}

// pairDiffs are B minus A per (task, trial) where both are known, ordered
// by task then trial: an order fixed before any value is seen.
func pairDiffs(runs []RunScore, tasks []string) []float64 {
	type key struct {
		task  string
		trial int
	}
	a, b := map[key]float64{}, map[key]float64{}
	trials := map[string][]int{}
	for _, x := range runs {
		if !x.Known {
			continue
		}
		k := key{x.Task, x.Trial}
		switch x.Arm {
		case 0:
			a[k] = clamp01(x.Success)
		case 1:
			b[k] = clamp01(x.Success)
		}
		if !slices.Contains(trials[x.Task], x.Trial) {
			trials[x.Task] = append(trials[x.Task], x.Trial)
		}
	}
	out := []float64{}
	for _, t := range tasks {
		ts := trials[t]
		slices.Sort(ts)
		for _, tr := range ts {
			k := key{t, tr}
			va, okA := a[k]
			vb, okB := b[k]
			if okA && okB {
				out = append(out, vb-va)
			}
		}
	}
	return out
}

// taskDiffs are per-task mean differences B minus A, for tasks both arms
// have.
func taskDiffs(a, b ArmScore, tasks []string) []float64 {
	out := []float64{}
	for _, t := range tasks {
		va, okA := a.PerTask[t]
		vb, okB := b.PerTask[t]
		if okA && okB {
			out = append(out, vb-va)
		}
	}
	return out
}

// TaskMeans are arm's per-task mean success (clamped to [0, 1]) over its
// known runs, for the tasks listed; a task with no known run is left out.
// The count is the tasks of the list that have no known run on this arm.
func TaskMeans(runs []RunScore, arm int, tasks []string) (map[string]float64, int) {
	want := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		want[t] = true
	}
	sum := map[string]float64{}
	cnt := map[string]int{}
	for _, x := range runs {
		if x.Arm != arm || !x.Known || !want[x.Task] {
			continue
		}
		sum[x.Task] += clamp01(x.Success)
		cnt[x.Task]++
	}
	out := make(map[string]float64, len(cnt))
	missing := 0
	for t := range want {
		if n := cnt[t]; n > 0 {
			out[t] = sum[t] / float64(n)
		} else {
			missing++
		}
	}
	return out, missing
}

// TaskDiffs are b minus a per task, for the tasks both maps hold, in sorted
// task order (A2.1).
func TaskDiffs(a, b map[string]float64, tasks []string) []float64 {
	ts := slices.Clone(tasks)
	slices.Sort(ts)
	ts = slices.Compact(ts)
	out := []float64{}
	for _, t := range ts {
		va, okA := a[t]
		vb, okB := b[t]
		if okA && okB {
			out = append(out, vb-va)
		}
	}
	return out
}

func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(0, math.Min(1, x))
}
