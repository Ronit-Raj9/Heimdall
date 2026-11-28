// Package taskguard is the quality guard of generated tasks: a set of extra
// admission checks that run only for tasks a builder made (from a trace, an
// issue, an API document or a policy document), so a task that can be passed
// by doing nothing, gives its answer away, flips between runs, is unclear,
// has no recorded origin or holds personal data never joins a benchmark.
package taskguard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// File is the guard record in the task folder; written by builders, read at
// admission.
const File = "guard.json"

// Builder of a generated task, from its provenance.
const (
	BuilderTrace  = "trace"  // created_by "task from-trace" or "task from-session"
	BuilderIssue  = "issue"  // "heimdall issuebench emit"
	BuilderAPI    = "api"    // "api policy generator"
	BuilderPolicy = "policy" // prefix "policy generator"
)

// Clarity verdicts and origins.
const (
	ClarityClear      = "clear"
	ClarityUnclear    = "unclear"
	ClarityNotChecked = "not_checked"

	OriginModel  = "model"
	OriginPerson = "person"
)

// Record is guard.json: what the builder already knows when it writes the
// task.
type Record struct {
	Builder     string    `json:"builder"`
	Source      string    `json:"source"`                // the trace file sha256, the commit pair, or the draft id
	Clarity     *Clarity  `json:"clarity,omitempty"`     // model check of the instruction
	LeakTexts   []string  `json:"leak_texts,omitempty"`  // strings the instruction must not hold (answers, fix lines), redacted
	FlakyTests  []string  `json:"flaky_tests,omitempty"` // issue builder: Validation.Flaky
	Determinism string    `json:"determinism,omitempty"` // issue builder: "3 of 3 rounds agree"
	WrittenAt   time.Time `json:"written_at"`
}

// Clarity is the check of the instruction's wording.
type Clarity struct {
	Verdict  string           `json:"verdict"` // "clear" | "unclear" | "not_checked"
	Problems []ClarityProblem `json:"problems,omitempty"`
	Origin   string           `json:"origin"` // "model" (issue builder check) | "person" (approved rules)
}

// ClarityProblem is one unclear passage of the instruction.
type ClarityProblem struct {
	Quote   string `json:"quote"`   // exact text of instruction.md, checked with fold
	Problem string `json:"problem"` // the model's sentence, shown as "Suggested"
}

// Write writes r to <taskDir>/guard.json, setting WrittenAt when it is zero.
func Write(taskDir string, r Record) error {
	if r.WrittenAt.IsZero() {
		r.WrittenAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", File, err)
	}
	path := filepath.Join(taskDir, File)
	tmp, err := os.CreateTemp(taskDir, ".guard.*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
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

// Read reads <taskDir>/guard.json. A missing file is nil, nil.
func Read(taskDir string) (*Record, error) {
	path := filepath.Join(taskDir, File)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &r, nil
}
