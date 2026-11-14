package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheckExitCode(t *testing.T) {
	failOn := []schemas.Decision{schemas.DecisionRegressed, schemas.DecisionInvalid}
	withPolicy := func(d schemas.PolicyDecision) *schemas.PolicyResult {
		return &schemas.PolicyResult{PolicyName: "p", Decision: d}
	}
	tests := []struct {
		name     string
		decision schemas.Decision
		policy   *schemas.PolicyResult
		want     int
	}{
		{"improved, no policy", schemas.DecisionImproved, nil, 0},
		{"improved, policy allows", schemas.DecisionImproved, withPolicy(schemas.PolicyAllow), 0},
		{"improved, policy warns", schemas.DecisionImproved, withPolicy(schemas.PolicyWarn), 0},
		{"improved, policy blocks", schemas.DecisionImproved, withPolicy(schemas.PolicyBlock), 5},
		{"inconclusive not in fail-on, policy blocks", schemas.DecisionInconclusive, withPolicy(schemas.PolicyBlock), 5},
		{"regressed, policy blocks: policy first", schemas.DecisionRegressed, withPolicy(schemas.PolicyBlock), 5},
		{"invalid, policy blocks: policy first", schemas.DecisionInvalid, withPolicy(schemas.PolicyBlock), 5},
		{"regressed, policy allows", schemas.DecisionRegressed, withPolicy(schemas.PolicyAllow), 1},
		{"invalid, no policy", schemas.DecisionInvalid, nil, 3},
		{"inconclusive not in fail-on", schemas.DecisionInconclusive, withPolicy(schemas.PolicyWarn), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			an := &schemas.Analysis{Verdict: schemas.Verdict{Decision: tt.decision}, Policy: tt.policy}
			if got := checkExitCode(an, failOn); got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}
