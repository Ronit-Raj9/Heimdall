package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// exampleSuites lists every suite folder of the example projects
// (examples/<project>/benchmarks/<suite>) whose suite.yaml declares a
// scenario track; the repository-task examples are covered by their own
// recorded runs.
func exampleSuites(t *testing.T) map[string]string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(benchDir, "examples", "*", "benchmarks", "*", "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range dirs {
		if tmpl := DetectTemplate([]byte(readFile(t, p))); IsScenarioTemplate(tmpl) {
			out[filepath.Dir(p)] = tmpl
		}
	}
	return out
}

// TestExampleScenarioSuites loads every task of the scenario examples and
// checks that task validate finds no error in it.
func TestExampleScenarioSuites(t *testing.T) {
	suites := exampleSuites(t)
	if len(suites) == 0 {
		t.Skip("no scenario example suites")
	}
	for dir := range suites {
		t.Run(filepath.Base(filepath.Dir(filepath.Dir(dir))), func(t *testing.T) {
			s, err := suite.Builtin(filepath.Dir(dir))[0].Load(context.Background(), filepath.Base(dir))
			if err != nil {
				t.Fatalf("load %s: %v", dir, err)
			}
			if len(s.Tasks) == 0 {
				t.Fatal("no tasks")
			}
			for _, p := range suite.Lint(s) {
				if p.Severity == suite.SeverityError {
					t.Errorf("lint %s: %s", p.Path, p.Message)
				}
			}
			for _, task := range s.Tasks {
				for _, p := range suite.ValidateTask(context.Background(), s, task) {
					if p.Severity == suite.SeverityError {
						t.Errorf("%s: %s: %s", task.ID, p.Path, p.Message)
					}
				}
			}
		})
	}
}

// TestExampleStateControls runs every control script of the state-graded
// example tasks on the host against the example's own tool server: the
// reference must reach reward 1, the empty control, each mutant and the
// exploit reward 0. It is the free proof that each task's verifier and
// reference agree, before any container runs.
func TestExampleStateControls(t *testing.T) {
	if _, err := lookPython(); err != nil {
		t.Skip("python3 not installed")
	}
	ran := 0
	for dir := range exampleSuites(t) {
		var list struct {
			Tasks []string `yaml:"tasks"`
		}
		if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, "suite.yaml"))), &list); err != nil {
			t.Fatal(err)
		}
		for _, id := range list.Tasks {
			// Tasks load one by one, so a probe the suite loader refuses
			// does not hide the controls.
			s, task, err := suite.LoadTask(filepath.Dir(dir), filepath.Base(dir), id)
			if err != nil {
				t.Fatalf("load %s/%s: %v", dir, id, err)
			}
			if !strings.HasSuffix(task.Controls.Reference, ".sh") || len(task.Services) == 0 {
				continue
			}
			ran++
			t.Run(s.ID+"/"+task.ID, func(t *testing.T) {
				runExampleControls(t, s, task)
			})
		}
	}
	if ran == 0 {
		t.Skip("no state-graded example tasks")
	}
}

func runExampleControls(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	if len(task.Services) > 1 { // Safety probes area (S1): acme-safety/kb-answer runs two MCP servers; Docker admission covers it
		t.Skipf("task %s starts %d services; the host harness runs one", task.ID, len(task.Services))
	}
	sv := task.Services[0]
	server := filepath.Join(s.Root, "services", sv.ID, "server.py")
	if _, err := os.Stat(server); err != nil {
		t.Skipf("service %s has no services/%s/server.py to run on the host", sv.ID, sv.ID)
	}
	seed := ""
	for _, src := range sv.Files {
		seed = filepath.Join(task.Dir, filepath.FromSlash(src))
	}
	want := map[string]float64{task.Controls.Reference: 1, task.Controls.Empty: 0, task.Controls.Exploit: 0}
	for _, m := range task.Controls.Mutants {
		want[m] = 0
	}
	for script, reward := range want {
		got := RunStateControl(t, StateControl{
			Server: server, Service: sv.ID, Seed: seed,
			Tests:  filepath.Join(task.Dir, filepath.FromSlash(task.Verifier.Tests)),
			Script: filepath.Join(task.Dir, filepath.FromSlash(script)), MCP: sv.Role == schemas.ServiceRoleMCP,
		})
		if got != reward {
			t.Errorf("%s: reward %v, want %v", script, got, reward)
		}
	}
}
