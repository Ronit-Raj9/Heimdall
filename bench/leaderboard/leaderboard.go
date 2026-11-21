// Package leaderboard ranks agent configurations across experiments
// (docs/leaderboard.md). One entry is one resolved agent config hash; its
// runs are pooled from every experiment of one suite version under one
// condition. Entries are ordered by verified success with every other
// column beside it: there is no blended score.
//
// Ranking uncertainty is explicit. Each entry is compared with the leader by
// the exact sign-flip test on the tasks both ran, Holm-adjusted across
// entries, and lands in tier 1 (cannot be told apart from the leader) or
// tier 2 (significantly behind). Rank alone is never a claim that one agent
// beats another.
//
// The package is pure compute over the run folder's read side: it reads
// manifests, runs, metrics.json and proxy.ledger.jsonl through Source, and
// regrades through RegradeSource, and writes nothing.
package leaderboard

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

// Query values and defaults.
const (
	ScopeAll         = "all"    // each entry over its own tasks
	ScopeCommon      = "common" // every entry over the common tasks only
	SyntheticExclude = "exclude"
	SyntheticOnly    = "only"
	DefaultAlpha     = 0.05
	// GradeSourceOriginal reads every run as sealed; GradeSourceLatest reads
	// each experiment's newest regrade where it has a result for the run.
	GradeSourceOriginal = "original"
	GradeSourceLatest   = "latest"
	// MaxDefaultK caps the default k of pass^k: beyond a few tries in a row
	// the estimate says more about the sample than about the agent.
	MaxDefaultK = 3
	// SortMetric is the one metric entries are ordered by.
	SortMetric = "verified_success"
)

// Run files the board reads.
const (
	fileMetrics = "metrics.json"
	fileLedger  = "proxy.ledger.jsonl"
	fileGrade   = "grade.json"
)

// ErrBadQuery marks a query the board cannot answer as asked. The HTTP
// handler maps it to 400 and the CLI to a usage error.
var ErrBadQuery = errors.New("bad leaderboard query")

// Run is one run as the board reads it from the run index.
type Run struct {
	Slot   schemas.Slot
	Run    schemas.Run
	Sealed bool
	// Superseded marks a run an operator requeued: it never counts.
	Superseded bool
}

// Source is the read side of the run folder. handlers.LeaderboardSource
// adapts the API's read index to it. A Source that also implements
// RegradeSource can serve grade source latest.
type Source interface {
	Experiments() []string
	Manifest(expID string) (*schemas.Manifest, bool)
	// Runs returns every run of an experiment, retries and unsealed runs included.
	Runs(expID string) []Run
	// RunFile returns one file of a run folder. A missing file is an error
	// wrapping fs.ErrNotExist or schemas.ErrNotFound.
	RunFile(runID, name string) ([]byte, error)
	// LatestDecision returns the verdict decision of the experiment's newest
	// analysis, and false when it has none.
	LatestDecision(expID string) (schemas.Decision, bool)
}

// Query selects one board. Zero fields take the defaults: the newest suite
// with runs, the clean condition, scope all, live runs only, original
// grades, each entry's default k, alpha 0.05 and the current time.
type Query struct {
	Suite       string    // id@version
	Condition   string    // clean or a probe id
	Scope       string    // all | common
	Synthetic   string    // exclude | only
	GradeSource string    // original | latest
	K           int       // k of pass^k for every entry; 0: each entry's default (defaultK)
	Alpha       float64   // significance level for tiers
	Now         time.Time // GeneratedAt

	// Split is public (default) or held_out: the board counts runs of that
	// split's tasks only (split.go).
	Split string

	// Customer is scripted (default) or simulated: a simulated board ranks
	// only runs whose customer a pinned model played, of the simulator pin
	// Simulator when set (simuser.go).
	Customer  string
	Simulator string
}

// Normalize fills the defaults and checks every field. Its error wraps
// ErrBadQuery.
func (q Query) Normalize() (Query, error) {
	q.Condition = cmp.Or(strings.TrimSpace(q.Condition), schemas.ConditionClean)
	q.Scope = cmp.Or(strings.TrimSpace(q.Scope), ScopeAll)
	q.Synthetic = cmp.Or(strings.TrimSpace(q.Synthetic), SyntheticExclude)
	q.GradeSource = cmp.Or(strings.TrimSpace(q.GradeSource), GradeSourceOriginal)
	q.Suite = strings.TrimSpace(q.Suite)
	q.Customer = strings.TrimSpace(q.Customer) // empty is scripted
	q.Simulator = strings.TrimSpace(q.Simulator)
	if q.Alpha == 0 {
		q.Alpha = DefaultAlpha
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	switch {
	case q.Suite != "" && !validSuite(q.Suite):
		return q, fmt.Errorf("%w: suite %q is not id@version", ErrBadQuery, q.Suite)
	case !validToken(q.Condition):
		return q, fmt.Errorf("%w: condition %q is not a condition id", ErrBadQuery, q.Condition)
	case q.Scope != ScopeAll && q.Scope != ScopeCommon:
		return q, fmt.Errorf("%w: scope %q is not all or common", ErrBadQuery, q.Scope)
	case q.Customer != "" && q.Customer != CustomerScripted && q.Customer != CustomerSimulated:
		return q, fmt.Errorf("%w: customer %q is not scripted or simulated", ErrBadQuery, q.Customer)
	case q.Synthetic != SyntheticExclude && q.Synthetic != SyntheticOnly:
		return q, fmt.Errorf("%w: synthetic %q is not exclude or only", ErrBadQuery, q.Synthetic)
	case q.GradeSource != GradeSourceOriginal && q.GradeSource != GradeSourceLatest && q.GradeSource != GradeSourceReviewed:
		return q, fmt.Errorf("%w: grade source %q is not original, latest or reviewed", ErrBadQuery, q.GradeSource)
	case q.K < 0 || q.K == 1:
		return q, fmt.Errorf("%w: k %d is not 0 (each entry's default) or 2 or more", ErrBadQuery, q.K)
	case !(q.Alpha > 0 && q.Alpha < 1):
		return q, fmt.Errorf("%w: alpha %v is not in (0, 1)", ErrBadQuery, q.Alpha)
	case !validSplit(q.Split): // Suite health area (split.go)
		return q, fmt.Errorf("%w: split %q is not public or held_out", ErrBadQuery, q.Split)
	}
	return q, nil
}

// Build computes one board. Runs enter it when their slot's counted run is
// sealed and graded (the outcome scores: pass, fail, timeout, budget, crash,
// refusal, integrity_violation). Every other run of the board's suite and
// condition is counted in Excluded by reason: its outcome (infra_error,
// invalid_evaluator, unknown, aborted_infra), "not sealed", "seal mismatch"
// when src is a SealSource and the run's files no longer match its seal,
// "slot has more than one counted run", or "experiment verdict invalid" when
// the experiment's newest analysis decided invalid.
// The synthetic filter applies before anything is counted, so a live board
// never mentions scripted runs and the other way round. With grade source
// latest each experiment's runs are read from its newest regrade
// (readGradeSource) before they are graded or excluded.
func Build(ctx context.Context, src Source, q Query) (*schemas.Leaderboard, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	recs, err := collect(ctx, src)
	if err != nil {
		return nil, err
	}
	synthetic, err := classify(ctx, src, recs)
	if err != nil {
		return nil, err
	}
	kept := make([]*record, 0, len(recs))
	for _, r := range recs {
		if synthetic[r.key()] == (q.Synthetic == SyntheticOnly) && inSplit(r, q.Split) && onCustomerBoard(r, q) { // Suite health area: held-out board is separate (split.go)
			kept = append(kept, r)
		}
	}
	lb := &schemas.Leaderboard{
		Suites: suiteList(kept), Condition: q.Condition, Scope: q.Scope, Synthetic: q.Synthetic,
		Alpha: q.Alpha, SortMetric: SortMetric, GeneratedAt: q.Now.UTC(), GradeSource: q.GradeSource,
		Conditions: []string{}, CommonTasks: []string{}, Entries: []schemas.LeaderboardEntry{}, Excluded: []schemas.LeaderboardExclusion{},
	}
	lb.Suite = q.Suite
	lb.Split = boardSplit(q.Split) // Suite health area (split.go)
	if lb.Suite == "" && len(lb.Suites) > 0 {
		lb.Suite = lb.Suites[0]
	}
	var inSuite []*record
	for _, r := range kept {
		if r.exp.suite == lb.Suite {
			inSuite = append(inSuite, r)
		}
	}
	lb.Conditions = conditionList(inSuite)
	if err := readGradeSource(ctx, src, inSuite, q.GradeSource); err != nil {
		return nil, err
	}
	lb.GradeSources = gradeSourceList(inSuite, q.Condition)
	lb.ReviewedRuns, lb.ReviewChangedRuns = reviewCounts(inSuite, q.Condition) // human review (review.go)
	graded, excluded, err := grade(ctx, src, inSuite, q.Customer == CustomerSimulated)
	if err != nil {
		return nil, err
	}
	lb.Excluded = exclusions(excluded, q.Condition)
	calcs, common := buildEntries(graded, q)
	// Grading provenance reads the same runs the entries score: on the
	// common scope only the common tasks (audit qc2-05-11).
	lb.Grading, lb.Judges = boardGrading(ctx, src, scopedPool(graded, q, common), q.Condition)
	if err := rank(ctx, calcs, q.Alpha); err != nil {
		return nil, err
	}
	lb.Entries, lb.CommonTasks = toEntries(calcs), common
	if q.Synthetic == SyntheticOnly {
		for i := range lb.Entries {
			lb.Entries[i].Synthetic = true
		}
	}
	if q.Customer == CustomerSimulated {
		lb.Customer, lb.Simulator = CustomerSimulated, q.Simulator
	}
	return lb, nil
}

// expInfo is what the board needs of one experiment.
type expInfo struct {
	id      string
	m       *schemas.Manifest
	suite   string         // id@version
	invalid bool           // the newest analysis decided invalid
	trials  map[string]int // trialKey(arm, condition) -> trials per task
	regrade string         // regrade id read under grade source latest; "" for none
}

// record is one slot of an experiment with its counted run, if sealed.
type record struct {
	exp     *expInfo
	slot    schemas.Slot
	run     schemas.Run // the counted run; the latest attempt when none is sealed
	sealed  bool
	arm     *schemas.ArmManifest     // nil when the manifest does not list the slot's arm
	hash    string                   // resolved agent config hash
	metrics map[string]schemas.Value // set when read from a regrade; nil: read metrics.json
	source  string                   // regrade id the run was read from; "" for its original grade
	counted int                      // sealed runs of the slot that are not aborted_infra; more than one is excluded
	review  *schemas.Review          // the human review the outcome came from under grade source reviewed (review.go)

	sealVerified    bool   // the seal verified (sealReason); checked once, before any file is read
	sealMismatch    bool   // the seal did not verify; no file of the run is read
	evidenceProblem string // a sealed file the board reads did not parse; the run is excluded
}

// key is the (experiment, config) pair synthetic classification works on.
func (r *record) key() string { return r.exp.id + "\x00" + r.hash }

// collect reads every experiment and picks each slot's counted run the way
// the store does: the first sealed run whose outcome is not aborted_infra,
// else the last attempt when it is a sealed aborted_infra. A slot with only
// unsealed runs keeps its latest attempt, unsealed. Slots with no run are
// planned work, not runs, and are skipped. Each experiment's trials per task
// come from its manifest's slots, or from its runs' slots when the manifest
// lists none for an arm and condition.
func collect(ctx context.Context, src Source) ([]*record, error) {
	var out []*record
	ids := slices.Clone(src.Experiments())
	slices.Sort(ids)
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("leaderboard: %w", err)
		}
		m, ok := src.Manifest(id)
		if !ok || m == nil || m.Experiment.Exploratory() { // run control: a rerun of failures is exploratory and never ranks
			continue
		}
		exp := &expInfo{id: id, m: m, suite: m.SuiteID + "@" + m.SuiteVersion}
		if d, ok := src.LatestDecision(id); ok && d == schemas.DecisionInvalid {
			exp.invalid = true
		}
		runs := src.Runs(id)
		exp.trials = slotTrials(m.Slots)
		bySlot := map[string][]Run{}
		ran := make([]schemas.Slot, 0, len(runs))
		for _, r := range runs {
			bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
			ran = append(ran, r.Slot)
		}
		for key, n := range slotTrials(ran) {
			if exp.trials[key] == 0 {
				exp.trials[key] = n
			}
		}
		slotIDs := make([]string, 0, len(bySlot))
		for s := range bySlot {
			slotIDs = append(slotIDs, s)
		}
		slices.Sort(slotIDs)
		for _, s := range slotIDs {
			if rec := newRecord(exp, bySlot[s]); rec != nil {
				out = append(out, rec)
			}
		}
	}
	return out, nil
}

// newRecord builds a slot's record from its runs, or nil when the slot's arm
// resolves to no config hash, is a legacy score-mode repeat
// (schemas.LegacyRepeatArm) or a stream's no-memory control
// (schemas.StreamControlArm).
func newRecord(exp *expInfo, runs []Run) *record {
	slices.SortStableFunc(runs, func(x, y Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
	pick, sealed := runs[len(runs)-1], false
	countedRun := func(r Run) bool { return r.Sealed && r.Run.Outcome != schemas.OutcomeAbortedInfra && !r.Superseded }
	if i := slices.IndexFunc(runs, countedRun); i >= 0 {
		pick, sealed = runs[i], true
	} else if pick.Sealed {
		sealed = true
	}
	counted := 0
	for _, r := range runs {
		if countedRun(r) {
			counted++
		}
	}
	if schemas.LegacyRepeatArm(exp.m, pick.Slot.Arm) {
		return nil // the hidden score-mode copy of another arm, which is already on the board
	}
	if schemas.StreamControlArm(exp.m, pick.Slot.Arm) {
		return nil // a stream's no-memory control: forward transfer only, never ranked
	}
	rec := &record{exp: exp, slot: pick.Slot, run: pick.Run, sealed: sealed, counted: counted}
	if pick.Slot.Arm == "compat" && exp.m.Compatibility != nil {
		rec.arm = exp.m.Compatibility
	} else if a, ok := exp.m.Arms[pick.Slot.Arm]; ok {
		rec.arm = &a
	}
	switch {
	case rec.arm != nil && rec.arm.ConfigHash != "":
		rec.hash = rec.arm.ConfigHash
	case pick.Run.Agent.ConfigHash != "":
		rec.hash = pick.Run.Agent.ConfigHash
	default:
		return nil
	}
	return rec
}

// slotTrials maps trialKey(arm, condition) to the trials per task the slots
// plan: the highest 1-based trial number.
func slotTrials(slots []schemas.Slot) map[string]int {
	out := map[string]int{}
	for _, s := range slots {
		key := trialKey(s.Arm, s.Condition)
		out[key] = max(out[key], s.Trial)
	}
	return out
}

// trialKey keys an experiment's trials per task by arm and condition.
func trialKey(arm, condition string) string { return arm + "\x00" + condition }

// suiteList returns every suite with a sealed run, newest first by the
// newest manifest that contributed one.
func suiteList(recs []*record) []string {
	newest := map[string]time.Time{}
	for _, r := range recs {
		if !r.sealed {
			continue
		}
		if t, ok := newest[r.exp.suite]; !ok || r.exp.m.CreatedAt.After(t) {
			newest[r.exp.suite] = r.exp.m.CreatedAt
		}
	}
	out := make([]string, 0, len(newest))
	for s := range newest {
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y string) int {
		return cmp.Or(newest[y].Compare(newest[x]), cmp.Compare(x, y))
	})
	return out
}

// conditionList returns the conditions with a sealed run, clean first, then
// by name.
func conditionList(recs []*record) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range recs {
		if r.sealed && !seen[r.slot.Condition] {
			seen[r.slot.Condition] = true
			out = append(out, r.slot.Condition)
		}
	}
	slices.SortFunc(out, func(x, y string) int {
		return cmp.Or(compareTrueFirst(x == schemas.ConditionClean, y == schemas.ConditionClean), cmp.Compare(x, y))
	})
	return out
}

// validSuite reports whether s is id@version with both parts plain tokens.
func validSuite(s string) bool {
	id, version, ok := strings.Cut(s, "@")
	return ok && validToken(id) && validToken(version)
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

// compareTrueFirst orders true before false.
func compareTrueFirst(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return -1
	default:
		return 1
	}
}

// scopedPool is p restricted to the runs the entries score on q.Condition:
// the common tasks on the common scope, everything otherwise.
func scopedPool(p pool, q Query, common []string) pool {
	if q.Scope != ScopeCommon {
		return p
	}
	out := pool{}
	for h, byCond := range p {
		runs := slices.DeleteFunc(slices.Clone(byCond[q.Condition]), func(r gradedRun) bool { return !slices.Contains(common, r.rec.slot.Task) })
		out[h] = map[string][]gradedRun{q.Condition: runs}
	}
	return out
}
