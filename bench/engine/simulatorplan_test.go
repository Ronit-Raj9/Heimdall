package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// simSuite is a support suite of two tasks with a simulated customer; t2's
// user file has no persona.
func simSuite(t *testing.T, sim *schemas.SimulatorSpec) *schemas.Suite {
	t.Helper()
	s := &schemas.Suite{ID: "s", Root: t.TempDir(), Simulator: sim, TaskIDs: []string{"t1", "t2", "plain"}}
	for id, user := range map[string]string{
		"t1": `{"script": {"opening": "hi"}, "persona": {"goal": "refund"}}`,
		"t2": `{"script": {"opening": "hi"}}`,
	} {
		dir := filepath.Join(s.Root, "tasks", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "user.json"), []byte(user), 0o644); err != nil {
			t.Fatal(err)
		}
		s.Tasks = append(s.Tasks, &schemas.Task{ID: id, Dir: dir, Services: []schemas.ServiceSpec{{ID: "customer", Role: schemas.ServiceRoleMCP,
			Gateway: &schemas.ServiceGateway{}, Files: map[string]string{"/sim/user.json": "user.json"}}}})
	}
	s.Tasks = append(s.Tasks, &schemas.Task{ID: "plain", Dir: s.Root})
	return s
}

func TestPlanSimulator(t *testing.T) {
	all := []condPlan{{id: "clean", tasks: []string{"t1", "t2"}}}
	llm := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim"}

	m := &schemas.Manifest{}
	if err := planSimulator(&schemas.Experiment{}, simSuite(t, &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}), all, m, schemas.NoopLogger{}); err != nil {
		t.Fatalf("scripted: %v", err)
	}
	if len(m.Scenario[ScenarioSimulator]) != 64 || m.Simulator == nil || m.Simulator.Mode != schemas.SimulatorScripted || m.Simulator.MaxTurns != 12 {
		t.Fatalf("manifest %+v %+v", m.Scenario, m.Simulator)
	}
	scripted := m.Scenario[ScenarioSimulator]

	err := planSimulator(&schemas.Experiment{Simulator: llm}, simSuite(t, nil), all, &schemas.Manifest{}, schemas.NoopLogger{})
	if err == nil || !strings.Contains(err.Error(), "t2") && !strings.Contains(err.Error(), "persona") {
		t.Fatalf("llm with a task without persona: %v", err)
	}
	m = &schemas.Manifest{}
	if err := planSimulator(&schemas.Experiment{Simulator: llm}, simSuite(t, nil), []condPlan{{id: "clean", tasks: []string{"t1"}}}, m, schemas.NoopLogger{}); err != nil {
		t.Fatalf("llm on t1: %v", err)
	}
	if m.Scenario[ScenarioSimulator] == scripted || m.Simulator.Model != "claude-sim" {
		t.Fatalf("llm manifest %+v", m.Simulator)
	}
	pinMe := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "pin-me", Model: "pin-me"}
	if err := planSimulator(&schemas.Experiment{}, simSuite(t, pinMe), all, &schemas.Manifest{}, schemas.NoopLogger{}); err == nil {
		t.Fatal("pin-me must be refused")
	}
	if err := planSimulator(&schemas.Experiment{Simulator: llm}, simSuite(t, nil), []condPlan{{id: "clean", tasks: []string{"plain"}}}, &schemas.Manifest{}, schemas.NoopLogger{}); err == nil {
		t.Fatal("a simulator for tasks without a simulated user must be refused")
	}
	m = &schemas.Manifest{}
	if err := planSimulator(&schemas.Experiment{}, simSuite(t, nil), all, m, schemas.NoopLogger{}); err != nil || m.Simulator != nil || m.Scenario != nil {
		t.Fatalf("no simulator: %v %+v", err, m)
	}
}
