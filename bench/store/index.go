package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/schemas"
)

// GradeSourceOriginal names the grade the run's own grader wrote, as opposed
// to a regrade id.
const GradeSourceOriginal = "original"

// runFileAllowlist is every run file the API may serve. Names are plain file
// names, so a lookup can never leave the run folder.
var runFileAllowlist = map[string]bool{
	"human_actions.jsonl":    true, // Z03: a person's inputs in a debug run (schemas/recordings.go)
	"run.json":               true,
	"agent.stream.jsonl":     true,
	"agent.stderr.txt":       true,
	"workspace.diff":         true,
	"gateway.requests.jsonl": true,
	"gateway.raw.jsonl":      true,
	"tools.jsonl":            true,
	"proxy.ledger.jsonl":     true,
	"events.jsonl":           true,
	"receipts.json":          true,
	"cost.json":              true,
	"gates.json":             true,
	"metrics.json":           true,
	"grade.json":             true,
	"plan.json":              true,
	"evidence.json":          true,
	"canonical.json":         true,
	"seal.json":              true,
	"transitions.jsonl":      true,
	"adapter.fault.json":     true,
	"answer.txt":             true, // lane Y15: annotation queues read the answer a run sealed (never served over HTTP: handlers keep their own allowlist)
	"adapter.fault.jsonl":    true,
	"realism.json":           true, // Eval integrity area (S2): a neutral run's realism record (environments/realism.go)
}

// SealedRuns loads every counted, sealed run of an experiment for analysis,
// in slot order. gradeSource is "" or "original" for the runs' own grades, or
// a regrade id: then a run's regrade grade is used when the regrade has one
// for it, and the original otherwise; when the regrade recomputed gates
// (regate.json), its gates, outcome, Y and changed metrics replace the sealed
// ones. SealedRun.GradeSource says which source the run used. A regrade file
// whose parent_seal is not the run's seal hash, or a regate whose content hash
// does not match, fails with schemas.ErrSealMismatch. metrics.json,
// gates.json, receipts.json and grade.json are optional.
//
// Per slot, the counted run is the lowest-attempt sealed run that is not
// aborted_infra. A slot whose runs were all aborted_infra enters with its last
// attempt once that one is sealed. A counted slot with no sealed run yet is
// left out here; the verdict adds it back from the manifest as an unknown, so
// it is never silently dropped.
//
// SealedRuns is the one place sealed runs are read for analysis, so it is
// where seals are verified: every returned run was compared with its seal.json
// and with the seal hash seals.log recorded (Verify's checks, through the
// verification cache). A run that does not verify is still returned, with no
// file of it parsed, its metrics empty and the reason in Problems; a slot with
// more than one counted run returns the lowest attempt with the count in
// Problems. Callers decide what a problem means: the verdict makes the
// experiment INVALID, the leaderboard leaves the run out, regate and release
// refuse. Regrade and analysis folders are child artifacts outside the seals
// and are checked by their parent_seal and content hash instead.
func (s *FSStore) SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("sealed runs: %w", err)
	}
	if gradeSource == GradeSourceReviewed {
		return s.reviewedRuns(ctx, expID) // human review: sealed grades with reviews applied (reviewed.go)
	}
	regradeID, err := s.resolveGradeSource(expID, gradeSource)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	slots, err := s.listSlots(expID)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	m, err := s.LoadManifest(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	slotProblems := canonicalSlots(m, slots)
	var cover map[string]bool
	if regradeID != "" {
		if cover, err = s.committedRegate(expID, regradeID); err != nil {
			return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
		}
	}
	logged, err := s.loggedSeals(expID)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	var out []schemas.SealedRun
	for i := range slots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slot := &slots[i]
		if !slot.Counted {
			continue
		}
		recs, err := s.loadSlotRuns(slot, false)
		if err != nil {
			return nil, fmt.Errorf("sealed runs %s: slot %s: %w", expID, slot.SlotID, err)
		}
		// Every sealed attempt's run.json drives the selection below, so
		// each one is verified first: a damaged superseded attempt is still a
		// link of the seal chain and must surface (audit 1-A-04).
		supersededProblems, err := s.verifySupersededAttempts(recs, logged)
		if err != nil {
			return nil, fmt.Errorf("sealed runs %s: slot %s: %w", expID, slot.SlotID, err)
		}
		rec, counted := countedRun(recs)
		if rec == nil {
			if len(supersededProblems) > 0 {
				// No attempt counts yet (a retry is still running or was
				// interrupted), but a sealed earlier attempt no longer
				// verifies. The slot enters as an unknown carrying that
				// problem instead of vanishing into ordinary missing
				// coverage (audit qc2-01-06).
				out = append(out, unfinishedSlotRun(expID, slot, supersededProblems))
			}
			continue
		}
		sr, err := s.loadSealedRun(expID, slot, rec, regradeID, logged)
		if err != nil {
			return nil, fmt.Errorf("sealed runs %s: run %s: %w", expID, rec.Run.RunID, err)
		}
		if cover[rec.Run.RunID] && sr.GradeSource != regradeID && len(sr.Problems) == 0 {
			// The regate covered this run but its result is missing: the
			// regrade folder lost a file after publishing.
			sr.Problems = append(sr.Problems, fmt.Sprintf("regrade %s covers run %s but holds no result for it", regradeID, rec.Run.RunID))
		}
		for j := range recs {
			if id := recs[j].Run.RunID; id != rec.Run.RunID && supersededProblems[id] != "" {
				sr.Problems = append(sr.Problems, supersededProblems[id])
			}
		}
		sr.Problems = append(sr.Problems, slotProblems[slot.SlotID]...)
		sr.Problems = append(sr.Problems, runIdentityProblems(expID, slot, &rec.Run)...)
		if len(counted) > 1 {
			s.logger.Warn("slot has more than one counted run", "slot_id", slot.SlotID, "runs", strings.Join(counted, ","))
			sr.Problems = append(sr.Problems, fmt.Sprintf("slot %s has %d counted runs (%s); exactly one may count",
				slot.SlotID, len(counted), strings.Join(counted, ", ")))
		}
		out = append(out, *sr)
	}
	return out, nil
}

// HarnessAttempts loads every sealed attempt SealedRuns does not count: the
// runs of uncounted (compatibility) slots and the superseded attempts of
// counted slots (an aborted_infra retry, or any attempt beside the counted
// one). Each is verified against its seal like a counted run and carries its
// own sealed gates; every returned run has Slot.Counted false, so the verdict
// reads it only for harness-level checks (egress) and never in paired
// arithmetic (audit qc2-02-05). Damaged attempts are already reported on
// their slot by SealedRuns, and come back here with no gates.
func (s *FSStore) HarnessAttempts(ctx context.Context, expID string) ([]schemas.SealedRun, error) {
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("harness attempts: %w", err)
	}
	slots, err := s.listSlots(expID)
	if err != nil {
		return nil, fmt.Errorf("harness attempts %s: %w", expID, err)
	}
	logged, err := s.loggedSeals(expID)
	if err != nil {
		return nil, fmt.Errorf("harness attempts %s: %w", expID, err)
	}
	var out []schemas.SealedRun
	for i := range slots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slot := &slots[i]
		recs, err := s.loadSlotRuns(slot, false)
		if err != nil {
			return nil, fmt.Errorf("harness attempts %s: slot %s: %w", expID, slot.SlotID, err)
		}
		picked := ""
		if slot.Counted {
			if rec, _ := countedRun(recs); rec != nil {
				picked = rec.Run.RunID
			}
		}
		for j := range recs {
			rec := &recs[j]
			if !rec.Sealed || rec.Run.RunID == picked {
				continue
			}
			sr, err := s.loadSealedRun(expID, slot, rec, "", logged)
			if err != nil {
				return nil, fmt.Errorf("harness attempts %s: run %s: %w", expID, rec.Run.RunID, err)
			}
			sr.Slot.Counted = false
			out = append(out, *sr)
		}
	}
	return out, nil
}

// RunEntry is one run as the API lists it, with its slot and seal state. Dir
// is a host path and is never serialized.
type RunEntry struct {
	Slot     schemas.Slot `json:"slot"`
	Run      schemas.Run  `json:"run"`
	Dir      string       `json:"-"`
	Sealed   bool         `json:"sealed"`
	SealHash string       `json:"seal_hash,omitempty"`
	// Superseded marks a run an operator requeued: it never counts.
	Superseded bool `json:"superseded,omitempty"`
}

// Index is the in-memory read index the API serves from. Rebuild reads the
// run folder and swaps in a whole new snapshot through an atomic pointer, as
// Bifrost's hot config reload does; a snapshot is never mutated, so readers
// need no lock and always see one consistent state.
type Index struct {
	store   *FSStore
	rebuild sync.Mutex // one Rebuild at a time, so an older scan never replaces a newer one
	snap    atomic.Pointer[indexSnapshot]
}

type indexSnapshot struct {
	experiments []string
	manifests   map[string]*schemas.Manifest
	runsByExp   map[string][]RunEntry
	byRun       map[string]RunEntry
	byPair      map[string][]RunEntry
}

// NewIndex returns an empty index over store. Call Rebuild to fill it.
func NewIndex(store *FSStore) *Index {
	ix := &Index{store: store}
	ix.snap.Store(&indexSnapshot{
		manifests: map[string]*schemas.Manifest{},
		runsByExp: map[string][]RunEntry{},
		byRun:     map[string]RunEntry{},
		byPair:    map[string][]RunEntry{},
	})
	return ix
}

// Rebuild rescans the store and swaps in the new snapshot. An experiment that
// cannot be read is skipped with a warning so one bad folder does not hide
// the rest; a failure to list the root keeps the previous snapshot.
func (ix *Index) Rebuild(ctx context.Context) error {
	ix.rebuild.Lock()
	defer ix.rebuild.Unlock()
	ids, err := ix.store.ListExperiments(ctx)
	if err != nil {
		return fmt.Errorf("rebuild index: %w", err)
	}
	next := &indexSnapshot{
		manifests: make(map[string]*schemas.Manifest, len(ids)),
		runsByExp: make(map[string][]RunEntry, len(ids)),
		byRun:     map[string]RunEntry{},
		byPair:    map[string][]RunEntry{},
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, runs, err := ix.store.indexExperiment(ctx, id)
		if err != nil {
			ix.store.logger.Warn("index: experiment skipped", "exp_id", id, "error", err.Error())
			continue
		}
		next.experiments = append(next.experiments, id)
		next.manifests[id] = m
		next.runsByExp[id] = runs
		for _, r := range runs {
			next.byRun[r.Run.RunID] = r
			if r.Sealed && r.Slot.PairID != "" {
				next.byPair[r.Slot.PairID] = append(next.byPair[r.Slot.PairID], r)
			}
		}
	}
	for _, runs := range next.byPair {
		sort.SliceStable(runs, func(i, j int) bool {
			if runs[i].Slot.Arm != runs[j].Slot.Arm {
				return runs[i].Slot.Arm < runs[j].Slot.Arm
			}
			return runs[i].Run.Attempt < runs[j].Run.Attempt
		})
	}
	ix.snap.Store(next)
	return nil
}

// Experiments returns the indexed experiment ids, sorted.
func (ix *Index) Experiments() []string {
	return append([]string(nil), ix.snap.Load().experiments...)
}

// Manifest returns a copy of an experiment's manifest. Its maps and slices
// are shared with the index and must not be modified.
func (ix *Index) Manifest(expID string) (*schemas.Manifest, bool) {
	m, ok := ix.snap.Load().manifests[expID]
	if !ok {
		return nil, false
	}
	c := *m
	return &c, true
}

// Runs returns every run of an experiment, counted or not, sealed or not, in
// slot order then attempt order.
func (ix *Index) Runs(expID string) ([]RunEntry, bool) {
	runs, ok := ix.snap.Load().runsByExp[expID]
	if !ok {
		return nil, false
	}
	return append([]RunEntry(nil), runs...), true
}

// Run returns one run by id.
func (ix *Index) Run(runID string) (RunEntry, bool) {
	r, ok := ix.snap.Load().byRun[runID]
	return r, ok
}

// Pair returns the sealed runs of a pair (the slots of every arm for one task,
// condition and trial), ordered by arm then attempt.
func (ix *Index) Pair(pairID string) ([]RunEntry, bool) {
	runs, ok := ix.snap.Load().byPair[pairID]
	if !ok {
		return nil, false
	}
	return append([]RunEntry(nil), runs...), true
}

// RunFile returns the raw bytes of one file of a run folder. Only the names
// in the fixed allowlist are served, and only regular files; anything else is
// schemas.ErrNotFound, so the API cannot be walked to other paths.
func (ix *Index) RunFile(runID, name string) ([]byte, error) {
	if !runFileAllowed(name) {
		return nil, fmt.Errorf("run file %q: not served: %w", name, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	p := filepath.Join(r.Dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s file %s: %w", runID, name, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s file %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, err)
	}
	return b, nil
}

func runFileAllowed(name string) bool {
	if runFileAllowlist[name] {
		return true
	}
	if strings.HasPrefix(name, "agent.stream.") && strings.HasSuffix(name, ".jsonl") {
		n := strings.TrimSuffix(strings.TrimPrefix(name, "agent.stream."), ".jsonl")
		return positiveSessionIndex(n)
	}
	if strings.HasPrefix(name, "agent.stderr.") && strings.HasSuffix(name, ".txt") {
		n := strings.TrimSuffix(strings.TrimPrefix(name, "agent.stderr."), ".txt")
		return positiveSessionIndex(n)
	}
	if id, ok := strings.CutPrefix(name, "services/"); ok && strings.HasSuffix(id, ".log") { // X2: a compose service's log (environments/compose.go)
		return serviceLogID(strings.TrimSuffix(id, ".log"))
	}
	return false
}

// serviceLogID reports whether id is one host-name label (lower-case
// letters, digits and inner '-', at most 63), the service ids of
// services/<id>.log (X2). It is pure.
func serviceLogID(id string) bool {
	if id == "" || len(id) > 63 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	return strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

func positiveSessionIndex(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') || strings.Trim(s, "0123456789") != "" {
		return false
	}
	i, err := strconv.Atoi(s)
	return err == nil && i > 0
}

// VerifyRun checks one indexed run against its seal.json and the seal hash
// its experiment's seals.log recorded, as SealedRuns does, through the
// store's verification cache: the first call on a run hashes its files, later
// calls on an unchanged folder only stat them. An unsealed run has nothing to
// verify and returns nil; an unknown run is schemas.ErrNotFound; a difference
// wraps schemas.ErrSealMismatch and names the first file that differs.
func (ix *Index) VerifyRun(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	if !r.Sealed {
		return nil
	}
	expID := r.Slot.ExpID
	if expID == "" {
		expID = r.Run.ExpID
	}
	logged, err := ix.store.loggedSeals(expID)
	if err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	if _, err := ix.store.verifySealedRun(r.Dir, runID, logged); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	return nil
}

// Store returns the store the index reads, so a reader that needs the store
// itself (a regrade's sealed runs) shares its verification cache.
func (ix *Index) Store() *FSStore {
	return ix.store
}

// indexExperiment loads one experiment's manifest and run entries.
func (s *FSStore) indexExperiment(ctx context.Context, expID string) (*schemas.Manifest, []RunEntry, error) {
	m, err := s.LoadManifest(ctx, expID)
	if err != nil {
		return nil, nil, err
	}
	slots, err := s.listSlots(expID)
	if err != nil {
		return nil, nil, err
	}
	var runs []RunEntry
	for i := range slots {
		recs, err := s.loadSlotRuns(&slots[i], false)
		if err != nil {
			return nil, nil, fmt.Errorf("slot %s: %w", slots[i].SlotID, err)
		}
		for _, rec := range recs {
			e := RunEntry{Slot: slots[i], Run: rec.Run, Dir: rec.Dir, Sealed: rec.Sealed, Superseded: rec.Superseded}
			if rec.Sealed {
				seal, err := readSeal(rec.Dir)
				if err != nil {
					s.logger.Warn("index: seal unreadable", "run_id", rec.Run.RunID, "error", err.Error())
				} else {
					e.SealHash = seal.Hash
				}
			}
			runs = append(runs, e)
		}
	}
	return m, runs, nil
}

// canonicalSlots resolves each slot read from slot.json (slots, changed in
// place) to its slot in the frozen manifest m, which is what the verdict
// cells use: task, condition, arm, trial and counted come from the manifest,
// never from the per-slot file beside the runs (audit 1-A-01). A slot.json
// that disagrees with the manifest, or a slot the manifest does not list,
// is returned as a problem keyed by slot id, so the run in it makes the
// verdict INVALID. A manifest without slots (an old experiment) keeps the
// slot.json values.
func canonicalSlots(m *schemas.Manifest, slots []schemas.Slot) map[string][]string {
	if m == nil || len(m.Slots) == 0 {
		return nil
	}
	frozen := make(map[string]schemas.Slot, len(m.Slots))
	for _, sl := range m.Slots {
		if sl.ExpID == "" {
			sl.ExpID = m.ExpID
		}
		frozen[sl.SlotID] = sl
	}
	problems := map[string][]string{}
	for i := range slots {
		disk := slots[i]
		want, ok := frozen[disk.SlotID]
		if !ok {
			problems[disk.SlotID] = append(problems[disk.SlotID], fmt.Sprintf("slot %s is not in the manifest", disk.SlotID))
			continue
		}
		if disk != want {
			problems[disk.SlotID] = append(problems[disk.SlotID], fmt.Sprintf("slot.json of %s disagrees with the manifest (%s)", disk.SlotID, slotDiff(disk, want)))
		}
		slots[i] = want
	}
	return problems
}

// slotDiff names the fields where slot.json (got) differs from the manifest.
func slotDiff(got, want schemas.Slot) string {
	var d []string
	add := func(name string, g, w any) {
		if g != w {
			d = append(d, fmt.Sprintf("%s %v, manifest %v", name, g, w))
		}
	}
	add("pair_id", got.PairID, want.PairID)
	add("exp_id", got.ExpID, want.ExpID)
	add("suite", got.Suite, want.Suite)
	add("task", got.Task, want.Task)
	add("task_version", got.TaskVersion, want.TaskVersion)
	add("task_hash", got.TaskHash, want.TaskHash)
	add("run_input_hash", got.RunInputHash, want.RunInputHash)
	add("arm", got.Arm, want.Arm)
	add("condition", got.Condition, want.Condition)
	add("trial", got.Trial, want.Trial)
	add("order_index", got.OrderIndex, want.OrderIndex)
	add("counted", got.Counted, want.Counted)
	add("optional", got.Optional, want.Optional)
	return strings.Join(d, "; ")
}

// runIdentityProblems checks that the sealed run.json names the slot, pair
// and experiment it was found under, so a run copied into another slot
// folder cannot take that slot's place in the verdict.
func runIdentityProblems(expID string, slot *schemas.Slot, r *schemas.Run) []string {
	var p []string
	if r.SlotID != slot.SlotID {
		p = append(p, fmt.Sprintf("run %s names slot %s, found under slot %s", r.RunID, r.SlotID, slot.SlotID))
	}
	if r.PairID != "" && r.PairID != slot.PairID {
		p = append(p, fmt.Sprintf("run %s names pair %s, slot %s is in pair %s", r.RunID, r.PairID, slot.SlotID, slot.PairID))
	}
	if r.ExpID != "" && r.ExpID != expID {
		p = append(p, fmt.Sprintf("run %s names experiment %s, found under %s", r.RunID, r.ExpID, expID))
	}
	return p
}

// listSlots reads every slot.json of an experiment, sorted by order index.
func (s *FSStore) listSlots(expID string) ([]schemas.Slot, error) {
	if _, err := os.Stat(filepath.Join(s.expDir(expID), manifestFile)); err != nil {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	entries, err := os.ReadDir(filepath.Join(s.expDir(expID), slotsDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list slots: %w", err)
	}
	var slots []schemas.Slot
	for _, e := range entries {
		if !e.IsDir() || validateID("slot_id", e.Name()) != nil {
			continue
		}
		var sl schemas.Slot
		if err := readJSON(filepath.Join(s.expDir(expID), slotsDir, e.Name(), slotFile), &sl); err != nil {
			return nil, fmt.Errorf("slot %s: %w", e.Name(), err)
		}
		if sl.SlotID != e.Name() {
			return nil, fmt.Errorf("slot folder %s holds slot.json of %s", e.Name(), sl.SlotID)
		}
		if sl.ExpID == "" {
			sl.ExpID = expID
		}
		slots = append(slots, sl)
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].OrderIndex != slots[j].OrderIndex {
			return slots[i].OrderIndex < slots[j].OrderIndex
		}
		return slots[i].SlotID < slots[j].SlotID
	})
	return slots, nil
}

// resolveGradeSource returns the regrade id to read, or "" for original.
func (s *FSStore) resolveGradeSource(expID, gradeSource string) (string, error) {
	if gradeSource == "" || gradeSource == GradeSourceOriginal {
		return "", nil
	}
	if err := validateID("regrade_id", gradeSource); err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(s.expDir(expID), regradesDir, gradeSource)); err != nil {
		return "", fmt.Errorf("regrade %s: %w", gradeSource, schemas.ErrNotFound)
	}
	if _, err := s.committedRegate(expID, gradeSource); err != nil {
		return "", err
	}
	return gradeSource, nil
}

// committedRegate reads the regrade.json of regradeID and, for a regate
// (gate versions set), returns its coverage record as a set. A regate
// without one was not published by WriteGateRegrades and is refused as a
// grade source (audit 8-D-03). A grade regrade returns nil.
func (s *FSStore) committedRegate(expID, regradeID string) (map[string]bool, error) {
	var info RegradeInfo
	found, err := readOptionalJSON(filepath.Join(s.expDir(expID), regradesDir, regradeID, regradeFile), &info)
	if err != nil {
		return nil, fmt.Errorf("regrade %s: %w", regradeID, err)
	}
	if !found || len(info.GateVersions) == 0 {
		return nil, nil
	}
	if len(info.Runs) == 0 {
		return nil, fmt.Errorf("regrade %s has no coverage record: it was interrupted or not written in one batch, so it is not a grade source; regate again under a new id, or, for a regate written before coverage records existed, commit it with regate -commit-legacy: %w", regradeID, schemas.ErrNotFound)
	}
	cover := make(map[string]bool, len(info.Runs))
	for _, id := range info.Runs {
		cover[id] = true
	}
	return cover, nil
}

// countedRun picks the slot's counted run from recs (sorted by attempt), or
// nil when none is sealed yet, and returns the ids of every run the rule
// counts. aborted_infra retries are never counted, so a slot with retries has
// one counted run; more than one means the engine's one-counted-run rule was
// broken and the caller reports it. The pick is always the lowest attempt, so
// it is deterministic.
func countedRun(recs []schemas.RunRecord) (*schemas.RunRecord, []string) {
	var picked *schemas.RunRecord
	var counted []string
	for i := range recs {
		r := &recs[i]
		if !r.Sealed || r.Run.Outcome == schemas.OutcomeAbortedInfra || r.Superseded { // run control: a requeued run never counts
			continue
		}
		counted = append(counted, r.Run.RunID)
		if picked == nil {
			picked = r
		}
	}
	if picked != nil || len(recs) == 0 {
		return picked, counted
	}
	last := &recs[len(recs)-1]
	if last.Sealed && last.Run.Outcome == schemas.OutcomeAbortedInfra && !last.Superseded {
		return last, []string{last.Run.RunID}
	}
	return nil, nil
}

// unfinishedSlotRun is the stand-in for a counted slot with no counted run
// whose sealed attempts have integrity problems: the same unknown the verdict
// adds for a missing slot (verdict.CompleteSlots), with the problems attached
// in run id order.
func unfinishedSlotRun(expID string, slot *schemas.Slot, problems map[string]string) schemas.SealedRun {
	sl := *slot
	if sl.ExpID == "" {
		sl.ExpID = expID
	}
	ids := make([]string, 0, len(problems))
	for id := range problems {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sr := schemas.SealedRun{
		Slot:    sl,
		Run:     schemas.Run{SlotID: sl.SlotID, PairID: sl.PairID, ExpID: sl.ExpID, Outcome: schemas.OutcomeUnknown, Y: schemas.Unknown("no counted run", schemas.EvidenceOutcome)},
		Metrics: map[string]schemas.Value{},
	}
	for _, id := range ids {
		sr.Problems = append(sr.Problems, problems[id])
	}
	return sr
}

// verifySupersededAttempts verifies every sealed attempt of one slot and
// returns run id -> problem for each that does not verify. The picked
// attempt is verified again by loadSealedRun (a cache hit), which reports its
// own mismatch, so the caller drops its entry and never counts it twice.
func (s *FSStore) verifySupersededAttempts(recs []schemas.RunRecord, logged map[string]string) (map[string]string, error) {
	var out map[string]string
	for i := range recs {
		r := &recs[i]
		if !r.Sealed {
			continue
		}
		_, err := s.verifySealedRun(r.Dir, r.Run.RunID, logged)
		if err == nil {
			continue
		}
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return nil, fmt.Errorf("verify attempt %s: %w", r.Run.RunID, err)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[r.Run.RunID] = fmt.Sprintf("sealed attempt %s (attempt %d) does not verify: %s", r.Run.RunID, r.Run.Attempt, err)
	}
	return out, nil
}

// loadSealedRun verifies one sealed run against its seal and seals.log
// (logged), then reads the chosen grade and the optional evidence summaries.
// A run that does not verify comes back with its slot and run.json only, its
// metrics empty and the mismatch in Problems: nothing read from a changed
// folder can be trusted, and a changed file may not even parse. The gateway
// tally (usage completeness and the drop counter) is derived from
// evidence.json here rather than stored, so runs sealed before the tally
// existed are analysed the same way.
func (s *FSStore) loadSealedRun(expID string, slot *schemas.Slot, rec *schemas.RunRecord, regradeID string, logged map[string]string) (*schemas.SealedRun, error) {
	seal, err := s.verifySealedRun(rec.Dir, rec.Run.RunID, logged)
	if err != nil {
		if !errors.Is(err, schemas.ErrSealMismatch) {
			return nil, err
		}
		sr := &schemas.SealedRun{Slot: *slot, Run: rec.Run, Dir: rec.Dir, GradeSource: GradeSourceOriginal,
			Metrics: map[string]schemas.Value{}, Problems: []string{"seal does not verify: " + err.Error()}}
		if seal != nil {
			sr.SealHash = seal.Hash
		}
		return sr, nil
	}
	if err := verifyCanonicalFile(rec.Dir, &rec.Run); err != nil {
		return nil, fmt.Errorf("verify canonical run %s: %w", rec.Run.RunID, err)
	}
	sr := &schemas.SealedRun{Slot: *slot, Run: rec.Run, Dir: rec.Dir, SealHash: seal.Hash, Metrics: map[string]schemas.Value{}}
	if regradeID != "" {
		var g schemas.Grade
		p := filepath.Join(s.expDir(expID), regradesDir, regradeID, runsDir, rec.Run.RunID, gradeFile)
		found, err := readOptionalJSON(p, &g)
		if err != nil {
			return nil, fmt.Errorf("regrade %s: %w", regradeID, err)
		}
		if found {
			if g.ParentSeal != seal.Hash {
				return nil, fmt.Errorf("regrade %s parent_seal %s, run seal %s: %w", regradeID, g.ParentSeal, seal.Hash, schemas.ErrSealMismatch)
			}
			sr.Grade, sr.GradeSource = &g, regradeID
		}
	}
	if sr.Grade == nil {
		sr.GradeSource = GradeSourceOriginal
		var g schemas.Grade
		found, err := readOptionalJSON(filepath.Join(rec.Dir, gradeFile), &g)
		if err != nil {
			return nil, err
		}
		if found {
			sr.Grade = &g
		}
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, metricsFile), &sr.Metrics); err != nil {
		return nil, err
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, gatesFile), &sr.Gates); err != nil {
		return nil, err
	}
	if regradeID != "" {
		if err := s.overlayRegate(sr, expID, regradeID, seal.Hash); err != nil {
			return nil, fmt.Errorf("regrade %s: %w", regradeID, err)
		}
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, receiptsFile), &sr.Receipts); err != nil {
		return nil, err
	}
	var ev struct {
		Requests     *schemas.RequestLog `json:"requests"`
		LedgerBreaks []string            `json:"ledger_breaks"`
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, evidenceFile), &ev); err != nil {
		return nil, err
	}
	sr.Gateway = ev.Requests.Tally()
	if len(ev.LedgerBreaks) > 0 {
		// A fault proxy ledger whose hash chain broke at collect time: the
		// gateway matching, cost and receipts derived from it cannot be
		// verified, so no verdict rests on the run (audit 3-B-07).
		sr.Problems = append(sr.Problems, fmt.Sprintf("proxy ledger hash chain broken: %s", strings.Join(ev.LedgerBreaks, "; ")))
	}
	// Tenancy area: a runner's own gateway preflight, recorded by the
	// coordinator in the sealed remote/worker.json (hosted manifests defer
	// the preflight to runners).
	var remote struct {
		Gateway *schemas.GatewayManifest `json:"gateway"`
		Images  *schemas.RunnerImages    `json:"images"` // Deploy area: the runner's image ids (engine/remoteimages.go)
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, "remote", "worker.json"), &remote); err != nil {
		return nil, err
	}
	sr.RunnerGateway = remote.Gateway
	sr.RunnerImages = remote.Images // Deploy area
	return sr, nil
}

// overlayRegate replaces the sealed gates, outcome, Y and gate-dependent
// metrics of sr with the regrade's regate.json when it has one for the run.
// Only the in-memory copy changes; the sealed files are never written.
func (s *FSStore) overlayRegate(sr *schemas.SealedRun, expID, regradeID, sealHash string) error {
	var r GateRegrade
	p := filepath.Join(s.expDir(expID), regradesDir, regradeID, runsDir, sr.Run.RunID, regateFile)
	found, err := readOptionalJSON(p, &r)
	if err != nil || !found {
		return err
	}
	if err := verifyGateRegrade(&r, sealHash); err != nil {
		return fmt.Errorf("regate of run %s: %w", sr.Run.RunID, err)
	}
	sr.Gates = r.Gates
	sr.Run.Outcome, sr.Run.Y = r.Outcome, r.Y
	if sr.Metrics == nil {
		sr.Metrics = map[string]schemas.Value{}
	}
	for name, v := range r.Metrics {
		sr.Metrics[name] = v
	}
	sr.GradeSource = regradeID
	return nil
}
