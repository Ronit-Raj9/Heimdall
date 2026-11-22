package store

// Hot paths whose cost must not grow with the number of runs already sealed
// in an experiment (INFRA_I4 item 1): the seal itself, the check the server
// runs on an accepted run right after sealing it, and the index refresh
// after a seal. Full checks (Verify, VerifyChain, Index.Rebuild) stay as
// they were and still run off the hot path: at experiment end, in the
// evidence verify job and on a timer.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sealsTailChunk is how much of seals.log is read at a time from its end.
// One line is about 100 bytes, so one chunk holds the last few hundred seals.
// A variable so a test can make it small.
var sealsTailChunk int64 = 64 << 10

// sealsLogLastHash returns the hash of the last well-formed line of the open
// seals.log, reading only its tail: the same value lastHash gives for the
// whole parsed log. A tail with no well-formed line falls back to reading
// the whole log, so a long run of malformed lines changes nothing.
func (s *FSStore) sealsLogLastHash(f *os.File, expID string) (string, error) {
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("read seals.log: %w", err)
	}
	size := st.Size()
	if size <= sealsTailChunk {
		b := make([]byte, size)
		if _, err := f.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read seals.log: %w", err)
		}
		return lastHash(s.lenientSealsLog(b, expID)), nil
	}
	b := make([]byte, sealsTailChunk)
	if _, err := f.ReadAt(b, size-sealsTailChunk); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read seals.log: %w", err)
	}
	// The chunk starts inside a line: drop that partial line.
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		b = b[i+1:]
	}
	if entries, _ := parseSealsLog(b); len(entries) > 0 {
		return entries[len(entries)-1].Hash, nil
	}
	all := make([]byte, size)
	if _, err := f.ReadAt(all, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read seals.log: %w", err)
	}
	return lastHash(s.lenientSealsLog(all, expID)), nil
}

// sealLogged reports whether seals.log of the experiment has the line
// "<runID> <hash>". It reads the log backwards in chunks and stops at the
// first match, so a run sealed a moment ago costs one chunk however long the
// log is; a run that is not logged costs one full read, as before.
func (s *FSStore) sealLogged(expID, runID, hash string) (bool, error) {
	f, err := os.Open(filepath.Join(s.expDir(expID), sealsLogFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read seals.log: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("read seals.log: %w", err)
	}
	want := runID + " " + hash
	pos := st.Size()
	var carry []byte // the start of a line cut by the previous chunk's start
	for pos > 0 {
		start := max(pos-sealsTailChunk, 0)
		buf := make([]byte, pos-start, pos-start+int64(len(carry)))
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return false, fmt.Errorf("read seals.log: %w", err)
		}
		buf = append(buf, carry...)
		carry = nil
		if start > 0 {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 { // one line longer than a chunk: keep all of it for the next read
				carry = buf
				pos = start
				continue
			}
			carry, buf = append([]byte(nil), buf[:i]...), buf[i+1:]
		}
		// Newest line first: a run sealed a moment ago is at the very end.
		for end := len(buf); end > 0; {
			i := bytes.LastIndexByte(buf[:end], '\n')
			if sealLineIs(buf[i+1:end], want) {
				return true, nil
			}
			end = max(i, 0)
		}
		pos = start
	}
	if len(carry) > 0 && sealLineIs(carry, want) {
		return true, nil
	}
	return false, nil
}

// sealLineIs reports whether one seals.log line is want ("<run> <hash>"),
// ignoring surrounding and repeated blanks as parseSealsLog does.
func sealLineIs(line []byte, want string) bool {
	line = bytes.TrimSpace(line)
	if len(line) == len(want) && string(line) == want {
		return true
	}
	if len(line) < len(want) {
		return false
	}
	return strings.Join(strings.Fields(string(line)), " ") == want
}

// VerifySealed is Verify for a run whose folder is known from the run
// itself (experiment, slot and run ids) and that was sealed a moment ago:
// the server's check of an accepted run. It runs every check Verify runs on
// the folder (seal.json, every file, the artifacts its format needs, the
// canonical record), but finds the folder without listing every slot and
// looks the seal up in seals.log from the end, where it was just appended.
// Its cost does not grow with the runs already sealed in the experiment.
func (s *FSStore) VerifySealed(ctx context.Context, run *schemas.Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil {
		return errors.New("verify: nil run")
	}
	if err := validateRunIDs(run); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	dir := s.runDir(run)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("verify run %s: %w", run.RunID, schemas.ErrNotFound)
	}
	rec, err := readSeal(dir)
	if err != nil {
		return fmt.Errorf("verify run %s: %w", run.RunID, err)
	}
	if rec.RunID != run.RunID {
		return fmt.Errorf("verify run %s: seal.json names run %s: %w", run.RunID, rec.RunID, schemas.ErrSealMismatch)
	}
	if err := s.verifyRunFiles(dir, rec); err != nil {
		return fmt.Errorf("verify run %s: %w", run.RunID, err)
	}
	if missing := missingArtifacts(rec.Format, rec.Files); len(missing) > 0 {
		return fmt.Errorf("verify run %s: seal format %s lacks %s: %w", run.RunID, rec.Format, strings.Join(missing, ", "), schemas.ErrSealMismatch)
	}
	var onDisk schemas.Run
	if err := readJSON(filepath.Join(dir, runFile), &onDisk); err != nil {
		return fmt.Errorf("verify run %s: read run: %w", run.RunID, err)
	}
	if err := verifyCanonicalFile(dir, &onDisk); err != nil {
		return fmt.Errorf("verify run %s: %w", run.RunID, err)
	}
	ok, err := s.sealLogged(run.ExpID, run.RunID, rec.Hash)
	if err != nil {
		return fmt.Errorf("verify run %s: %w", run.RunID, err)
	}
	if !ok {
		return fmt.Errorf("verify run %s: seal hash %s not in seals.log: %w", run.RunID, rec.Hash, schemas.ErrSealMismatch)
	}
	return nil
}

// RefreshSlots re-reads the given slots of one experiment and swaps them
// into a copy of the current snapshot, leaving every other experiment and
// every other slot as it was: after a seal, only the slot that sealed has
// changed. Its disk work is one slot's runs, whatever the experiment's
// size. An experiment not in the snapshot yet (or one whose manifest can
// not be read) is indexed whole. Superseded marks come from the run control
// log, which is read once per call.
//
// The full Rebuild stays the reference: it runs at experiment end and on a
// timer, so anything a refresh could miss (a slot folder removed by hand)
// is corrected there.
func (ix *Index) RefreshSlots(ctx context.Context, expID string, slotIDs []string) error {
	if err := validateID("exp_id", expID); err != nil {
		return err
	}
	ix.rebuild.Lock()
	defer ix.rebuild.Unlock()
	cur := ix.snap.Load()
	old, known := cur.runsByExp[expID]
	if _, hasManifest := cur.manifests[expID]; !known || !hasManifest || len(slotIDs) == 0 {
		return ix.refreshExperimentLocked(ctx, cur, expID)
	}
	want := map[string]bool{}
	for _, id := range slotIDs {
		if validateID("slot_id", id) == nil {
			want[id] = true
		}
	}
	var fresh []RunEntry
	for id := range want {
		if err := ctx.Err(); err != nil {
			return err
		}
		var sl schemas.Slot
		if err := readJSON(filepath.Join(ix.store.expDir(expID), slotsDir, id, slotFile), &sl); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // not written yet: nothing to index
			}
			return fmt.Errorf("refresh slot %s: %w", id, err)
		}
		if sl.SlotID != id {
			return fmt.Errorf("slot folder %s holds slot.json of %s", id, sl.SlotID)
		}
		if sl.ExpID == "" {
			sl.ExpID = expID
		}
		recs, err := ix.store.loadSlotRuns(&sl, false)
		if err != nil {
			return fmt.Errorf("slot %s: %w", id, err)
		}
		for _, rec := range recs {
			e := RunEntry{Slot: sl, Run: rec.Run, Dir: rec.Dir, Sealed: rec.Sealed, Superseded: rec.Superseded}
			if rec.Sealed {
				if seal, err := readSeal(rec.Dir); err != nil {
					ix.store.logger.Warn("index: seal unreadable", "run_id", rec.Run.RunID, "error", err.Error())
				} else {
					e.SealHash = seal.Hash
				}
			}
			fresh = append(fresh, e)
		}
	}
	// The order Rebuild gives: slot order (order index, then slot id), then
	// the order loadSlotRuns returns within a slot. old is in that order;
	// fresh is sorted the same way and merged in, one pass.
	sort.SliceStable(fresh, func(i, j int) bool { return slotBefore(fresh[i].Slot, fresh[j].Slot) })
	runs := make([]RunEntry, 0, len(old)+len(fresh))
	k := 0
	for _, r := range old {
		if want[r.Slot.SlotID] {
			continue
		}
		for k < len(fresh) && slotBefore(fresh[k].Slot, r.Slot) {
			runs = append(runs, fresh[k])
			k++
		}
		runs = append(runs, r)
	}
	runs = append(runs, fresh[k:]...)
	ix.snap.Store(cur.withExperiment(expID, cur.manifests[expID], runs, old))
	return nil
}

// RefreshExperiment re-indexes one experiment and swaps it into a copy of
// the current snapshot: the disk work is that experiment only, not every
// experiment of the organization as Rebuild does.
func (ix *Index) RefreshExperiment(ctx context.Context, expID string) error {
	if err := validateID("exp_id", expID); err != nil {
		return err
	}
	ix.rebuild.Lock()
	defer ix.rebuild.Unlock()
	return ix.refreshExperimentLocked(ctx, ix.snap.Load(), expID)
}

func (ix *Index) refreshExperimentLocked(ctx context.Context, cur *indexSnapshot, expID string) error {
	m, runs, err := ix.store.indexExperiment(ctx, expID)
	if err != nil {
		return fmt.Errorf("refresh experiment %s: %w", expID, err)
	}
	ix.snap.Store(cur.withExperiment(expID, m, runs, cur.runsByExp[expID]))
	return nil
}

// withExperiment returns a copy of the snapshot with one experiment's runs
// replaced. The per-experiment maps are copied (pointers and small structs
// in memory); old is the experiment's previous run list, whose run and pair
// entries are dropped first.
func (s *indexSnapshot) withExperiment(expID string, m *schemas.Manifest, runs, old []RunEntry) *indexSnapshot {
	next := &indexSnapshot{
		experiments: s.experiments,
		manifests:   make(map[string]*schemas.Manifest, len(s.manifests)+1),
		runsByExp:   make(map[string][]RunEntry, len(s.runsByExp)+1),
		byRun:       make(map[string]RunEntry, len(s.byRun)+len(runs)-len(old)),
		byPair:      make(map[string][]RunEntry, len(s.byPair)),
	}
	for k, v := range s.manifests {
		next.manifests[k] = v
	}
	for k, v := range s.runsByExp {
		next.runsByExp[k] = v
	}
	next.manifests[expID] = m
	next.runsByExp[expID] = runs
	if _, ok := s.manifests[expID]; !ok {
		next.experiments = append(append([]string(nil), s.experiments...), expID)
		sort.Strings(next.experiments)
	}
	drop := make(map[string]bool, len(old))
	pairs := map[string]bool{}
	for _, r := range old {
		drop[r.Run.RunID] = true
		if r.Slot.PairID != "" {
			pairs[r.Slot.PairID] = true
		}
	}
	for _, r := range runs {
		if r.Slot.PairID != "" {
			pairs[r.Slot.PairID] = true
		}
	}
	for k, v := range s.byRun {
		if !drop[k] {
			next.byRun[k] = v
		}
	}
	for _, r := range runs {
		next.byRun[r.Run.RunID] = r
	}
	for k, v := range s.byPair {
		if !pairs[k] {
			next.byPair[k] = v
		}
	}
	// The pairs this experiment touches are built again as Rebuild builds
	// them: experiments in id order, runs in index order, then sorted by arm
	// and attempt. Pair ids are not unique across experiments.
	if len(pairs) > 0 {
		for _, id := range next.experiments {
			for _, r := range next.runsByExp[id] {
				if r.Sealed && pairs[r.Slot.PairID] {
					next.byPair[r.Slot.PairID] = append(next.byPair[r.Slot.PairID], r)
				}
			}
		}
		for k := range pairs {
			if v, ok := next.byPair[k]; ok {
				sortPair(v)
			}
		}
	}
	return next
}

// slotBefore is the index's slot order: order index, then slot id.
func slotBefore(a, b schemas.Slot) bool {
	if a.OrderIndex != b.OrderIndex {
		return a.OrderIndex < b.OrderIndex
	}
	return a.SlotID < b.SlotID
}

// sortPair orders a pair's sealed runs by arm, then attempt, as Rebuild does.
func sortPair(runs []RunEntry) {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Slot.Arm != runs[j].Slot.Arm {
			return runs[i].Slot.Arm < runs[j].Slot.Arm
		}
		return runs[i].Run.Attempt < runs[j].Run.Attempt
	})
}

// VerifyLoggedRun runs Verify's checks on the sealed run folder dir with the
// seal hash the caller read from seals.log, so a caller that already parsed
// the log (a mirror pass) does not make every run read it again.
func (s *FSStore) VerifyLoggedRun(ctx context.Context, dir, runID, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateID("run_id", runID); err != nil {
		return err
	}
	if _, err := s.verifySealedRun(dir, runID, map[string]string{runID: hash}); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	var run schemas.Run
	if err := readJSON(filepath.Join(dir, runFile), &run); err != nil {
		return fmt.Errorf("verify run %s: read run: %w", runID, err)
	}
	if err := verifyCanonicalFile(dir, &run); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	return nil
}
