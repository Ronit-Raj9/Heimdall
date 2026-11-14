package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Config hashes of the shipped agents/codex.yaml and agents/custom-python.yaml
// as the planner resolved them before per-suite images existed. A config
// without build.base_arg must keep hashing the same, or every existing
// experiment would stop comparing with new ones.
const (
	pinnedCodexHash        = "42116f1c0e89811700a4bb213281b1b219b9b78242a428a8eca35fac82946586"
	pinnedCustomPythonHash = "b2d0bb59d32cb657269f4652e809d3e9ba588cac5c51b9dd1fad9311dea846c4"
)

// inspectingEnv is the fake environment plus engine.ImageInspector.
type inspectingEnv struct {
	fakeEnv
	ids map[string]string
}

func (e inspectingEnv) ImageID(_ context.Context, ref string) (string, error) {
	if id, ok := e.ids[ref]; ok {
		return id, nil
	}
	return "", errors.New("no such image")
}

// copyAgent copies a shipped agent config into the bench root.
func copyAgent(t *testing.T, b *bench, id string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "agents", id+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("agents", id+".yaml")
	if err := os.WriteFile(filepath.Join(b.root, rel), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestShippedAgentConfigHashesUnchanged(t *testing.T) {
	b := newBench(t)
	b.reg.Parsers = append(b.reg.Parsers, fakeParser{part{"codex"}})
	b.reg.Environments = []schemas.Environment{inspectingEnv{fakeEnv{part{"docker_workspace"}, b.w},
		map[string]string{"bench/agent-codex:1": "sha256:aaa", "bench/agent-custom-python:1": "sha256:bbb"}}}
	for id, want := range map[string]string{"codex": pinnedCodexHash, "custom-python": pinnedCustomPythonHash} {
		t.Run(id, func(t *testing.T) {
			m, err := NewPlanner(b.reg, b.root, nil).PlanCheck(context.Background(), CheckSpec{Agent: copyAgent(t, b, id), Suite: "coding-core", Task: "t01"})
			if err != nil {
				t.Fatal(err)
			}
			am := m.Arms[CheckArm]
			if am.ConfigHash != want {
				t.Errorf("config hash %s, want %s (pinned before per-suite images)", am.ConfigHash, want)
			}
			if am.Config.Image != "bench/agent-"+id+":1" || am.ImageID == "" {
				t.Errorf("image %s id %q", am.Config.Image, am.ImageID)
			}
		})
	}
	e := testExperiment()
	e.Base = ""
	e.Arms = map[string]schemas.ArmSpec{"A": {Label: "codex", Agent: copyAgent(t, b, "codex")}, "B": {Label: "custom", Agent: copyAgent(t, b, "custom-python")}}
	m := b.plan(t, e)
	if m.Arms["A"].ConfigHash != pinnedCodexHash || m.Arms["B"].ConfigHash != pinnedCustomPythonHash {
		t.Errorf("experiment hashes A %s B %s", m.Arms["A"].ConfigHash, m.Arms["B"].ConfigHash)
	}
}

const perSuiteAgentYAML = `id: mine
adapter: command
image: acme/agent-mine:3
build: {dockerfile: agents/images/mine/Dockerfile, tag: acme/agent-mine:3, base_arg: BASE_IMAGE}
argv: [bench-agent]
env: {BENCH_API_KEY: "{{run.key}}", BENCH_SESSION_ID: "{{run.session}}"}
model: claude-test
notes: {project_file: AGENTS.md}
budgets: {wall_s: 900, cpus: 1, mem_gb: 2}
gateway: {provider: anthropic, max_retries: 0}
`

func TestPlanPerSuiteImage(t *testing.T) {
	b := newBench(t)
	b.suite.BaseImage = schemas.ImageSpec{Tag: "bench/coding-core-base:1"}
	if err := os.WriteFile(filepath.Join(b.root, "agents", "mine.yaml"), []byte(perSuiteAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("tag per suite, hashed, image id recorded", func(t *testing.T) {
		b.reg.Environments = []schemas.Environment{inspectingEnv{fakeEnv{part{"docker_workspace"}, b.w},
			map[string]string{"acme/agent-mine:3-coding-core": "sha256:ccc"}}}
		m, err := NewPlanner(b.reg, b.root, nil).PlanCheck(context.Background(), CheckSpec{Agent: "agents/mine.yaml", Suite: "coding-core"})
		if err != nil {
			t.Fatal(err)
		}
		am := m.Arms[CheckArm]
		if am.Config.Image != "acme/agent-mine:3-coding-core" || am.Config.Build.Tag != "acme/agent-mine:3" || am.ImageID != "sha256:ccc" {
			t.Fatalf("arm = image %s build %+v id %s", am.Config.Image, am.Config.Build, am.ImageID)
		}
		if h, _ := utils.HashJSON(&am.Config); h != am.ConfigHash {
			t.Fatal("config hash is not the hash of the resolved config")
		}
	})
	t.Run("missing image plans without an id", func(t *testing.T) {
		b.reg.Environments = []schemas.Environment{inspectingEnv{fakeEnv{part{"docker_workspace"}, b.w}, nil}}
		m, err := NewPlanner(b.reg, b.root, nil).PlanCheck(context.Background(), CheckSpec{Agent: "agents/mine.yaml", Suite: "coding-core"})
		if err != nil || m.Arms[CheckArm].ImageID != "" {
			t.Fatalf("err %v id %q", err, m.Arms[CheckArm].ImageID)
		}
	})
	t.Run("suite without a base image refuses the plan", func(t *testing.T) {
		b.suite.BaseImage = schemas.ImageSpec{}
		_, err := NewPlanner(b.reg, b.root, nil).PlanCheck(context.Background(), CheckSpec{Agent: "agents/mine.yaml", Suite: "coding-core"})
		if err == nil || !strings.Contains(err.Error(), "no base image") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPlanCheck(t *testing.T) {
	b := newBench(t)
	b.suite.Lock.Tasks["t01"] = schemas.TaskLock{Version: "1.0.0", Status: schemas.TaskDraft}
	checker := &fakeChecker{err: errors.New("must not be called")}
	p := NewPlanner(b.reg, b.root, nil, WithUpstreamChecker(checker))
	m, err := p.PlanCheck(context.Background(), CheckSpec{Agent: "agents/claude-code.yaml", Suite: "coding-core"})
	if err != nil {
		t.Fatalf("a check on a draft task with a broken upstream checker must plan: %v", err)
	}
	if len(m.Arms) != 1 || len(m.Slots) != 1 || checker.calls != 0 {
		t.Fatalf("arms %d slots %d checker calls %d", len(m.Arms), len(m.Slots), checker.calls)
	}
	s := m.Slots[0]
	if s.Arm != CheckArm || s.Task != "t01" || s.Condition != schemas.ConditionClean || s.Trial != 1 || !s.Counted {
		t.Fatalf("slot = %+v", s)
	}
	if !strings.HasPrefix(m.ExpID, "e_check_claude_code_") || m.Hash == "" {
		t.Fatalf("exp id %s hash %q", m.ExpID, m.Hash)
	}
	if m, err := p.PlanCheck(context.Background(), CheckSpec{Agent: "agents/claude-code.yaml", Suite: "coding-core", Task: "t03"}); err != nil || m.Slots[0].Task != "t03" {
		t.Fatalf("named task: %v", err)
	}
	for _, spec := range []CheckSpec{{Suite: "coding-core"}, {Agent: "agents/claude-code.yaml"}, {Agent: "agents/claude-code.yaml", Suite: "coding-core", Task: "t99"}} {
		if _, err := p.PlanCheck(context.Background(), spec); err == nil {
			t.Errorf("spec %+v planned", spec)
		}
	}
	// An experiment still needs two arms.
	e := testExperiment()
	delete(e.Arms, "B")
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err == nil || !strings.Contains(err.Error(), "need at least 2 arms") {
		t.Fatalf("one-arm experiment: %v", err)
	}
}

func TestCheckName(t *testing.T) {
	for in, want := range map[string]string{"agents/mine.yaml": "mine", `C:\x\y.yml`: "y", "plain": "plain", "": "agent"} {
		if got := checkName(in); got != want {
			t.Errorf("checkName(%q) = %q, want %q", in, got, want)
		}
	}
}
