package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const baseAgent = `id: claude-code
adapter: command
image: bench/agent:1
env: {A: "1"}
model: claude-test
notes:
  project_file: CLAUDE.md
  user_path: /home/agent/.claude/CLAUDE.md
budgets: {wall_s: 900, cpus: 2, mem_gb: 4}
gateway: {provider: anthropic}
`

func TestResolveAgentConfig(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		set     map[string]any
		check   func(t *testing.T, cfgModel, userFile string, wall int, env map[string]string, scripted string)
		wantErr string
	}{
		{name: "no overrides", doc: baseAgent, check: func(t *testing.T, model, uf string, wall int, _ map[string]string, _ string) {
			if model != "claude-test" || uf != "" || wall != 900 {
				t.Fatalf("base changed: %s %s %d", model, uf, wall)
			}
		}},
		{name: "notes.user_file, model and budgets.wall_s", doc: baseAgent,
			set: map[string]any{"notes.user_file": "experiments/fixtures/codewins.md", "model": "claude-other", "budgets.wall_s": 600},
			check: func(t *testing.T, model, uf string, wall int, _ map[string]string, _ string) {
				if model != "claude-other" || uf != "experiments/fixtures/codewins.md" || wall != 600 {
					t.Fatalf("got %s %s %d", model, uf, wall)
				}
			}},
		{name: "map key", doc: baseAgent, set: map[string]any{"env.NEW_VAR": "x"},
			check: func(t *testing.T, _, _ string, _ int, env map[string]string, _ string) {
				if env["NEW_VAR"] != "x" || env["A"] != "1" {
					t.Fatalf("env %v", env)
				}
			}},
		{name: "pointer struct created", doc: baseAgent, set: map[string]any{"scripted.kind": "oracle"},
			check: func(t *testing.T, _, _ string, _ int, _ map[string]string, scripted string) {
				if scripted != "oracle" {
					t.Fatalf("scripted.kind %q", scripted)
				}
			}},
		{name: "misspelled field", doc: baseAgent, set: map[string]any{"notes.userfile": "x"}, wantErr: `no field "userfile"`},
		{name: "unknown top-level field", doc: baseAgent, set: map[string]any{"modle": "x"}, wantErr: `no field "modle"`},
		{name: "below a scalar", doc: baseAgent, set: map[string]any{"budgets.wall_s.max": 1}, wantErr: "has no field"},
		{name: "slice index", doc: baseAgent, set: map[string]any{"argv.0": "x"}, wantErr: "has no field"},
		{name: "wrong value type", doc: baseAgent, set: map[string]any{"budgets.wall_s": "soon"}, wantErr: "decode agent config"},
		{name: "unknown field in base file", doc: baseAgent + "extra: 1\n", wantErr: "field extra not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ResolveAgentConfig([]byte(tt.doc), tt.set)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			kind := ""
			if cfg.Scripted != nil {
				kind = cfg.Scripted.Kind
			}
			tt.check(t, cfg.Model, cfg.Notes.UserFile, cfg.Budgets.WallS, cfg.Env, kind)
		})
	}
}

func TestCheckOverridePath(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{"notes.user_file", true},
		{"notes.user_path", true},
		{"model", true},
		{"budgets.wall_s", true},
		{"gateway.fallbacks", true},
		{"files./tmp/x", true},
		{"build.tag", true},
		{"notes.userfile", false},
		{"notes", true},
		{"", false},
		{"notes..user_file", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if err := CheckOverridePath(tt.path); (err == nil) != tt.ok {
				t.Fatalf("CheckOverridePath(%q) = %v, want ok=%v", tt.path, err, tt.ok)
			}
		})
	}
}

// TestExperimentArmsResolve resolves every arm and compatibility agent of the
// shipped experiment files against the shipped agent configs, so a renamed
// agent field or a typo in a `set:` path fails here, not at plan time.
func TestExperimentArmsResolve(t *testing.T) {
	root := ".."
	files, err := filepath.Glob(filepath.Join(root, "experiments", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no experiment files: %v", err)
	}
	for _, f := range files {
		e := loadExperiment(t, f)
		for id, arm := range e.Arms {
			t.Run(filepath.Base(f)+"/"+id, func(t *testing.T) {
				cfgPath := arm.Agent
				if cfgPath == "" {
					cfgPath = e.Base
				}
				raw, err := os.ReadFile(filepath.Join(root, cfgPath))
				if err != nil {
					t.Fatal(err)
				}
				cfg, err := ResolveAgentConfig(raw, arm.Set)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Notes.UserFile != "" {
					if _, err := os.Stat(filepath.Join(root, cfg.Notes.UserFile)); err != nil || cfg.Notes.UserPath == "" {
						t.Fatalf("user file %q (path %q): %v", cfg.Notes.UserFile, cfg.Notes.UserPath, err)
					}
				}
			})
		}
		if e.Compatibility != nil {
			raw, err := os.ReadFile(filepath.Join(root, e.Compatibility.Agent))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveAgentConfig(raw, nil); err != nil {
				t.Fatalf("%s compatibility agent: %v", f, err)
			}
		}
	}
}
