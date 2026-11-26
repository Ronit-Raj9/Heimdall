package improve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InterruptedReason is the reason a run the server restart left behind
// carries (A1.7).
const InterruptedReason = "The server restarted during this run. Press Resume to continue; nothing already paid for runs again."

// resumeRefusal is a plain refusal of a resume; it reads as ErrState or
// ErrBusy.
type resumeRefusal struct {
	text string
	is   error
}

func (e resumeRefusal) Error() string { return e.text }
func (e resumeRefusal) Unwrap() error { return e.is }

// CanResume reports whether a run may be resumed: planned, failed and
// interrupted runs may; a running one or a finished one may not.
func (r *Runner) CanResume(id string) (*Record, error) {
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if r.Running(id) || rec.Status == StatusRunning {
		return rec, resumeRefusal{"This run is already running.", ErrBusy}
	}
	return rec, resumable(rec)
}

func resumable(rec *Record) error {
	switch rec.Status {
	case StatusPlanned, StatusFailed, StatusInterrupted:
		return nil
	}
	return resumeRefusal{fmt.Sprintf("This run is %s; it cannot be resumed.", statusWords(rec.Status)), ErrState}
}

func statusWords(s string) string {
	if s == StatusAwaiting {
		return "awaiting approval"
	}
	return s
}

// AnyRunning reports whether this runner drives any loop, so a hosted org
// runtime is not evicted under one.
func (r *Runner) AnyRunning() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running) > 0
}

// MarkInterrupted sets every record under root that says running to
// interrupted, with the plain reason. It is called at start-up, before any
// loop runs, and answers how many it changed.
func MarkInterrupted(root string) (int, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	r := &Runner{Root: root}
	n := 0
	var errs []error
	for _, e := range ents {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), RecordFile)); err != nil {
			continue
		}
		rec, err := r.load(e.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if rec.Status != StatusRunning {
			continue
		}
		rec.Status, rec.Reason = StatusInterrupted, InterruptedReason
		if err := r.save(rec); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// ResumeRunning lists the records under the runner's root that say running:
// on a local server they are driven again at start-up.
func (r *Runner) ResumeRunning() ([]string, error) {
	recs, err := r.List()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, rec := range recs {
		if rec.Status == StatusRunning && !r.Running(rec.ID) {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}
