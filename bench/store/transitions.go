package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// AppendTransition appends one JSON line to the run's transitions.jsonl with
// O_APPEND and fsyncs before returning, so resume never sees a stage as done
// that was not durably recorded. A sealed run refuses with schemas.ErrSealed.
func (s *FSStore) AppendTransition(ctx context.Context, run *schemas.Run, t schemas.Transition) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("append transition: nil run")
	}
	if err := validateRunIDs(run); err != nil {
		return fmt.Errorf("append transition: %w", err)
	}
	line, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("append transition for run %s: %w", run.RunID, err)
	}
	unlock := s.runLocks.lock(run.RunID)
	defer unlock()
	dir, err := s.openRunDir(run)
	if err != nil {
		return fmt.Errorf("append transition: %w", err)
	}
	if err := appendLine(filepath.Join(dir, transitionsFile), line); err != nil {
		return fmt.Errorf("append transition for run %s: %w", run.RunID, err)
	}
	return nil
}

// SlotRuns returns every run of a slot, sealed or not, sorted by attempt, with
// its transitions. A run folder whose run.json is missing or unreadable (a
// crash between mkdir and the first write) is returned with its ids only.
func (s *FSStore) SlotRuns(ctx context.Context, expID, slotID string) ([]schemas.RunRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("slot runs: %w", err)
	}
	if err := validateID("slot_id", slotID); err != nil {
		return nil, fmt.Errorf("slot runs: %w", err)
	}
	var slot schemas.Slot
	if err := readJSON(filepath.Join(s.expDir(expID), slotsDir, slotID, slotFile), &slot); err != nil {
		return nil, fmt.Errorf("slot runs %s: %w", slotID, err)
	}
	recs, err := s.loadSlotRuns(&slot, true)
	if err != nil {
		return nil, fmt.Errorf("slot runs %s: %w", slotID, err)
	}
	return recs, nil
}

// loadSlotRuns reads every run folder of a slot. Transitions are parsed only
// when withTransitions is set; the index does not need them.
func (s *FSStore) loadSlotRuns(slot *schemas.Slot, withTransitions bool) ([]schemas.RunRecord, error) {
	base := filepath.Join(s.expDir(slot.ExpID), slotsDir, slot.SlotID, runsDir)
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list runs: %w", err)
	}
	var out []schemas.RunRecord
	for _, e := range entries {
		if !e.IsDir() || validateID("run_id", e.Name()) != nil {
			continue
		}
		dir := filepath.Join(base, e.Name())
		rec := schemas.RunRecord{Dir: dir}
		if err := readJSON(filepath.Join(dir, runFile), &rec.Run); err != nil {
			s.logger.Warn("run.json unreadable, using folder ids", "run_id", e.Name(), "error", err.Error())
			rec.Run = schemas.Run{RunID: e.Name(), SlotID: slot.SlotID, PairID: slot.PairID, ExpID: slot.ExpID}
		}
		if rec.Run.RunID != e.Name() {
			return nil, fmt.Errorf("run folder %s holds run.json of %s", e.Name(), rec.Run.RunID)
		}
		if rec.Sealed, err = isSealed(dir); err != nil {
			return nil, fmt.Errorf("run %s: %w", e.Name(), err)
		}
		if withTransitions {
			if rec.Transitions, err = s.readTransitions(filepath.Join(dir, transitionsFile), e.Name()); err != nil {
				return nil, fmt.Errorf("run %s: %w", e.Name(), err)
			}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Run.Attempt != out[j].Run.Attempt {
			return out[i].Run.Attempt < out[j].Run.Attempt
		}
		return out[i].Run.RunID < out[j].Run.RunID
	})
	return out, nil
}

// readTransitions parses transitions.jsonl. A line that does not decode is
// skipped with a warning and never fails the read: the last one is a write
// torn by a crash, and an earlier one is such a torn write that a later
// append moved off the end (appendLine starts a fresh line after it).
func (s *FSStore) readTransitions(p, runID string) ([]schemas.Transition, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read transitions: %w", err)
	}
	lines := bytes.Split(b, []byte{'\n'})
	var out []schemas.Transition
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var t schemas.Transition
		if err := json.Unmarshal(line, &t); err != nil {
			if i == len(lines)-1 {
				s.logger.Warn("torn last transition line ignored", "run_id", runID, "bytes", len(line))
			} else {
				s.logger.Warn("corrupt transition line ignored", "run_id", runID, "line", i+1)
			}
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// appendLine appends line plus a newline in one write and fsyncs. When the
// file does not end in a newline (a torn earlier write), it first ends that
// line, so the torn bytes never merge with the new record.
func appendLine(p string, line []byte) error {
	f, err := os.OpenFile(p, os.O_RDWR|os.O_APPEND|os.O_CREATE, filePerm)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(p), err)
	}
	werr := appendLineTo(f, line)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("append %s: %w", filepath.Base(p), err)
	}
	return nil
}

func appendLineTo(f *os.File, line []byte) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	buf := make([]byte, 0, len(line)+2)
	if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if last[0] != '\n' {
			buf = append(buf, '\n')
		}
	}
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		return err
	}
	return f.Sync()
}
