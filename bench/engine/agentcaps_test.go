package engine

import (
	"context"
	"strings"
	"testing"
)

// A01: an arm whose agent lacks a capability the benchmark needs is refused
// at plan time with the contract's message.
func TestPlanRefusesIncompatibleAgent(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	arm := e.Arms["B"]
	arm.Set = map[string]any{"capabilities": []any{"tools", "sessions"}}
	e.Arms["B"] = arm
	_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), "agent claude-code cannot run benchmark coding-core: needs shell") {
		t.Fatalf("err = %v", err)
	}

	e = testExperiment()
	arm = e.Arms["B"]
	arm.Set = map[string]any{"capabilities": []any{"shell"}}
	e.Arms["B"] = arm
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err != nil {
		t.Fatalf("declared shell on a coding benchmark: %v", err)
	}
}
