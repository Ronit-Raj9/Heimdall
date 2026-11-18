package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLintSimulator(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("both.json", `{"script": {"opening": "hi"}, "persona": {"goal": "refund"}}`)
	write("script.json", `{"script": {"opening": "hi"}}`)
	write("persona.json", `{"persona": {"goal": "refund"}}`)
	task := func(file string) *schemas.Task {
		return &schemas.Task{ID: "t", Dir: dir, Services: []schemas.ServiceSpec{{ID: "customer", Role: schemas.ServiceRoleMCP,
			Gateway: &schemas.ServiceGateway{}, Files: map[string]string{"/sim/user.json": file}}}}
	}
	sev := func(ps []Problem) string {
		var s []string
		for _, p := range ps {
			s = append(s, p.Severity+": "+p.Message)
		}
		return strings.Join(s, "; ")
	}
	if got := lintSimulatorTask(task("both.json"), "task.yaml"); len(got) != 0 {
		t.Errorf("both: %s", sev(got))
	}
	if got := sev(lintSimulatorTask(task("script.json"), "task.yaml")); !strings.HasPrefix(got, SeverityWarning) || !strings.Contains(got, "persona") {
		t.Errorf("script only: %s", got)
	}
	if got := sev(lintSimulatorTask(task("persona.json"), "task.yaml")); !strings.HasPrefix(got, SeverityError) {
		t.Errorf("persona only: %s", got)
	}
	if got := sev(lintSimulatorTask(task("missing.json"), "task.yaml")); !strings.HasPrefix(got, SeverityError) {
		t.Errorf("missing: %s", got)
	}

	s := &schemas.Suite{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "pin-me", Model: "pin-me"}, Tasks: []*schemas.Task{task("both.json")}}
	if got := sev(lintSimulatorSuite(s)); !strings.HasPrefix(got, SeverityWarning) {
		t.Errorf("pin-me: %s", got)
	}
	s.Simulator = &schemas.SimulatorSpec{Mode: "human"}
	if got := sev(lintSimulatorSuite(s)); !strings.HasPrefix(got, SeverityError) {
		t.Errorf("bad mode: %s", got)
	}
	s.Simulator, s.Tasks = &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}, []*schemas.Task{{ID: "plain"}}
	if got := sev(lintSimulatorSuite(s)); !strings.Contains(got, "no service plays the user") {
		t.Errorf("no simulated service: %s", got)
	}
}
