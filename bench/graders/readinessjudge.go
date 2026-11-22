package graders

// Readiness checks of judged benchmarks (lane Y07). A judged task's
// readiness check must show the judge a wrong answer: the empty control, a
// refusal and an answer that addresses the grader fail before the judge is
// asked (judgeguard.go), so only the reference runs, the mutants and the
// wrong answers the guard lets through reach the judge. These helpers name
// which ones do, count the judge calls a readiness check makes (the Build
// tab asks for consent with that number) and give suite's static validation
// the guard, so a judged task whose every wrong answer is caught before the
// judge is refused.

import (
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	suite.JudgeSkip = JudgeSkipReason
}

// JudgeSkipReason is why the judge would not be asked to grade answer for a
// task with hidden answer ans (it fails unjudged), "" when the judge is
// asked.
func JudgeSkipReason(ans *schemas.AnswerSpec, answer string) string {
	if inj := JudgeInjection(answer); inj != "" {
		return "the answer " + inj
	}
	return answerNonAnswer(ans, answer)
}

// JudgedSuite reports whether s grades with a pinned model judge whose
// guard runs (every prompt but cheatbench-v1).
func JudgedSuite(s *schemas.Suite) bool {
	return s != nil && utils.Grading(s) == schemas.GradingJudged && s.Judge != nil && s.Judge.PromptVersion != PromptCheatBenchV1
}

// JudgedAnswerTask reports whether t is an answer task of a judged suite.
func JudgedAnswerTask(s *schemas.Suite, t *schemas.Task) bool {
	return JudgedSuite(s) && t != nil && utils.OutputKind(s, t) == schemas.OutputAnswer
}

// JudgeVotes is the calls one judged grade makes: the votes (default
// three), twice that with an auditor.
func JudgeVotes(s *schemas.Suite) int {
	if s == nil || s.Judge == nil {
		return 0
	}
	n := s.Judge.Votes
	if n <= 0 {
		n = defaultJudgeVotes
	}
	if s.Judge.Auditor != nil {
		n *= 2
	}
	return n
}

// ReadinessJudgeCalls is how many judge calls the readiness check of task t
// makes at most: referenceRuns reference runs, plus every mutant and wrong
// answer the guard lets through, times JudgeVotes. Zero for a suite that is
// not a judged answer suite.
func ReadinessJudgeCalls(s *schemas.Suite, t *schemas.Task, referenceRuns int) int {
	if !JudgedAnswerTask(s, t) {
		return 0
	}
	ans, _ := utils.TaskAnswerSpec(t)
	graded := referenceRuns
	for _, m := range t.Controls.Mutants {
		if b, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(m))); err == nil && JudgeSkipReason(ans, string(b)) == "" {
			graded++
		}
	}
	if ans != nil {
		for _, w := range ans.AllWrong() {
			if JudgeSkipReason(ans, w) == "" {
				graded++
			}
		}
	}
	return graded * JudgeVotes(s)
}
