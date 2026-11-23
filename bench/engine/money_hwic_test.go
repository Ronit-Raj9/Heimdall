package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Money review of the merged lanes H13, H14 and H16 (fix wave 3, group C):
// the scheduler side of the budget rules. The handler side is in
// handlers/money_hwic_test.go.

// threeRuns is a one-arm experiment of three runs (one slot per task).
func threeRuns() *schemas.Experiment {
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02", "t03"}}, Trials: 1})
	e.Arms = map[string]schemas.ArmSpec{"A": {Label: "baseline"}}
	return e
}

// Rules 2 and 3: a budget that fits two runs of three starts exactly two,
// stops with the next run's cost, and a raised budget starts the third.
func TestMoneyTwoOfThreeRunsThenRaise(t *testing.T) {
	b := newBench(t)
	b.w.cost = 0.6
	e := threeRuns()
	e.Budget.TotalUSD, e.Budget.ReserveUSD = 1.5, 0.6 // 2.5 runs
	m := b.plan(t, e)
	if len(m.Slots) != 3 {
		t.Fatalf("planned %d slots, want 3", len(m.Slots))
	}
	ctx := context.Background()
	events := make(chan Event, 256)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Concurrency: 3, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 2 || sum.Sealed != 2 || sum.Pending != 1 || !sum.BudgetStopped || sum.NextRunUSD != 0.6 {
		t.Fatalf("budget for 2.5 runs: executed %d, summary %+v; want 2 runs, 1 pending, a budget stop naming 0.6", n, sum)
	}
	if drain(events)[EventBudgetStop] != 1 {
		t.Fatal("no budget_stop event")
	}
	raised, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true, Concurrency: 3, TotalUSD: 2.1})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 3 || raised.Sealed != 1 || raised.Skipped != 2 || raised.Pending != 0 || raised.BudgetStopped {
		t.Fatalf("raised to 3.5 runs: executed %d in all, summary %+v; want the third run started", n, raised)
	}
}

// Rule 4: with an unknown price (no observed cost, no reserve_usd) runs go
// one at a time with the whole remaining budget reserved, so with three runs
// allowed at once only the first starts; its unknown cost is counted at that
// reservation, so nothing else starts.
func TestMoneyUnknownPriceOneAtATime(t *testing.T) {
	b := newBench(t)
	b.w.costUnknown = true
	e := threeRuns()
	e.Budget.TotalUSD = 1
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 1 || !sum.BudgetStopped || sum.Pending != 2 || sum.UnknownCostRuns != 1 {
		t.Fatalf("unknown price, three at once allowed: executed %d, summary %+v; want exactly one run", n, sum)
	}
}

// Rule 4, evaluation case: an unpriced evaluation reserves the budget split
// over its runs (handlers.quickReserve, rounded down); every planned run
// fits, and the unknown costs are counted at that split.
func TestMoneyUnknownPriceEvenSplitRunsAll(t *testing.T) {
	b := newBench(t)
	b.w.costUnknown = true
	e := threeRuns()
	e.Budget.TotalUSD, e.Budget.ReserveUSD = 5, 1.666666 // quickReserve(5, 3)
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 3 || sum.BudgetStopped {
		t.Fatalf("even split of $5 over 3 unpriced runs: executed %d, summary %+v; want all 3", n, sum)
	}
}
