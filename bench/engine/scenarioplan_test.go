package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// imageEnv is a scenario env that also builds task images.
type imageEnv struct {
	fakeScenarioEnv
	fail bool
}

func (e imageEnv) EnsureTaskImage(_ context.Context, s *schemas.Suite, t *schemas.Task) (string, string, error) {
	if e.fail {
		return "", "", errors.New("docker build failed")
	}
	return "bench/task-" + s.ID + "-" + t.ID + ":abc", "sha256:img-" + t.ID, nil
}

func TestPlanScenarioChecks(t *testing.T) {
	tests := []struct {
		name    string
		plain   bool // environment without scenario interfaces
		edit    func(s *schemas.Suite, task *schemas.Task)
		agent   string // agent yaml written over agents/claude-code.yaml
		wantErr string
	}{
		{"services need a service environment", true, func(_ *schemas.Suite, task *schemas.Task) {
			task.Services = []schemas.ServiceSpec{{ID: "db", Image: "x"}}
		}, "", "task t01 needs services"},
		{"sessions need a session environment", true, func(_ *schemas.Suite, task *schemas.Task) {
			task.Sessions = []schemas.SessionSpec{{ID: "a", Instruction: "a.md"}}
		}, "", "needs sessions"},
		{"snapshot needs a snapshot environment", true, func(s *schemas.Suite, _ *schemas.Task) {
			s.Verifier.Mode = schemas.VerifierSnapshot
		}, "", "needs snapshot grading"},
		{"answer needs an answer environment", true, func(_ *schemas.Suite, task *schemas.Task) {
			task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
		}, "", "needs an answer export"},
		{"artifact needs an artifact environment", true, func(_ *schemas.Suite, task *schemas.Task) {
			task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
		}, "", "needs an artifact export"},
		{"judged suite needs a judge", false, func(s *schemas.Suite, _ *schemas.Task) {
			s.Grading = schemas.GradingJudged
		}, "", "is judged but names no judge"},
		{"remote agent only answers", false, func(*schemas.Suite, *schemas.Task) {}, "remote", "run a remote agent, which can only answer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBench(t)
			if !tc.plain {
				b.reg.Environments = []schemas.Environment{fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}}
			}
			if tc.agent == "remote" {
				b.reg.Adapters = append(b.reg.Adapters, fakeAdapter{part{"remote_http"}, b.w})
				raw, err := os.ReadFile(filepath.Join(b.root, "agents/claude-code.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				out := strings.Replace(string(raw), "adapter: command", "adapter: remote_http", 1)
				if out == string(raw) {
					t.Fatalf("agent yaml has no adapter line: %s", raw)
				}
				if err := os.WriteFile(filepath.Join(b.root, "agents/claude-code.yaml"), []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			tc.edit(b.suite, b.suite.TaskByID("t01"))
			_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPlanTaskImagesAndScenarioHashes(t *testing.T) {
	b := newBench(t)
	b.reg.Environments = []schemas.Environment{imageEnv{fakeScenarioEnv: fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}}}
	b.suite.TaskByID("t01").Environment = &schemas.TaskEnvironment{Dockerfile: "env/Dockerfile"}
	b.suite.Services = []schemas.ServiceSpec{{ID: "cache", Image: "redis:7"}}
	b.suite.Verifier.Mode = schemas.VerifierSnapshot
	m := b.plan(t, testExperiment())
	if m.TaskImages["t01"] != "sha256:img-t01" || len(m.TaskImages) != 1 {
		t.Fatalf("task images %v", m.TaskImages)
	}
	if m.Scenario["suite.services"] == "" || m.Scenario["suite.verifier.mode"] == "" || len(m.Scenario) != 2 {
		t.Fatalf("scenario hashes %v", m.Scenario)
	}
	h, err := ManifestHash(m)
	if err != nil || h != m.Hash {
		t.Fatalf("hash %s vs %s (%v)", h, m.Hash, err)
	}

	b.reg.Environments = []schemas.Environment{imageEnv{fakeScenarioEnv: fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}, fail: true}}
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment()); err == nil || !strings.Contains(err.Error(), "task t01 image") {
		t.Fatalf("failed image build: got %v", err)
	}
}

// TestRecordedHashesUnchanged recomputes the hash of every recorded manifest
// and re-encodes every recorded run file the engine writes from schemas
// types: adding scenario fields must not move a hash or change a byte.
func TestRecordedHashesUnchanged(t *testing.T) {
	root := filepath.Join("..", "testdata", "recorded")
	manifests, err := filepath.Glob(filepath.Join(root, "*", "manifest.json"))
	if err != nil || len(manifests) == 0 {
		t.Skipf("no recorded manifests: %v", err)
	}
	for _, path := range manifests {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m schemas.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got, err := ManifestHash(&m)
		if err != nil {
			t.Fatal(err)
		}
		if got != m.Hash {
			t.Errorf("%s: hash %s, recorded %s", path, got, m.Hash)
		}
	}
	files := map[string]func() any{
		"evidence.json": func() any { return &schemas.Evidence{} },
		"grade.json":    func() any { return &schemas.Grade{} },
		"run.json":      func() any { return &schemas.Run{} },
		"plan.json":     func() any { return &schemas.RunPlan{} },
	}
	checked := 0
	for name, mk := range files {
		paths, err := filepath.Glob(filepath.Join(root, "*", "slots", "*", "runs", "*", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			v := mk()
			if err := json.Unmarshal(raw, v); err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			out, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimRight(raw, "\n"), out) {
				t.Errorf("%s: re-encoding changed the file", p)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Skip("no recorded run files")
	}
}
