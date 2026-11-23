package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestParseDecisionsLeader: -fail-on accepts the setups decision leader
// (fix wave 3, N244).
func TestParseDecisionsLeader(t *testing.T) {
	got, err := parseDecisions("leader,regressed")
	if err != nil || len(got) != 2 || got[0] != schemas.DecisionLeader {
		t.Fatalf("got %v, %v", got, err)
	}
}
