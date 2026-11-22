package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A judged row task whose every wrong answer fails before the judge is
// asked is refused; one plausible wrong answer is enough; a judged task
// that is not an imported row only gets a warning.
func TestLintJudgedWrong(t *testing.T) {
	old := JudgeSkip
	t.Cleanup(func() { JudgeSkip = old })
	JudgeSkip = func(_ *schemas.AnswerSpec, a string) string {
		if a = strings.TrimSpace(a); a == "" || strings.HasPrefix(a, "I don't know") {
			return "refusal"
		}
		return ""
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "verifier", "answer.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &schemas.Suite{Root: filepath.Dir(dir), Grading: schemas.GradingJudged, Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer},
		Judge: &schemas.JudgeSpec{Provider: "openai", Model: "m", PromptVersion: "qa-v1"}}
	task := &schemas.Task{ID: "t", Dir: dir, Verifier: schemas.TaskVerifier{Tests: "verifier"}, Provenance: &schemas.TaskProvenance{CreatedBy: RowImportCreator}}

	spec("match: exact\nexpected: [30 days]\nwrong_answers: [\"I don't know.\"]\n")
	ps := lintJudgedWrong(s, task)
	if len(ps) != 1 || ps[0].Severity != SeverityError || !strings.Contains(ps[0].Message, "reaches the judge") {
		t.Fatalf("problems %+v", ps)
	}
	task.Provenance.CreatedBy = "task init"
	if ps := lintJudgedWrong(s, task); len(ps) != 1 || ps[0].Severity != SeverityWarning {
		t.Fatalf("non-row task: %+v", ps)
	}
	task.Provenance.CreatedBy = RowImportCreator
	spec("match: exact\nexpected: [30 days]\nwrong_answers: [\"I don't know.\"]\ngenerated_wrong_answers: [31 days]\n")
	if ps := lintJudgedWrong(s, task); len(ps) != 0 {
		t.Fatalf("a generated wrong answer reaches the judge: %+v", ps)
	}
	s.Grading = schemas.GradingVerified
	spec("match: exact\nexpected: [30 days]\n")
	if ps := lintJudgedWrong(s, task); len(ps) != 0 {
		t.Fatalf("exact grading needs no judged wrong answer: %+v", ps)
	}
}
