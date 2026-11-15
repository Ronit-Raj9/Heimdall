//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
)

// Images of the end-to-end scenario test. The base image holds python and
// git (the helper steps need git); services run in python:3.12-slim.
const (
	e2eBaseImage    = "bench/coding-core-base:1"
	e2eServiceImage = "python:3.12-slim"
)

// e2eFixture is a hand-written suite (suite/testdata/scenario): one state
// task whose service stores what the agent posts, one answer task with two
// sessions, and the scripted oracle and noop agents. The reference of the
// state task posts {"done": true}; the answer task's reference answer is 42.
const e2eFixture = "../suite/testdata/scenario"

// e2eGateway mints a key that is never used: scripted controls call no model.
type e2eGateway struct{}

func (e2eGateway) GetName() string    { return "bifrost" }
func (e2eGateway) GetVersion() string { return "0.0.0-test" }
func (e2eGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (e2eGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	return &schemas.RunKey{ID: "vk-" + rc.Run.RunID, KeySlot: "slot-1", Provider: "anthropic", Model: "none"}, nil
}
func (e2eGateway) RevokeKey(context.Context, *schemas.RunKey) error { return nil }
func (e2eGateway) RevokeStale(context.Context, map[string]bool) (int, error) {
	return 0, nil
}
func (e2eGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return &schemas.RequestLog{Complete: true}, nil
}

// e2eMetric is the primary metric the planner requires.
type e2eMetric struct{}

func (e2eMetric) GetName() string    { return "verified_success" }
func (e2eMetric) GetVersion() string { return "0.0.0-test" }
func (e2eMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: "verified_success", Kind: schemas.KindRate, HigherIsBetter: true, MinEvidence: schemas.EvidenceOutcome}
}
func (e2eMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if ev.Grade != nil && ev.Grade.Passed {
		return schemas.Observed(1, schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(0, schemas.EvidenceOutcome), nil
}

func requireImages(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	for _, img := range []string{e2eBaseImage, e2eServiceImage} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Skipf("image %s is not present locally", img)
		}
	}
}

// TestScenarioEndToEnd plans a two-arm experiment (scripted oracle against
// noop) on a hand-written suite with a state task (one service) and an
// answer task (two sessions), runs every slot through the real docker
// environment and graders, and checks the outcomes, the sealed scenario
// files and that nothing of a run is left in docker.
func TestScenarioEndToEnd(t *testing.T) {
	requireImages(t)
	root := t.TempDir()
	// Sealed run folders are read-only; make them removable again.
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	err := filepath.WalkDir(e2eFixture, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(e2eFixture, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFSStore(filepath.Join(root, "runs"), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &schemas.Registry{
		Loaders:      suite.Builtin(root),
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{HelperImage: e2eBaseImage}),
		Adapters:     adapters.Builtin(adapters.CommandConfig{BenchRoot: root}),
		Graders:      graders.Builtin(graders.HiddenTestsConfig{}),
		Metrics:      []schemas.Metric{e2eMetric{}},
		Gateway:      e2eGateway{},
		Store:        st,
	}
	s, err := engine.LoadSuite(context.Background(), reg, "hd-run-e2e")
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	exp := &schemas.Experiment{
		Name: "hd-run-e2e", Suite: "hd-run-e2e", Smoke: true, Seed: 1, Concurrency: 1,
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "oracle", Agent: "agents/oracle.yaml"}, "B": {Label: "noop", Agent: "agents/noop.yaml"}},
		Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{"state", "recall"}}, Trials: 1}},
		Primary:    schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	m, err := engine.NewPlanner(reg, root, nil).Plan(ctx, exp)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := engine.NewPipeline(reg, root, nil)
	for i := range m.Slots {
		slot := &m.Slots[i]
		res, err := p.RunSlot(ctx, engine.SlotInput{Manifest: m, Suite: s, Slot: slot, Attempt: 1})
		if err != nil {
			t.Fatalf("slot %s: %v", slot.SlotID, err)
		}
		want := schemas.OutcomeFail
		if slot.Arm == "A" {
			want = schemas.OutcomePass
		}
		if res.Run.Outcome != want || res.Seal == nil {
			t.Errorf("%s/%s: outcome %s (want %s), sealed %v, failures %+v", slot.Task, m.Arms[slot.Arm].Label, res.Run.Outcome, want, res.Seal != nil, res.Run.Failures)
		}
		if err := st.Verify(ctx, m.ExpID, res.Run.RunID); err != nil {
			t.Errorf("%s: seal does not verify: %v", res.Run.RunID, err)
		}
		checkRunFiles(t, slot.Task, res.Dir)
		checkNothingLeft(t, res.Run.RunID)
	}
}

// checkRunFiles checks the sealed scenario files of one run.
func checkRunFiles(t *testing.T, task, dir string) {
	t.Helper()
	var want []string
	switch task {
	case "state":
		want = []string{"services.json", "state/db/state"}
	case "recall":
		want = []string{"sessions.json", "answer.txt", "agent.stream.1.jsonl", "agent.stream.2.jsonl", "agent.stream.jsonl"}
	}
	for _, f := range want {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s: %s missing: %v", task, f, err)
		}
	}
	if task == "recall" {
		b, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
		if err != nil {
			return
		}
		var sessions []schemas.SessionResult
		if err := json.Unmarshal(b, &sessions); err != nil || len(sessions) != 2 {
			t.Errorf("sessions.json: %v %s", err, b)
		}
	}
}

// checkNothingLeft fails when any container, volume or network of the run is
// still in docker.
func checkNothingLeft(t *testing.T, runID string) {
	t.Helper()
	for _, kind := range []string{"container", "volume", "network"} {
		args := []string{kind, "ls", "-q", "--filter", "label=bifrost-bench.run=" + runID}
		if kind == "container" {
			args = []string{"ps", "-aq", "--filter", "label=bifrost-bench.run=" + runID}
		}
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Errorf("docker %s: %v", strings.Join(args, " "), err)
			continue
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("run %s left %s: %s", runID, kind, s)
		}
	}
}

// TestPerTaskAgentImageEndToEnd plans an experiment on a task with its own
// Dockerfile image and a command agent whose build declares base_arg: the
// planner builds the task image, then the agent image on it, and records both
// ids. The agent image holds the task image's files and the agent's own.
func TestPerTaskAgentImageEndToEnd(t *testing.T) {
	requireImages(t)
	root := t.TempDir()
	files := map[string]string{
		"hd-run-img/suite.yaml": `id: hd-run-img
version: 1.0.0
environment: docker_workspace
grader: verifier_command
base_image:
  tag: ` + e2eBaseImage + `
workdir: /workspace
verifier:
  command: ["true"]
  timeout_s: 60
  result: exit_code
tasks: [t1]
probes: []
`,
		"hd-run-img/tasks/t1/task.yaml": `id: t1
version: 1.0.0
title: Task with its own image
project: e2e
kind: feature
instruction: instruction.md
repo_dir: repo
verifier:
  tests: verifier/tests
  required: []
controls:
  reference: controls/reference.sh
  empty: controls/empty.sh
  mutants: []
  exploit: controls/exploit.sh
timeouts:
  agent_s: 60
environment:
  dockerfile: env/Dockerfile
`,
		"hd-run-img/tasks/t1/env/Dockerfile":          "FROM " + e2eServiceImage + "\nRUN echo task > /task-marker\n",
		"hd-run-img/tasks/t1/instruction.md":          "Do it.\n",
		"hd-run-img/tasks/t1/repo/README.md":          "t1\n",
		"hd-run-img/tasks/t1/verifier/tests/check.sh": "true\n",
		"hd-run-img/tasks/t1/controls/reference.sh":   "true\n",
		"hd-run-img/tasks/t1/controls/empty.sh":       "true\n",
		"hd-run-img/tasks/t1/controls/exploit.sh":     "true\n",
		"agents/Dockerfile":                           "ARG BASE\nFROM ${BASE}\nRUN echo agent > /agent-marker\n",
		"agents/a.yaml":                               commandAgentYAML("a"),
		"agents/b.yaml":                               commandAgentYAML("b"),
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := &schemas.Registry{
		Loaders:      suite.Builtin(root),
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{HelperImage: e2eBaseImage}),
		Adapters:     adapters.Builtin(adapters.CommandConfig{BenchRoot: root}),
		Graders:      graders.Builtin(graders.HiddenTestsConfig{}),
		Metrics:      []schemas.Metric{e2eMetric{}},
		Gateway:      e2eGateway{},
	}
	exp := &schemas.Experiment{
		Name: "hd-run-img", Suite: "hd-run-img", Smoke: true, Seed: 1, Concurrency: 1,
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "a", Agent: "agents/a.yaml"}, "B": {Label: "b", Agent: "agents/b.yaml"}},
		Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{"t1"}}, Trials: 1}},
		Primary:    schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	m, err := engine.NewPlanner(reg, root, nil).Plan(ctx, exp)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	am := m.Arms["A"]
	tag := am.TaskImages["t1"]
	t.Cleanup(func() {
		for _, ref := range []string{m.Arms["A"].TaskImages["t1"], m.Arms["B"].TaskImages["t1"]} {
			if ref != "" {
				_ = exec.Command("docker", "image", "rm", ref).Run()
			}
		}
		if id := m.TaskImages["t1"]; id != "" {
			_ = exec.Command("docker", "image", "rm", id).Run()
		}
	})
	if tag != "bench/hd-run-agent-a:1-hd-run-img-t1" || !strings.HasPrefix(am.TaskImageIDs["t1"], "sha256:") || !strings.HasPrefix(m.TaskImages["t1"], "sha256:") {
		t.Fatalf("task images %v ids %v manifest %v", am.TaskImages, am.TaskImageIDs, m.TaskImages)
	}
	out, err := exec.Command("docker", "run", "--rm", "--network", "none", tag, "cat", "/task-marker", "/agent-marker").CombinedOutput()
	if err != nil || string(out) != "task\nagent\n" {
		t.Fatalf("agent image does not hold the task image's files: %v %q", err, out)
	}
}

func commandAgentYAML(id string) string {
	return `id: ` + id + `
adapter: command
image: bench/hd-run-agent-` + id + `:1
build: {dockerfile: agents/Dockerfile, tag: "bench/hd-run-agent-` + id + `:1", base_arg: BASE}
argv: [sh, -c, "true"]
env: {}
model: none
notes:
  project_file: AGENTS.md
budgets: {wall_s: 60, cpus: 1, mem_gb: 1}
gateway: {provider: anthropic, max_retries: 0}
`
}
