package balrog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// fakeDocker: no image present, a build that succeeds, the fixture catalog for run.
type fakeDocker struct {
	catalog []byte
	calls   [][]string
}

func (f *fakeDocker) Run(_ context.Context, args ...string) (*utils.DockerResult, error) {
	f.calls = append(f.calls, args)
	return &utils.DockerResult{ExitCode: 1}, nil
}

func (f *fakeDocker) MustRun(_ context.Context, args ...string) (*utils.DockerResult, error) {
	f.calls = append(f.calls, args)
	if args[0] == "run" {
		return &utils.DockerResult{Stdout: f.catalog}, nil
	}
	return &utils.DockerResult{}, nil
}

func TestImportFixture(t *testing.T) {
	cat, err := os.ReadFile("testdata/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	fd := &fakeDocker{catalog: cat}
	im := &importer{docker: fd, now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	root := t.TempDir()
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/upstream", SuiteID: "balrog"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"balrog-babyai-mixedtrainlocal-v0-goto-s0", "balrog-nle-nethackchallenge-v0-s0", "balrog-babaisai-goto-win-s1"}
	if strings.Join(res.Tasks, ",") != strings.Join(want, ",") {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	var s schemas.Suite
	b, err := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.TaskIDs) != 3 || s.Track != schemas.TrackMCP {
		t.Fatalf("suite tasks %v track %q", s.TaskIDs, s.Track)
	}
	ready := filepath.Join(res.SuiteDir, "tasks", want[0])
	ref, _ := os.ReadFile(filepath.Join(ready, "controls", "reference.sh"))
	if !strings.Contains(string(ref), `"actions":["turn left","go forward"]`) {
		t.Fatalf("reference lacks the plan:\n%s", ref)
	}
	if _, err := os.Stat(filepath.Join(ready, "controls", "mutant1.sh")); err != nil {
		t.Fatalf("mutant missing: %v", err)
	}
	ty, _ := os.ReadFile(filepath.Join(ready, "task.yaml"))
	for _, s := range []string{`BALROG_SEED: "0"`, "tags: [balrog, babyai]", "mutants: [controls/mutant1.sh]", "bench/balrog:0.1.0-"} {
		if !strings.Contains(string(ty), s) {
			t.Errorf("task.yaml lacks %q", s)
		}
	}
	ins, _ := os.ReadFile(filepath.Join(ready, "instruction.md"))
	if !strings.Contains(string(ins), "\nYou are an agent playing") {
		t.Errorf("instruction not dedented:\n%s", ins)
	}
	for _, id := range want[1:] {
		r, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", id, "controls", "reference.sh"))
		if !strings.Contains(string(r), "exit 1") {
			t.Errorf("%s: draft reference does not refuse:\n%s", id, r)
		}
	}
	draft, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", want[2], "task.yaml"))
	if !strings.Contains(string(draft), "# Draft: the environment does not build") {
		t.Errorf("draft reason missing:\n%s", draft)
	}
}

func TestSettings(t *testing.T) {
	if _, err := parseSettings(map[string]string{"envs": "babyai,doom"}); err == nil {
		t.Fatal("unknown env accepted")
	}
	if _, err := parseSettings(map[string]string{"nope": "1"}); err == nil {
		t.Fatal("unknown setting accepted")
	}
	s, err := parseSettings(map[string]string{"envs": "babyai, textworld"})
	if err != nil || len(s.envs) != 2 {
		t.Fatalf("%v %v", s, err)
	}
}
