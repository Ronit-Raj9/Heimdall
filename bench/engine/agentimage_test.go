package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeAgentBuilder records the agent images the planner builds.
type fakeAgentBuilder struct{ built []AgentImage }

func (f *fakeAgentBuilder) BuildAgentImage(_ context.Context, img AgentImage) (string, error) {
	f.built = append(f.built, img)
	return "sha256:agent-" + img.Tag, nil
}

// idEnv is a scenario env that reports one id for every image.
type idEnv struct {
	fakeScenarioEnv
	id string
}

func (e idEnv) ImageID(context.Context, string) (string, error) { return e.id, nil }

const baseArgAgentYAML = `id: claude-code
adapter: command
image: bench/agent:1
build: {dockerfile: agents/Dockerfile, tag: "bench/agent:1", base_arg: BASE}
argv: [agent]
env: {}
model: claude-test
notes:
  project_file: CLAUDE.md
  user_path: /home/agent/.claude/CLAUDE.md
budgets: {wall_s: 900, cpus: 2, mem_gb: 4}
gateway: {provider: anthropic, max_retries: 0}
`

// taskImageBench is a bench whose task t01 builds its own image and whose
// agent declares build.base_arg.
func taskImageBench(t *testing.T) (*bench, *fakeAgentBuilder) {
	t.Helper()
	b := newBench(t)
	b.suite.BaseImage.Tag = "bench/base:1"
	task := b.suite.TaskByID("t01")
	task.Environment = &schemas.TaskEnvironment{Dockerfile: "env/Dockerfile"}
	for rel, content := range map[string]string{
		filepath.Join(task.Dir, "env", "Dockerfile"):        "FROM python:3.12-slim\n",
		filepath.Join(b.root, "agents", "Dockerfile"):       "ARG BASE\nFROM ${BASE}\n",
		filepath.Join(b.root, "agents", "claude-code.yaml"): baseArgAgentYAML,
		filepath.Join(b.root, "agents", "plain-agent.yaml"): strings.Replace(baseArgAgentYAML, "build: {dockerfile: agents/Dockerfile, tag: \"bench/agent:1\", base_arg: BASE}\n", "", 1),
	} {
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.reg.Environments = []schemas.Environment{imageEnv{fakeScenarioEnv: fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}}}
	return b, &fakeAgentBuilder{}
}

func TestPlanBuildsPerTaskAgentImages(t *testing.T) {
	b, fb := taskImageBench(t)
	e := testExperiment()
	e.Arms["B"] = schemas.ArmSpec{Label: "plain", Agent: "agents/plain-agent.yaml"}
	m, err := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb)).Plan(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	a := m.Arms["A"]
	if a.TaskImages["t01"] != "bench/agent:1-coding-core-t01" || a.TaskImageIDs["t01"] != "sha256:agent-bench/agent:1-coding-core-t01" {
		t.Fatalf("arm A task images %v %v", a.TaskImages, a.TaskImageIDs)
	}
	if len(a.TaskImages) != 1 {
		t.Errorf("a task without its own image got a per-task agent: %v", a.TaskImages)
	}
	if len(fb.built) != 1 || !strings.HasPrefix(fb.built[0].BuildArgs["BASE"], "bench/task-coding-core-t01:") {
		t.Fatalf("built %+v", fb.built)
	}
	if bm := m.Arms["B"]; bm.TaskImages != nil || bm.TaskImageIDs != nil {
		t.Errorf("agent without base_arg got per-task images: %+v", bm)
	}
	if a.Config.Image != "bench/agent:1-coding-core" {
		t.Errorf("arm config image %s, want the per-suite tag", a.Config.Image)
	}
}

// A scripted control stands in the agent's place, so on a task with its own
// image it runs in that image, pinned to the id the planner recorded.
func TestPlanScriptedControlUsesTaskImage(t *testing.T) {
	b, fb := taskImageBench(t)
	e := testExperiment()
	e.Arms["B"] = schemas.ArmSpec{Label: "control", Agent: "agents/toy.yaml"}
	m, err := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb)).Plan(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	bm := m.Arms["B"]
	if bm.Config.Adapter != "scripted" {
		t.Fatalf("arm B adapter %s, want scripted", bm.Config.Adapter)
	}
	tag := bm.TaskImages["t01"]
	if !strings.HasPrefix(tag, "bench/task-coding-core-t01:") {
		t.Fatalf("scripted control image %q, want the task image", tag)
	}
	if len(bm.TaskImages) != 1 {
		t.Errorf("a task without its own image got a per-task image: %v", bm.TaskImages)
	}
	if want := m.TaskImages["t01"]; want == "" || bm.TaskImageIDs["t01"] != want {
		t.Errorf("scripted control image id %q, want the task image id %q", bm.TaskImageIDs["t01"], want)
	}
	if len(fb.built) != 1 {
		t.Errorf("built %d agent images, want 1: a scripted control builds none", len(fb.built))
	}
}

func TestRunSlotUsesPerTaskAgentImage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		envID   string // "" uses an env that cannot inspect images
		want    schemas.Outcome
		created bool
	}{
		{"image used", "", schemas.OutcomePass, true},
		{"frozen image matches", "sha256:agent-bench/agent:1-coding-core-t01", schemas.OutcomePass, true},
		{"rebuilt image refused", "sha256:other", schemas.OutcomeAbortedInfra, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, fb := taskImageBench(t)
			m, err := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb)).Plan(context.Background(), testExperiment())
			if err != nil {
				t.Fatal(err)
			}
			if tc.envID != "" {
				b.reg.Environments = []schemas.Environment{idEnv{fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, &scenarioWorld{}}, tc.envID}}
			}
			var slot *schemas.Slot
			for i := range m.Slots {
				if m.Slots[i].Arm == "A" && m.Slots[i].Task == "t01" {
					slot = &m.Slots[i]
				}
			}
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != tc.want {
				t.Fatalf("outcome %s %+v", res.Run.Outcome, res.Run.Failures)
			}
			if tc.created != (len(b.w.agents) == 1) {
				t.Fatalf("created %d workspaces", len(b.w.agents))
			}
			if tc.created && b.w.agents[0].Image != "bench/agent:1-coding-core-t01" {
				t.Errorf("run started image %s", b.w.agents[0].Image)
			}
		})
	}
}

// An agent without build.base_arg on a task with its own image, in a suite
// graded by snapshot, would be graded in a snapshot of its own image and
// score 0 whatever it did (live e_live_l10_tb2_m7r2): the plan refuses it.
// With base_arg, or with the default clean_room verifier, it plans.
func TestPlanRefusesOwnImageAgentUnderSnapshotVerifier(t *testing.T) {
	b, fb := taskImageBench(t)
	b.suite.Verifier.Mode = schemas.VerifierSnapshot
	e := testExperiment()
	e.Arms["B"] = schemas.ArmSpec{Label: "plain", Agent: "agents/plain-agent.yaml"}
	_, err := NewPlanner(b.reg, b.root, nil, WithAgentImageBuilder(fb)).Plan(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), "arm B, task t01: the task brings its own image") || !strings.Contains(err.Error(), "no build.base_arg") {
		t.Fatalf("err = %v, want arm B refused on t01", err)
	}
	if strings.Contains(err.Error(), "arm A") {
		t.Errorf("arm A has base_arg and must not be refused: %v", err)
	}

	b2, fb2 := taskImageBench(t)
	if _, err := NewPlanner(b2.reg, b2.root, nil, WithAgentImageBuilder(fb2)).Plan(context.Background(), e); err != nil {
		t.Fatalf("clean_room suite: %v", err)
	}
}
