package main

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
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

func TestArmCounts(t *testing.T) {
	tests := []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{"B": 2, "A": 3}, "arm A 3, arm B 2"},
		{map[string]int{"A": 1}, "arm A 1"},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := armCounts(tt.counts); got != tt.want {
			t.Errorf("armCounts(%v) = %q, want %q", tt.counts, got, tt.want)
		}
	}
}

func TestProgressLine(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 49, 41, 0, time.UTC)
	tests := []struct {
		name string
		ev   engine.Event
		want string
	}{
		{"stage names the stage", engine.Event{Kind: engine.EventStage, SlotID: "s_1", Stage: schemas.Stage("prepare"), Message: "started", At: at},
			"00:49:41 stage          s_1 prepare started"},
		{"sealed run", engine.Event{Kind: engine.EventRunSealed, SlotID: "s_1", RunID: "r_2", Outcome: schemas.Outcome("pass"), At: at},
			"00:49:41 run_sealed     s_1 r_2 pass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressLine(tt.ev); got != tt.want {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}
