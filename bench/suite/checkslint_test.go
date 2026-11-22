package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLintChecks(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "tasks", "t")
	if err := os.MkdirAll(filepath.Join(taskDir, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(taskDir, "verifier", "checks.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task := &schemas.Task{ID: "t", Dir: taskDir, Verifier: schemas.TaskVerifier{Tests: "verifier"},
		Controls: schemas.TaskControls{Reference: "solution/reference.sh", Empty: "controls/empty.sh"},
		Services: []schemas.ServiceSpec{{ID: "helpdesk", Role: schemas.ServiceRoleMCP, Export: []schemas.StateExport{{ID: "db.json", Path: "/state/db.json"}}}}}
	s := &schemas.Suite{ID: "s", Root: root, Grader: ChecksGrader, Verifier: schemas.SuiteVerifier{Command: []string{"true"}}}
	msgs := func() string {
		var out []string
		for _, p := range lintChecksTask(s, task) {
			out = append(out, p.Severity+": "+p.Message)
		}
		return strings.Join(out, "\n")
	}

	// No checks.yaml and nothing else grades the task.
	if got := msgs(); !strings.Contains(got, "error: verifier: the suite grades with checks") {
		t.Fatalf("no file: %s", got)
	}
	write("checks:\n  - id: ok\n    tool_called: {tool: issue_refund}\n  - id: st\n    state: {file: helpdesk/db.json, path: refunds, length: 1}\n")
	if got := msgs(); got != "" {
		t.Fatalf("good file: %s", got)
	}
	write("checks:\n  - id: st\n    state: {file: billing/db.json, path: x, equals: 1}\n  - id: r\n    reply_rubric: be kind\n")
	got := msgs()
	for _, want := range []string{"state file billing/db.json: no service of this task exports it (exports: helpdesk/db.json)", "grading: judged"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	s.Grading, s.Judge = schemas.GradingJudged, &schemas.JudgeSpec{Provider: "openai", Model: "m", PromptVersion: "qa-v1"}
	if got := msgs(); strings.Contains(got, "judged") {
		t.Errorf("judged suite still refused: %s", got)
	}
	write("checks:\n  - id: Bad\n")
	if got := msgs(); !strings.Contains(got, "error: checks[0] (Bad)") {
		t.Errorf("parse errors: %s", got)
	}
	s.Grader = "verifier_command"
	write("checks:\n  - id: ok\n    tool_called: {tool: issue_refund}\n")
	if got := msgs(); !strings.Contains(got, "warning: checks.yaml is read only by grader checks") {
		t.Errorf("other grader: %s", got)
	}
}

func TestReferenceMissedChecks(t *testing.T) {
	g := &schemas.Grade{Passed: true, Dimensions: map[string]float64{"check.a": 1, "check.b": 0, "checks_score": 0.9}}
	if why := referenceMissedChecks(ChecksGrader, "reference_1", g); !strings.Contains(why, "fails check b") {
		t.Fatalf("why %q", why)
	}
	if why := referenceMissedChecks(ChecksGrader, "mutant_1", g); why != "" {
		t.Fatalf("mutant: %q", why)
	}
	if why := referenceMissedChecks("verifier_command", "reference_1", g); why != "" {
		t.Fatalf("other grader: %q", why)
	}
}
