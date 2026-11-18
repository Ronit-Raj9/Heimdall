package tau2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/suite"
)

// TestImportLLMUser checks -set user=llm (W3): the suite pins an llm
// simulator with upstream's prompt and placeholders the planner refuses, the
// service is marked gateway: {} and carries simuser.py and the prompts, and
// the default import is unchanged.
func TestImportLLMUser(t *testing.T) {
	res, root := runImport(t, map[string]string{SetUser: UserLLM, SetDomains: "airline"}, nil, 1)
	if len(res.Tasks) != 1 {
		t.Fatalf("tasks %v", res.Tasks)
	}
	s, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "tau2-fixture", res.Tasks[0])
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	sim := s.Simulator
	if s.Track != schemas.TrackSupport || sim == nil || sim.Mode != schemas.SimulatorLLM || sim.PromptVersion != simuser.PromptTau2UserV1 ||
		sim.Provider != simuser.Placeholder || sim.MaxTurns != 30 {
		t.Fatalf("suite track %s simulator %+v", s.Track, sim)
	}
	if err := simuser.Check(simuser.Effective(s, nil)); err == nil || !strings.Contains(err.Error(), simuser.Placeholder) {
		t.Fatalf("unpinned simulator must be refused, got %v", err)
	}
	if g := task.Services[0].Gateway; g == nil || *g != (schemas.ServiceGateway{}) {
		t.Fatalf("service gateway %+v", task.Services[0].Gateway)
	}
	for _, f := range []string{"service/simuser.py", "service/prompts/tau2-user-v1.txt"} {
		if _, err := os.Stat(filepath.Join(task.Dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	df, err := os.ReadFile(filepath.Join(task.Dir, "service/Dockerfile"))
	if err != nil || !strings.Contains(string(df), "COPY simuser.py /opt/tau2svc/simuser.py") {
		t.Fatalf("Dockerfile does not copy simuser.py: %v", err)
	}

	res, root = runImport(t, map[string]string{SetDomains: "airline"}, nil, 1)
	s, task, err = suite.LoadTask(filepath.Join(root, "benchmarks"), "tau2-fixture", res.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Simulator != nil || s.Track != schemas.TrackToolUse || task.Services[0].Gateway != nil {
		t.Fatalf("scripted import changed: track %s simulator %+v gateway %+v", s.Track, s.Simulator, task.Services[0].Gateway)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, "service/simuser.py")); err == nil {
		t.Fatal("scripted import carries simuser.py")
	}
}
