package suite

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The quarantine sidecar (schemas.SuiteHealthFileName) lives beside
// suite.lock and is read by the planner and the API. It is deliberately not a
// lock field: quarantining a task is a reading of the record, not a change to
// the task, so the lock file and its hash stay byte-identical and every
// manifest and seal written before keeps verifying. Analysis of an old
// experiment never reads it.

// ReadHealthFile reads <root>/suite.health.json. A missing file is an empty
// record, never an error.
func ReadHealthFile(root string) (*schemas.SuiteHealthFile, error) {
	path := filepath.Join(root, schemas.SuiteHealthFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &schemas.SuiteHealthFile{Quarantined: map[string]schemas.TaskQuarantine{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f schemas.SuiteHealthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Quarantined == nil {
		f.Quarantined = map[string]schemas.TaskQuarantine{}
	}
	return &f, nil
}

// WriteHealthFile writes f to <root>/suite.health.json as indented JSON
// through a temp file and a rename. A record with no quarantined task removes
// the file, so a fully restored suite looks the same as one never touched.
func WriteHealthFile(root string, f *schemas.SuiteHealthFile) error {
	path := filepath.Join(root, schemas.SuiteHealthFileName)
	if f == nil || len(f.Quarantined) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(root, ".suite.health.*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Quarantine records task as quarantined in the suite's sidecar. The task
// must be loaded in s and the reason must not be empty: a quarantine nobody
// can explain later is worse than none. Quarantining a task again replaces
// its record.
func Quarantine(s *schemas.Suite, taskID string, q schemas.TaskQuarantine) error {
	t := s.TaskByID(taskID)
	if t == nil {
		return fmt.Errorf("quarantine: task %s is not in suite %s", taskID, s.ID)
	}
	if strings.TrimSpace(q.Reason) == "" {
		return fmt.Errorf("quarantine %s/%s: a reason is required", s.ID, taskID)
	}
	if q.Source == "" {
		q.Source = schemas.QuarantineManual
	}
	if q.At.IsZero() {
		q.At = time.Now().UTC()
	}
	if q.TaskVersion == "" {
		q.TaskVersion = t.Version
	}
	f, err := ReadHealthFile(s.Root)
	if err != nil {
		return err
	}
	f.Suite = s.ID
	f.Quarantined[taskID] = q
	return WriteHealthFile(s.Root, f)
}

// Restore removes task from the suite's quarantine. It reports whether the
// task was quarantined.
func Restore(s *schemas.Suite, taskID string) (bool, error) {
	f, err := ReadHealthFile(s.Root)
	if err != nil {
		return false, err
	}
	if _, ok := f.Quarantined[taskID]; !ok {
		return false, nil
	}
	delete(f.Quarantined, taskID)
	f.Suite = s.ID
	return true, WriteHealthFile(s.Root, f)
}

// TaskSplit returns a task's split, public when unset.
func TaskSplit(t *schemas.Task) string {
	if t == nil || t.Split == "" {
		return schemas.SplitPublic
	}
	return t.Split
}
