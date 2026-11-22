package suite

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// JudgeSkip is the judge guard (graders.JudgeSkipReason, set by the graders
// package): why the judge would not be asked to grade an answer, "" when it
// would. Nil leaves the judged wrong-answer check out (a binary without the
// graders).
var JudgeSkip func(ans *schemas.AnswerSpec, answer string) string

// RowImportCreator is provenance.created_by of a task written from one row
// of the author's data (scaffold.ImportRows). Its readiness check needs a
// wrong answer the judge actually grades (an error); any other judged task
// gets a warning, so library packs imported before generated wrong answers
// keep their admission path.
const RowImportCreator = "web import"

// cheatBenchPrompt is the judge prompt that grades trajectories, not
// answers; its guard does not run.
const cheatBenchPrompt = "cheatbench-v1"

// lintJudgedWrong flags a judged answer task whose readiness check would
// never show the judge a wrong answer (an error for an imported row, a
// warning otherwise): every mutant and every wrong answer
// is empty, a refusal or addresses the grader, so the guard fails it before
// the judge is asked, and a judge that passes everything would be admitted.
func lintJudgedWrong(s *schemas.Suite, t *schemas.Task) []Problem {
	if JudgeSkip == nil || s == nil || t == nil || utils.Grading(s) != schemas.GradingJudged || s.Judge == nil ||
		s.Judge.PromptVersion == cheatBenchPrompt || utils.OutputKind(s, t) != schemas.OutputAnswer {
		return nil
	}
	ans, err := utils.TaskAnswerSpec(t)
	if err != nil || ans == nil {
		return nil // the answer spec problem is reported by its own check
	}
	candidates := ans.AllWrong()
	for _, m := range t.Controls.Mutants {
		if b, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(m))); err == nil {
			candidates = append(candidates, string(b))
		}
	}
	for _, c := range candidates {
		if JudgeSkip(ans, c) == "" {
			return nil
		}
	}
	file := rel(s.Root, filepath.Join(t.Dir, filepath.FromSlash(answerSpecRel(t))))
	sev := SeverityWarning
	if t.Provenance != nil && t.Provenance.CreatedBy == RowImportCreator {
		sev = SeverityError
	}
	return []Problem{{Path: file, Severity: sev,
		Message: "no wrong answer reaches the judge: every wrong answer and mutant is empty, a refusal or addresses the grader, so it fails before the judge is asked and the readiness check never shows the judge a wrong answer; add a plausible wrong answer (for example the right one with a number or fact changed) to wrong_answers"}}
}

// answerSpecRel is the task-relative path of the hidden answer file.
func answerSpecRel(t *schemas.Task) string {
	if t.Verifier.Answer != "" {
		return t.Verifier.Answer
	}
	return filepath.ToSlash(filepath.Join(t.Verifier.Tests, defaultAnswerSpec))
}

// FileChecks are the admission checks of task t that read files only and
// run nothing: the instruction was edited, no secret in the task files and
// the verifier runs without network (as grader g reports it; nil judges by
// the suite's grader name). Local admission records them after its controls;
// the hosted readiness check records the same ones, so both records hold
// the same rows.
func FileChecks(s *schemas.Suite, t *schemas.Task, g schemas.Grader) []schemas.AdmissionCheck {
	if g == nil {
		g = namedGrader(s.Grader)
	}
	a := &admitter{deps: AdmitDeps{Grader: g, Logger: schemas.NoopLogger{}}, s: s, t: t}
	return []schemas.AdmissionCheck{a.instructionEdited(), a.secretScan(), a.verifierNetwork()}
}

// namedGrader is a stand-in grader that only has a name, for FileChecks
// without the registry.
type namedGrader string

func (n namedGrader) GetName() string    { return string(n) }
func (n namedGrader) GetVersion() string { return "" }
func (n namedGrader) Grade(context.Context, *schemas.RunContext, *schemas.Diff) (*schemas.Grade, error) {
	return nil, errors.New("not a grader")
}
