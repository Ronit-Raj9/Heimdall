package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Audit qc2-03-06: beyond the sizing cap `extend` names the cap, never
// "about 0 more tasks".
func TestSizingTextBeyondCap(t *testing.T) {
	over := &schemas.MoreEvidence{TargetPP: 2, Over: true, TasksTotal: 200, SD: 0.3, SDAssumed: true, CostBasis: "mean cost"}
	got := sizingText(over)
	if !strings.Contains(got, "more than 200 tasks") || strings.Contains(got, "about 0") {
		t.Fatalf("beyond cap: %q", got)
	}
	cost := 12.5
	in := &schemas.MoreEvidence{TargetPP: 10, Tasks: 40, TasksTotal: 60, Runs: 80, SD: 0.3, CostUSD: &cost, CostBasis: "mean cost"}
	if got := sizingText(in); !strings.Contains(got, "about 40 more tasks") || !strings.Contains(got, "12.50 USD") {
		t.Fatalf("in range: %q", got)
	}
}
