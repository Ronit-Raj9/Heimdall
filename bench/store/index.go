package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	"run.json":               true,
	"agent.stream.jsonl":     true,
	"workspace.diff":         true,
	"gateway.requests.jsonl": true,
	"proxy.ledger.jsonl":     true,
	"events.jsonl":           true,
	"receipts.json":          true,
	"cost.json":              true,
	"gates.json":             true,
	"metrics.json":           true,
	"grade.json":             true,
	"plan.json":              true,
	"evidence.json":          true,
	"seal.json":              true,
	"transitions.jsonl":      true,
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
// attempt once that one is sealed, so a slot is never silently dropped.
func (s *FSStore) SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("sealed runs: %w", err)
	}
	regradeID, err := s.resolveGradeSource(expID, gradeSource)
	if err != nil {
		return nil, fmt.Errorf("sealed runs %s: %w", expID, err)
	}
	slots, err := s.listSlots(expID)
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
		rec := s.countedRun(slot.SlotID, recs)
		if rec == nil {
			continue
		}
		sr, err := s.loadSealedRun(expID, slot, rec, regradeID)
		if err != nil {
			return nil, fmt.Errorf("sealed runs %s: run %s: %w", expID, rec.Run.RunID, err)
		}
		out = append(out, *sr)
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
	if !runFileAllowlist[name] {
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
			e := RunEntry{Slot: slots[i], Run: rec.Run, Dir: rec.Dir, Sealed: rec.Sealed}
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
	return gradeSource, nil
}

// countedRun picks the slot's counted run from recs (sorted by attempt), or
// nil when none is sealed yet.
func (s *FSStore) countedRun(slotID string, recs []schemas.RunRecord) *schemas.RunRecord {
	var picked *schemas.RunRecord
	for i := range recs {
		r := &recs[i]
		if !r.Sealed || r.Run.Outcome == schemas.OutcomeAbortedInfra {
			continue
		}
		if picked != nil {
			s.logger.Warn("slot has more than one counted run, keeping the first", "slot_id", slotID,
				"kept", picked.Run.RunID, "ignored", r.Run.RunID)
			continue
		}
		picked = r
	}
	if picked != nil || len(recs) == 0 {
		return picked
	}
	last := &recs[len(recs)-1]
	if last.Sealed && last.Run.Outcome == schemas.OutcomeAbortedInfra {
		return last
	}
	return nil
}

// loadSealedRun reads the seal, the chosen grade and the optional evidence
// summaries of one sealed run. The gateway tally (usage completeness and the
// drop counter) is derived from evidence.json here rather than stored, so runs
// sealed before the tally existed are analysed the same way.
func (s *FSStore) loadSealedRun(expID string, slot *schemas.Slot, rec *schemas.RunRecord, regradeID string) (*schemas.SealedRun, error) {
	seal, err := readSeal(rec.Dir)
	if err != nil {
		return nil, err
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
		Requests *schemas.RequestLog `json:"requests"`
	}
	if _, err := readOptionalJSON(filepath.Join(rec.Dir, evidenceFile), &ev); err != nil {
		return nil, err
	}
	sr.Gateway = ev.Requests.Tally()
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
