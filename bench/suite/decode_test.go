package suite

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidateSchemas(t *testing.T) {
	if err := ValidateSchemas(); err != nil {
		t.Fatalf("embedded schemas do not compile: %v", err)
	}
}

func TestDecodeYAMLInvalid(t *testing.T) {
	tests := []struct {
		file   string
		schema string
		out    any
		want   []string
	}{
		{"task-unknown-field.yaml", SchemaTask, &schemas.Task{}, []string{"task-unknown-field.yaml", "controls.exploitt: unknown field"}},
		{"task-missing-exploit.yaml", SchemaTask, &schemas.Task{}, []string{"controls.exploit: required field missing"}},
		{"task-bad-kind.yaml", SchemaTask, &schemas.Task{}, []string{"kind:", "version:", "instruction:", "timeouts.agent_s:"}},
		{"probe-wrong-block.yaml", SchemaProbe, &schemas.ProbeSpec{}, []string{"apply.fault: required field missing"}},
		{"experiment-typo-set.yaml", SchemaExperiment, &schemas.Experiment{}, []string{"arms.B.set.modle: unknown field", "conditions[0].tasks"}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("testdata", "invalid", tc.file)
			err := DecodeYAMLFile(path, tc.file, tc.schema, tc.out)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestDecodeYAMLValid(t *testing.T) {
	agent := `
id: claude-code
adapter: command
image: bench/agent-claude-code:1
argv: ["claude", "-p", "{{instruction}}"]
env:
  ANTHROPIC_BASE_URL: "{{gateway.url}}/anthropic"
  CLAUDE_CODE_DISABLE_AUTO_MEMORY: "1"
model: claude-sonnet-5-5
notes:
  project_file: CLAUDE.md
  user_file: null
  user_path: /tmp/cc/CLAUDE.md
parser: claudecode
budgets: {wall_s: 900, turns: 60, cpus: 2, mem_gb: 4}
gateway: {provider: anthropic, max_retries: 0}
`
	experiment := `
name: code-wins-sentence
suite: coding-core@1.0.0
base: agents/claude-code.yaml
arms:
  A: {label: baseline}
  B: {label: code-wins sentence, set: {notes.user_file: fixtures/notes/codewins.md}}
primary: {condition: stale-notes, metric: verified_success}
guards:
  - {condition: clean, rule: no_regression}
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 3}
  - {id: model-outage, tasks: [t01, t04, t06], trials: 2}
compatibility:
  agent: agents/second-agent.yaml
  tasks: [t01, t02]
  conditions: [clean, stale-notes]
  trials: 1
budget: {total_usd: 40}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261114
concurrency: 4
`
	t.Run("agent", func(t *testing.T) {
		var a schemas.AgentConfig
		if err := DecodeYAML([]byte(agent), "agent.yaml", SchemaAgent, &a); err != nil {
			t.Fatal(err)
		}
		if a.Budgets.CPUs != 2 || a.Notes.ProjectFile != "CLAUDE.md" || a.Notes.UserFile != "" {
			t.Errorf("decoded %+v", a)
		}
	})
	t.Run("experiment", func(t *testing.T) {
		var e schemas.Experiment
		if err := DecodeYAML([]byte(experiment), "exp.yaml", SchemaExperiment, &e); err != nil {
			t.Fatal(err)
		}
		if !e.Conditions[0].Tasks.All || len(e.Conditions[1].Tasks.IDs) != 3 {
			t.Errorf("task selectors: %+v", e.Conditions)
		}
		if e.Arms["B"].Set["notes.user_file"] != "fixtures/notes/codewins.md" {
			t.Errorf("arm set: %+v", e.Arms["B"].Set)
		}
	})
	t.Run("task from task init", func(t *testing.T) {
		task := `
id: t01
version: 1.0.0
title: "Fix rounding"
project: "app"
kind: bugfix
instruction: instruction.md
repo_dir: repo
verifier:
  tests: verifier/tests
  required: []
protected: []
controls:
  reference: solution/reference.diff
  empty: controls/empty.diff
  mutants: []
  exploit: controls/exploit.sh
timeouts:
  agent_s: 900
capabilities: ["test_discipline", "planning"]
difficulty: medium
provenance:
  source: "https://example.com/org/app.git"
  base_commit: 0123456789abcdef0123456789abcdef01234567
  reference_commit: 89abcdef0123456789abcdef0123456789abcdef
  license: "proprietary"
  created_by: task init
`
		var tk schemas.Task
		if err := DecodeYAML([]byte(task), "task.yaml", SchemaTask, &tk); err != nil {
			t.Fatal(err)
		}
		if tk.Difficulty != "medium" || len(tk.Capabilities) != 2 || tk.Provenance == nil || tk.Provenance.CreatedBy != "task init" || tk.Notes.Clean != "" {
			t.Errorf("decoded %+v", tk)
		}
		for _, bad := range []struct{ from, to, want string }{
			{"difficulty: medium", "difficulty: extreme", "difficulty"},
			{"created_by: task init", "created_by: task init\n  author: me", "provenance.author: unknown field"},
			{"base_commit: 0123456789abcdef0123456789abcdef01234567", "base_commit: main", "provenance.base_commit"},
			{"required: []", "required: [\"just a name\"]", "verifier.required[0]"},
		} {
			err := DecodeYAML([]byte(strings.Replace(task, bad.from, bad.to, 1)), "task.yaml", SchemaTask, &schemas.Task{})
			if err == nil || !strings.Contains(err.Error(), bad.want) {
				t.Errorf("%s: want error naming %s, got %v", bad.to, bad.want, err)
			}
		}
		for _, id := range []string{"tests/test_a.py::TestC::test_b[1]", "example.com/mod/pkg::TestAdd", "test::adds two numbers"} {
			ok := strings.Replace(task, "required: []", "required: ["+strconv.Quote(id)+"]", 1)
			if err := DecodeYAML([]byte(ok), "task.yaml", SchemaTask, &schemas.Task{}); err != nil {
				t.Errorf("required id %q rejected: %v", id, err)
			}
		}
	})
	t.Run("agent cpu typo", func(t *testing.T) {
		bad := strings.Replace(agent, "cpus: 2", "cpu: 2", 1)
		err := DecodeYAML([]byte(bad), "agent.yaml", SchemaAgent, &schemas.AgentConfig{})
		if err == nil || !strings.Contains(err.Error(), "budgets.cpu: unknown field") {
			t.Fatalf("want budgets.cpu unknown field, got %v", err)
		}
	})
}

func TestDecodeStrict(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"known fields", "id: t01\nversion: 1.0.0\n", ""},
		{"unknown field", "id: t01\nversoin: 1.0.0\n", "field versoin not found"},
		{"two documents", "id: t01\n---\nid: t02\n", "more than one YAML document"},
		{"empty", "", "empty document"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var task schemas.Task
			err := decodeStrict([]byte(tc.doc), &task)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestFieldPath(t *testing.T) {
	tests := []struct {
		loc  []string
		want string
	}{
		{nil, "(document)"},
		{[]string{"controls", "mutants", "0"}, "controls.mutants[0]"},
		{[]string{"arms", "B", "set"}, "arms.B.set"},
	}
	for _, tc := range tests {
		if got := fieldPath(tc.loc); got != tc.want {
			t.Errorf("fieldPath(%v) = %q, want %q", tc.loc, got, tc.want)
		}
	}
}
