package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSchedulerCapBetweenAttempts (audit 2-A-02): a slot's reservation is
// sized for one run, so a retry after an aborted attempt that spent past the
// cap must not start.
func TestSchedulerCapBetweenAttempts(t *testing.T) {
	b := newBench(t)
	b.w.cost = 0.6
	unreachable := []schemas.LedgerEntry{ledgerLine(1, false)}
	b.w.ledgers = [][]schemas.LedgerEntry{unreachable, unreachable, unreachable}
	e := testExperiment()
	e.Budget.TotalUSD = 0.5
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n := b.w.count("exec"); n != 1 || sum.Retried != 0 || !sum.BudgetStopped {
		t.Fatalf("cap=0.50: attempted=%d, retried=%d, spent=%.2f; expected 1 attempt (%+v)", n, sum.Retried, sum.SpentUSD, sum)
	}
}

// TestSchedulerZeroBeforeUnknown (audit 2-A-03): one observed zero (a run
// whose agent never started) must not make later unknown costs free.
func TestSchedulerZeroBeforeUnknown(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Budget.TotalUSD = 0.5
	m := b.plan(t, e)
	r := &schedRun{s: NewScheduler(b.reg, b.root, nil, nil), m: m, freed: make(chan struct{})}
	r.observeLocked(0)
	if est := r.estimateLocked(); est != 0.5 {
		t.Fatalf("estimate after one observed zero = %.2f, want the remaining 0.50", est)
	}
	now := time.Now()
	r.record(&SlotResult{Run: &schemas.Run{RunID: "r1", Timing: schemas.RunTiming{AgentStarted: &now}}}, 0)
	if r.sum.SpentUSD < 0.5 || r.sum.UnknownCostRuns != 1 {
		t.Fatalf("unknown run after an observed zero charged %.2f (%+v)", r.sum.SpentUSD, r.sum)
	}
	if _, ok := r.reserve(context.Background()); ok {
		t.Fatal("reserved past the cap")
	}
}

// TestSpendCostCountsSimulatorAndJudge (audit 2-C-03): the budget counts the
// model-played user's and the judge's spend, not only cost_usd.
func TestSpendCostCountsSimulatorAndJudge(t *testing.T) {
	now := time.Now()
	run := &schemas.Run{Timing: schemas.RunTiming{AgentStarted: &now}}
	metrics := map[string]schemas.Value{
		costMetric:          schemas.Observed(0, schemas.EvidenceGateway),
		simulatorCostMetric: schemas.Observed(2, schemas.EvidenceGateway),
	}
	if c, ok := runCost(metrics, run); !ok || c != 2 {
		t.Fatalf("runCost counted %.2f (known %t) for an observed simulator charge of 2.00", c, ok)
	}
	dir := t.TempDir()
	grade := `{"passed":true,"judged":true,"judge":{"model":"j","usd":{"state":"observed","v":0.5}}}`
	if err := os.WriteFile(filepath.Join(dir, "grade.json"), []byte(grade), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, ok := spendCost(dir, metrics, run); !ok || c != 2.5 {
		t.Fatalf("spendCost = %.2f (known %t), want 2.50 with the judge", c, ok)
	}
	metrics[simulatorCostMetric] = schemas.Unknown("rows missing", schemas.EvidenceGateway)
	if _, ok := runCost(metrics, run); ok {
		t.Fatal("an unobserved simulator charge read as known")
	}
	metrics[simulatorCostMetric] = schemas.NotApplicable()
	if c, ok := runCost(metrics, run); !ok || c != 0 {
		t.Fatalf("scripted user: %.2f %t", c, ok)
	}
	unk := `{"judge":{"usd":{"state":"unknown","reason":"unpriced"}}}`
	if err := os.WriteFile(filepath.Join(dir, "grade.json"), []byte(unk), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := spendCost(dir, metrics, run); ok {
		t.Fatal("an unpriced judge read as known")
	}
}

// TestRemoteUnsealedSpendSurvivesResume (audit 2-C-02): spend charged to a
// remote run that was never sealed is journaled and counted again when the
// experiment resumes, so leasing stays stopped past the cap.
func TestRemoteUnsealedSpendSurvivesResume(t *testing.T) {
	e := testExperiment()
	e.Budget.TotalUSD = 1.0
	rw := newRemoteWorld(t, e, RemoteOptions{})
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	cost := schemas.Observed(1.25, schemas.EvidenceGateway)
	if err := rw.c.Release(context.Background(), l.LeaseID, &schemas.WorkerRelease{WorkerID: "w1", Reason: "abandoned", CostUSD: &cost}); err != nil {
		t.Fatal(err)
	}
	if sum := done(); sum.SpentUSD < 1.25 {
		t.Fatalf("active spend %.2f", sum.SpentUSD)
	}
	rw.run.Resume = true
	rw.attach(t)
	rw.r.mu.Lock()
	spent := rw.r.sum.SpentUSD
	rw.r.mu.Unlock()
	if spent < 1.25 {
		t.Fatalf("resumed spend %.2f, previous spend 1.25 with cap 1.00", spent)
	}
	if again := lease(t, w); again != nil {
		t.Fatalf("resumed scheduler granted work: previous spend 1.25 with cap 1.00, resumed spend %.2f", spent)
	}
}
