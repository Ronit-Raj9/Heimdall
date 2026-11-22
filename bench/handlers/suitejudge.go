package handlers

import (
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
)

// readinessReferenceRuns is how many times a readiness check grades the
// reference (suite admission and the hosted check alike).
const readinessReferenceRuns = 3

// judgeSummary sets the judge of a judged suite and the judge calls of its
// readiness check over the loaded tasks (Y07).
func judgeSummary(s *schemas.Suite, out *schemas.SuiteSummary) {
	if !graders.JudgedSuite(s) {
		return
	}
	j := &schemas.SuiteJudge{Provider: s.Judge.Provider, Model: s.Judge.Model, Votes: graders.JudgeVotes(s)}
	for _, t := range s.Tasks {
		j.ReadinessCalls += graders.ReadinessJudgeCalls(s, t, readinessReferenceRuns)
	}
	out.Judge = j
}
