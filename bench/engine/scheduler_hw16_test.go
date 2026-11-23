package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N190: no run starts unless its expected cost fits the cap.

func hw16Run(total, reserve float64) *schedRun {
	r := &schedRun{m: &schemas.Manifest{}, freed: make(chan struct{})}
	r.s = &Scheduler{logger: nopLogger{}}
	r.m.Experiment.Budget.TotalUSD, r.m.Experiment.Budget.ReserveUSD = total, reserve
	return r
}

// A budget below one run's expected cost starts nothing and stops with the
// next run's cost.
func TestSchedulerRefusesRunThatDoesNotFit(t *testing.T) {
	r := hw16Run(0.01, 0.02)
	if _, ok := r.reserve(context.Background()); ok {
		t.Fatal("a run expected to cost 0.02 started under a 0.01 cap")
	}
	if !r.sum.BudgetStopped || r.sum.NextRunUSD != 0.02 || r.inflight != 0 {
		t.Fatalf("want a budget stop with next_run_usd 0.02, got %+v (inflight %d)", r.sum, r.inflight)
	}
}

// With no known cost (no observation, no reserve_usd) runs go one at a
// time with the whole remaining budget reserved: only the first run can
// pass the cap.
func TestSchedulerFirstRunUnknownCost(t *testing.T) {
	r := hw16Run(0.01, 0)
	est, ok := r.reserve(context.Background())
	if !ok || est != 0.01 {
		t.Fatalf("first run with an unknown cost: ok=%t est=%.4f, want it started with the whole 0.01 reserved", ok, est)
	}
	if _, ok, wait := r.tryReserve(); ok || !wait {
		t.Fatalf("second run started while the first, of unknown cost, is in flight (ok=%t wait=%t)", ok, wait)
	}
	r.mu.Lock()
	r.observeLocked(0.03) // the first run cost more than the whole budget
	r.mu.Unlock()
	r.release(est)
	if _, ok := r.reserve(context.Background()); ok || !r.sum.BudgetStopped {
		t.Fatalf("a run started after spend passed the cap (%+v)", r.sum)
	}
}

// Observed 0.004 per run under a 0.01 cap: two runs fit, the third would
// pass the cap, so the scheduler stops before it and says what it would
// have cost.
func TestSchedulerStopsWhenNextRunWouldPassCap(t *testing.T) {
	r := hw16Run(0.01, 0)
	started := 0
	for i := 0; i < 5; i++ {
		est, ok := r.reserve(context.Background())
		if !ok {
			break
		}
		started++
		r.mu.Lock()
		r.observeLocked(0.004)
		r.mu.Unlock()
		r.release(est)
	}
	if started != 2 {
		t.Fatalf("started %d runs at 0.004 each under a 0.01 cap, want 2 (the third would pass it)", started)
	}
	if !r.sum.BudgetStopped || r.sum.NextRunUSD != 0.004 {
		t.Fatalf("want a budget stop naming the next run's 0.004, got %+v", r.sum)
	}
	if r.sum.SpentUSD > 0.01+1e-9 {
		t.Fatalf("spent %.4f past the 0.01 cap", r.sum.SpentUSD)
	}
}

// Parallel runs: reserved spend of runs in flight counts, so a run that fits
// only after an in-flight one finishes waits, and is refused at the end.
func TestSchedulerReservedSpendCountsInParallel(t *testing.T) {
	r := hw16Run(0.05, 0.02)
	a, ok1 := r.reserve(context.Background())
	b, ok2 := r.reserve(context.Background())
	if !ok1 || !ok2 {
		t.Fatal("two runs at 0.02 did not both start under a 0.05 cap")
	}
	if _, ok, wait := r.tryReserve(); ok || !wait {
		t.Fatalf("a third run started with 0.04 reserved under a 0.05 cap (ok=%t wait=%t)", ok, wait)
	}
	r.mu.Lock()
	r.observeLocked(0.02)
	r.observeLocked(0.02)
	r.mu.Unlock()
	r.release(a)
	r.release(b)
	if _, ok := r.reserve(context.Background()); ok {
		t.Fatal("a run expected to cost 0.02 started with 0.01 left")
	}
	if !r.sum.BudgetStopped || r.sum.NextRunUSD != 0.02 {
		t.Fatalf("want a budget stop with next_run_usd 0.02, got %+v", r.sum)
	}
}

// The whole scheduler: a budget below one run's reserve starts nothing and
// reports every slot pending.
func TestSchedulerRunBudgetBelowOneRun(t *testing.T) {
	b := newBench(t)
	b.w.cost = 0.02
	e := testExperiment()
	e.Budget.TotalUSD, e.Budget.ReserveUSD = 0.01, 0.02
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 0 || !sum.BudgetStopped || sum.Pending != len(m.Slots) || sum.NextRunUSD != 0.02 {
		t.Fatalf("budget 0.01 under a 0.02 run: executed %d, summary %+v; want 0 runs, all %d slots pending", n, sum, len(m.Slots))
	}
}
