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

// The review sidecar (schemas.TaskReviewFileName) records a person's
// decision on each generated task. Like the quarantine sidecar it is not a
// lock field: accepting a task changes no hash, so its readiness record and
// every manifest stay valid.

// ReadReviewFile reads <root>/suite.review.json. A missing file is an empty
// record, never an error.
func ReadReviewFile(root string) (*schemas.TaskReviewFile, error) {
	path := filepath.Join(root, schemas.TaskReviewFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &schemas.TaskReviewFile{Decisions: map[string]schemas.TaskReviewDecision{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f schemas.TaskReviewFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Decisions == nil {
		f.Decisions = map[string]schemas.TaskReviewDecision{}
	}
	return &f, nil
}

// EncodeReviewFile is the sidecar's bytes, nil for a record with no
// decision (the file is then removed).
func EncodeReviewFile(f *schemas.TaskReviewFile) ([]byte, error) {
	if f == nil || len(f.Decisions) == 0 {
		return nil, nil
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", schemas.TaskReviewFileName, err)
	}
	return append(data, '\n'), nil
}

// WriteReviewFile writes f to <root>/suite.review.json through a temp file
// and a rename. A record with no decision removes the file.
func WriteReviewFile(root string, f *schemas.TaskReviewFile) error {
	path := filepath.Join(root, schemas.TaskReviewFileName)
	data, err := EncodeReviewFile(f)
	if err != nil {
		return err
	}
	if data == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	tmp, err := os.CreateTemp(root, ".suite.review.*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
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

// ReviewDecision builds the decision on generated task t: accepted or
// rejected, by a named person. A rejection needs a note, so the author
// knows what to fix. A task no model helped write needs no review.
func ReviewDecision(t *schemas.Task, decision, by, note string, at time.Time) (schemas.TaskReviewDecision, error) {
	if t == nil {
		return schemas.TaskReviewDecision{}, errors.New("review: no task")
	}
	if !t.IsGenerated() {
		return schemas.TaskReviewDecision{}, fmt.Errorf("task %s was written by a person and needs no review", t.ID)
	}
	switch decision {
	case schemas.ReviewAccepted, schemas.ReviewRejected:
	default:
		return schemas.TaskReviewDecision{}, fmt.Errorf("decision %q: want accepted or rejected", decision)
	}
	note = strings.TrimSpace(note)
	if decision == schemas.ReviewRejected && note == "" {
		return schemas.TaskReviewDecision{}, errors.New("a rejection needs a note saying what is wrong")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return schemas.TaskReviewDecision{Decision: decision, By: strings.TrimSpace(by), At: at.UTC(), Note: note,
		TaskVersion: t.Version, Generated: schemas.GeneratedFingerprint(t.GeneratedFields())}, nil
}

// Review records a person's decision on generated task taskID of s in the
// review sidecar.
func Review(s *schemas.Suite, taskID, decision, by, note string) (schemas.TaskReviewDecision, error) {
	t := s.TaskByID(taskID)
	if t == nil {
		return schemas.TaskReviewDecision{}, fmt.Errorf("review: task %s is not in suite %s", taskID, s.ID)
	}
	d, err := ReviewDecision(t, decision, by, note, time.Time{})
	if err != nil {
		return d, err
	}
	f, err := ReadReviewFile(s.Root)
	if err != nil {
		return d, err
	}
	f.Suite = s.ID
	f.Decisions[taskID] = d
	return d, WriteReviewFile(s.Root, f)
}

// ReviewStates is the review state of every task of s, by task id.
func ReviewStates(s *schemas.Suite) (map[string]string, error) {
	f, err := ReadReviewFile(s.Root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(s.Tasks))
	for _, t := range s.Tasks {
		out[t.ID] = schemas.TaskReviewState(t, f)
	}
	return out, nil
}
