// Package taskcat labels tasks with a kind of work (bug fix, refactor, test
// repair, ...) so a report card can show the score per kind. Labels live in
// a sidecar at the suite root, never in task.yaml, so labelling a task never
// changes its fingerprint and never un-admits it.
package taskcat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// File is the sidecar at the suite root; the leading dot keeps it out of the
// suite fingerprint (every root file named ".suite.*" is skipped).
const File = ".suite.categories.json"

// Categories (exact values).
const (
	BugFix           = "bug_fix"
	Feature          = "feature"
	Refactor         = "refactor"
	TestRepair       = "test_repair"
	DependencyUpdate = "dependency_update"
	Performance      = "performance"
	Docs             = "docs"
	Navigation       = "navigation"
	ToolUse          = "tool_use"
	Policy           = "policy"
	Conversation     = "conversation"
)

// All lists every category in display order.
func All() []string {
	return []string{BugFix, Feature, Refactor, TestRepair, DependencyUpdate, Performance, Docs, Navigation, ToolUse, Policy, Conversation}
}

// Valid reports whether c is a known category.
func Valid(c string) bool {
	for _, x := range All() {
		if x == c {
			return true
		}
	}
	return false
}

// Label sources.
const (
	SourceRule   = "rule"
	SourcePerson = "person"
	SourceModel  = "model" // reserved for a later wave
)

// Label is one task's category.
type Label struct {
	Category string    `json:"category"`
	Source   string    `json:"source"` // "rule" | "person" ("model" is reserved for a later wave)
	Sure     bool      `json:"sure"`   // false: shown as "{category} (unsure)"
	Why      string    `json:"why"`    // the rule name, or the model's sentence, or the person's name
	At       time.Time `json:"at"`
}

// Inputs are the facts the rules read.
type Inputs struct {
	Task          *schemas.Task
	ChangedFiles  []string // reference diff paths, when the task has one
	CommitSubject string   // provenance or issue title
	IssueLabels   []string
	SessionTools  []string // trace tasks: tool names of the session
}

// Rules labels from facts, in this order, first match wins: api or policy
// builder: policy; trace builder: kind qa or memory with only read tools:
// navigation, qa or memory: conversation, tool-use or mcp: tool_use; every
// changed file a test file: test_repair; every changed file a manifest or
// lock file: dependency_update; commit subject prefixes; issue labels; the
// task's kind (not sure). Otherwise ok is false.
func Rules(in Inputs) (Label, bool) {
	t := in.Task
	sure := func(c, why string) (Label, bool) {
		return Label{Category: c, Source: SourceRule, Sure: true, Why: why}, true
	}
	builder := builderOf(t)
	if builder == "api" || builder == "policy" {
		return sure(Policy, "policy builder")
	}
	if builder == "trace" {
		kind := traceKind(t)
		switch kind {
		case "qa", "memory":
			if len(in.SessionTools) > 0 && allOf(in.SessionTools, isReadTool) {
				return sure(Navigation, "read-only session")
			}
			return sure(Conversation, "question or memory session")
		case "tool-use", "mcp":
			return sure(ToolUse, "tool session")
		}
	}
	if len(in.ChangedFiles) > 0 && allOf(in.ChangedFiles, isTestFile) {
		return sure(TestRepair, "only test files changed")
	}
	if len(in.ChangedFiles) > 0 && allOf(in.ChangedFiles, isManifestFile) {
		return sure(DependencyUpdate, "only manifest or lock files changed")
	}
	subj := strings.ToLower(strings.TrimSpace(in.CommitSubject))
	switch {
	case strings.HasPrefix(subj, "fix:") || strings.HasPrefix(subj, "fix(") || strings.HasPrefix(subj, "bug") || strings.Contains(subj, "fixes #"):
		return sure(BugFix, "commit subject")
	case strings.HasPrefix(subj, "feat:") || strings.HasPrefix(subj, "feat(") || strings.HasPrefix(subj, "add "):
		return sure(Feature, "commit subject")
	case strings.HasPrefix(subj, "refactor:") || strings.HasPrefix(subj, "refactor("):
		return sure(Refactor, "commit subject")
	case strings.HasPrefix(subj, "perf:") || strings.HasPrefix(subj, "perf("):
		return sure(Performance, "commit subject")
	case strings.HasPrefix(subj, "docs:") || strings.HasPrefix(subj, "docs("):
		return sure(Docs, "commit subject")
	}
	for _, l := range in.IssueLabels {
		switch strings.ToLower(strings.TrimSpace(l)) {
		case "bug":
			return sure(BugFix, "issue label")
		case "enhancement", "feature":
			return sure(Feature, "issue label")
		}
	}
	if t != nil {
		m := map[schemas.TaskKind]string{schemas.TaskBugfix: BugFix, schemas.TaskFeature: Feature, schemas.TaskRefactor: Refactor, schemas.TaskPerformance: Performance}
		if c, ok := m[t.Kind]; ok {
			return Label{Category: c, Source: SourceRule, Sure: false, Why: "task kind"}, true
		}
	}
	return Label{}, false
}

func builderOf(t *schemas.Task) string {
	if t == nil || t.Provenance == nil {
		return ""
	}
	by := strings.TrimSpace(t.Provenance.CreatedBy)
	switch {
	case by == "task from-trace" || by == "task from-session":
		return "trace"
	case by == "heimdall issuebench emit":
		return "issue"
	case by == "api policy generator":
		return "api"
	case strings.HasPrefix(by, "policy generator"):
		return "policy"
	}
	return ""
}

// traceKind is the template a trace task was drafted with: qa when it is
// graded on an answer (qa and memory drafts), tool-use when it is graded by
// checks.yaml (tool-use and mcp drafts). A tag naming the template wins.
func traceKind(t *schemas.Task) string {
	for _, tag := range t.Tags {
		switch tag {
		case "qa", "memory", "tool-use", "mcp":
			return tag
		}
	}
	if t.Verifier.Answer != "" {
		return "qa"
	}
	tests := t.Verifier.Tests
	if tests == "" {
		tests = "verifier"
	}
	if _, err := os.Stat(filepath.Join(t.Dir, tests, "checks.yaml")); err == nil && t.Dir != "" {
		return "tool-use"
	}
	return ""
}

func allOf(l []string, f func(string) bool) bool {
	for _, s := range l {
		if !f(s) {
			return false
		}
	}
	return true
}

var readTools = map[string]bool{
	"read": true, "read_file": true, "grep": true, "glob": true, "ls": true, "list_files": true, "list_dir": true,
	"search": true, "find": true, "view": true, "cat": true, "websearch": true, "webfetch": true, "notebookread": true,
}

func isReadTool(name string) bool { return readTools[strings.ToLower(name)] }

func isTestFile(p string) bool {
	p = strings.ToLower(filepath.ToSlash(p))
	base := path.Base(p)
	if strings.HasPrefix(p, "test/") || strings.HasPrefix(p, "tests/") || strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") || strings.Contains(p, "__tests__/") {
		return true
	}
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

var manifestFiles = map[string]bool{
	"go.mod": true, "go.sum": true, "package.json": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"requirements.txt": true, "pyproject.toml": true, "poetry.lock": true, "pipfile": true, "pipfile.lock": true, "uv.lock": true,
	"cargo.toml": true, "cargo.lock": true, "gemfile": true, "gemfile.lock": true, "pom.xml": true, "build.gradle": true,
	"composer.json": true, "composer.lock": true, "setup.cfg": true,
}

func isManifestFile(p string) bool { return manifestFiles[strings.ToLower(path.Base(filepath.ToSlash(p)))] }

// Read reads the sidecar of suiteRoot. A missing file is an empty map.
func Read(suiteRoot string) (map[string]Label, error) {
	p := filepath.Join(suiteRoot, File)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Label{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	out := map[string]Label{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return out, nil
}

// Set writes taskID's label. It never replaces a "person" label with a
// "rule" or "model" one.
func Set(suiteRoot, taskID string, l Label) error {
	if !Valid(l.Category) {
		return fmt.Errorf("unknown category %q", l.Category)
	}
	unlock, err := lockFile(filepath.Join(suiteRoot, ".suite.categories.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	cur, err := Read(suiteRoot)
	if err != nil {
		return err
	}
	if old, ok := cur[taskID]; ok && old.Source == SourcePerson && l.Source != SourcePerson {
		return nil
	}
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	cur[taskID] = l
	data, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(suiteRoot, File)
	tmp, err := os.CreateTemp(suiteRoot, ".suite.categories.*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}
