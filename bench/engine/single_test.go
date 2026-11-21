package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A one-arm experiment plans one slot per task and trial, with no second arm
// and no paired-verdict requirements, and runs and seals every slot.
func TestPlanAndRunSingleArm(t *testing.T) {
	b := newBench(t)
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 2})
	delete(e.Arms, "B")
	m := b.plan(t, e)
	if len(m.Arms) != 1 || len(m.Experiment.Arms) != 1 {
		t.Fatalf("arms %v", m.Arms)
	}
	counted := slotsWhere(m, func(s schemas.Slot) bool { return s.Counted })
	if len(counted) != 2*2 {
		t.Fatalf("counted slots %d, want 4", len(counted))
	}
	for _, s := range counted {
		if s.Arm != "A" {
			t.Fatalf("slot on arm %s", s.Arm)
		}
	}
	checkArmOrder(t, m)
	ctx := context.Background()
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(counted) || sum.Errors != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

// A two-arm experiment still needs arms A and B for the paired verdict.
func TestPlanTwoArmsStillNeedAB(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Arms["C"] = e.Arms["B"]
	delete(e.Arms, "B")
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), e); err == nil {
		t.Fatal("arms A and C planned under paired_signflip")
	}
}
