package suite

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestApplyLintWaivers(t *testing.T) {
	task := &schemas.Task{Dir: "/b/s/tasks/x", Instruction: "instruction.md",
		Provenance: &schemas.TaskProvenance{ImportedFrom: "lhtb"},
		LintWaivers: []schemas.LintWaiver{
			{Rule: schemas.LintRuleInstructionTerm, Match: "grader", Reason: "verbatim"},
			{Rule: schemas.LintRuleHighEntropy, Path: "solution/a.py", Line: 3, Match: "min_key=****", Reason: "identifier"},
			{Rule: "anything", Match: "x", Reason: "no"},
		}}
	in := []Problem{
		{Path: "tasks/x/instruction.md", Severity: SeverityError, Message: `mentions "grader"; the instruction must not reveal grading or notes files`},
		{Path: "tasks/x/instruction.md", Severity: SeverityError, Message: `mentions "hidden test"; the instruction must not reveal grading or notes files`},
		{Path: "tasks/x/solution/a.py", Severity: SeverityError, Message: "line 3: possible high-entropy MIN_KEY value (min_key=****); remove it"},
		{Path: "tasks/x/solution/a.py", Severity: SeverityError, Message: "line 4: possible high-entropy MIN_KEY value (min_key=****); remove it"},
		{Path: "tasks/x/solution/a.py", Severity: SeverityError, Message: "line 3: possible OpenAI API key (sk-p****); remove it"},
	}
	out := ApplyLintWaivers("/b/s", task, in)
	want := []string{SeverityWarning, SeverityError, SeverityWarning, SeverityError, SeverityError}
	for i, p := range out {
		if p.Severity != want[i] {
			t.Errorf("%d %q: severity %s, want %s", i, p.Message, p.Severity, want[i])
		}
	}
	task.Provenance = nil
	if out := ApplyLintWaivers("/b/s", task, in); Errors(out) != len(in) {
		t.Errorf("waived on a task that was not imported: %+v", out)
	}
}
