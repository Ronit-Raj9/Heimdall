package engine

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func startedRun() *schemas.Run {
	now := time.Now()
	return &schemas.Run{RunID: "r1", Timing: schemas.RunTiming{AgentStarted: &now}}
}

// TestUnknownPartKeepsKnownFloor (QC2-07-01, QC2-04-01): an unknown judge
// or simulator bill never erases the observed agent spend; the scheduler,
// resume and the runner cap charge at least that subtotal.
func TestUnknownPartKeepsKnownFloor(t *testing.T) {
	run := startedRun()
	dir := t.TempDir()
	metrics := map[string]schemas.Value{costMetric: schemas.Observed(1.25, schemas.EvidenceGateway)}
	if err := os.WriteFile(filepath.Join(dir, "metrics.json"), []byte(`{"cost_usd":{"state":"observed","v":1.25}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grade.json"), []byte(`{"judge":{"usd":{"state":"unknown","reason":"unpriced"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, ok := spendCost(dir, metrics, run)
	if ok || c != 1.25 {
		t.Fatalf("spendCost = %.3f known=%t, want the 1.25 floor and unknown", c, ok)
	}
	if c, ok, _ := readSpend(dir, run); ok || c != 1.25 {
		t.Fatalf("readSpend = %.3f known=%t, want the 1.25 floor", c, ok)
	}

	b := newBench(t)
	e := testExperiment()
	e.Budget.TotalUSD, e.Budget.ReserveUSD = 1, 0.125
	r := &schedRun{s: NewScheduler(b.reg, b.root, nil, nil), m: b.plan(t, e), freed: make(chan struct{})}
	r.record(&SlotResult{Run: run, Dir: dir, Metrics: metrics}, 0.125)
	if r.sum.SpentUSD < 1.375 {
		t.Fatalf("scheduler charged %.3f, want at least the 1.25 floor plus the 0.125 reserve", r.sum.SpentUSD)
	}
	if !r.stopped {
		t.Fatal("charge past the cap did not stop leasing")
	}

	rs, err := newRunnerSpend(10, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.add(&SlotResult{Run: run, Dir: dir, Metrics: metrics}, nil); err != nil {
		t.Fatal(err)
	}
	if _, spent := rs.reached(); spent < 1.25+DefaultUnknownRunUSD {
		t.Fatalf("runner cap charged %.3f, want the floor plus the default estimate", spent)
	}

	v := costValue(&SlotResult{Run: run, Dir: dir, Metrics: metrics})
	if v.IsObserved() || lowerBound(v) != 1.25 {
		t.Fatalf("worker report %+v: want unknown with a 1.25 lower bound", v)
	}
	r2 := &schedRun{s: r.s, m: r.m, freed: make(chan struct{})}
	if usd, known := r2.charge(&v, 0.125, "test"); known || usd < 1.375 {
		t.Fatalf("coordinator charged a partial worker report %.3f, want at least 1.375", usd)
	}
}

// TestSpendCountsAuditorAndServices (QC2-07-07, QC2-07-06, QC2-04-02): the
// auditor's votes and every gateway-backed service's own spend are part of
// the bill, whatever metrics the experiment selected.
func TestSpendCountsAuditorAndServices(t *testing.T) {
	run := startedRun()
	dir := t.TempDir()
	grade := `{"judge":{"usd":{"state":"observed","v":0.25},"audit":{"usd":{"state":"observed","v":1.25}}}}`
	ev := `{"state":[{"service":"crm","id":"_gateway","gateway":{"usd":{"state":"observed","v":0.5}}},{"service":"files","id":"x"}]}`
	for name, body := range map[string]string{"grade.json": grade, "evidence.json": ev} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	metrics := map[string]schemas.Value{costMetric: schemas.Observed(0, schemas.EvidenceGateway)}
	if c, ok := spendCost(dir, metrics, run); !ok || math.Abs(c-2) > 1e-9 {
		t.Fatalf("spend %.3f known=%t, want 2.00 (judge 0.25 + auditor 1.25 + service 0.50)", c, ok)
	}
	ev = `{"state":[{"service":"crm","id":"_gateway","gateway":{"usd":{"state":"unknown"}}}]}`
	if err := os.WriteFile(filepath.Join(dir, "evidence.json"), []byte(ev), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, ok := spendCost(dir, metrics, run); ok || math.Abs(c-1.5) > 1e-9 {
		t.Fatalf("unobserved service spend: %.3f known=%t, want 1.50 and unknown", c, ok)
	}
	audit := `{"judge":{"usd":{"state":"observed","v":0.25},"audit":{"usd":{"state":"unknown"},"error":"boom"}}}`
	if err := os.WriteFile(filepath.Join(dir, "grade.json"), []byte(audit), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "evidence.json")); err != nil {
		t.Fatal(err)
	}
	if c, ok := spendCost(dir, metrics, run); ok || c != 0.25 {
		t.Fatalf("failed audit: %.3f known=%t, want 0.25 and unknown", c, ok)
	}
}

// TestDetachChargesOpenRemoteLease (QC2-07-02): a lease still out when the
// scheduler stops is charged its reservation, durably, so resume keeps it.
func TestDetachChargesOpenRemoteLease(t *testing.T) {
	e := testExperiment()
	e.Budget.TotalUSD, e.Budget.ReserveUSD = 1, 1
	rw := newRemoteWorld(t, e, RemoteOptions{})
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	if l := lease(t, w); l == nil {
		t.Fatal("no lease")
	}
	if sum := done(); sum.SpentUSD < 1 {
		t.Fatalf("stopped lease charged %.2f, want its 1.00 reservation", sum.SpentUSD)
	}
	rw.run.Resume = true
	rw.attach(t)
	rw.r.mu.Lock()
	spent := rw.r.sum.SpentUSD
	rw.r.mu.Unlock()
	if spent < 1 {
		t.Fatalf("resumed spend %.2f after a stopped lease reserved 1.00", spent)
	}
}

// TestCrashedLeaseChargedOnResume (QC2-07-02): a lease journaled with its
// reservation and no later event (the coordinator crashed) is charged that
// reservation on resume; a lease whose run is sealed is not (QC2-07-08).
func TestCrashedLeaseChargedOnResume(t *testing.T) {
	b := newBench(t)
	e := testExperiment()
	e.Budget.TotalUSD = 1
	m := b.plan(t, e)
	staging := t.TempDir()
	journal := `{"event":"leased","lease":"l1","reserved_usd":0.75}
{"event":"leased","lease":"l2","reserved_usd":0.5}
{"event":"rejected","lease":"l2","charged_usd":0.6}
{"event":"leased","lease":"l3","reserved_usd":0.5}
{"event":"accepted","lease":"l3"}
{"event":"rel`
	if err := os.WriteFile(filepath.Join(staging, m.ExpID+".jsonl"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCoordinator(b.reg, RemoteOptions{Token: remoteTestToken, StagingDir: staging})
	if err != nil {
		t.Fatal(err)
	}
	r := &schedRun{s: NewScheduler(b.reg, b.root, nil, nil), m: m, opts: RunOptions{Remote: c}, freed: make(chan struct{})}
	got, torn, err := r.remoteCharges(map[string]bool{"l2": true})
	if err != nil {
		t.Fatal(err)
	}
	if torn != 1 || len(got) != 1 || got[0].Lease != "l1" || *got[0].ChargedUSD != 0.75 || got[0].CostKnown {
		t.Fatalf("charges %+v torn %d: want only the unsettled l1 at 0.75 and one torn line", got, torn)
	}
}

// TestJournalFailureStopsLeasing (QC2-07-03): a charge that cannot be
// journaled stops leasing instead of being acknowledged as durable.
func TestJournalFailureStopsLeasing(t *testing.T) {
	e := testExperiment()
	e.Budget.TotalUSD = 10
	rw := newRemoteWorld(t, e, RemoteOptions{})
	rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	jp := filepath.Join(rw.c.opts.StagingDir, rw.m.ExpID+".jsonl")
	if err := os.Remove(jp); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(jp, 0o700); err != nil { // appends now fail
		t.Fatal(err)
	}
	cost := schemas.Observed(0.25, schemas.EvidenceGateway)
	if err := rw.c.Release(context.Background(), l.LeaseID, &schemas.WorkerRelease{WorkerID: "w1", Reason: "abandoned", CostUSD: &cost}); err != nil {
		t.Fatal(err)
	}
	rw.r.mu.Lock()
	stopped := rw.r.stopped
	rw.r.mu.Unlock()
	if !stopped {
		t.Fatal("leasing continues after a charge could not be journaled")
	}
	if again := lease(t, w); again != nil {
		t.Fatal("lease granted with an undurable spend record")
	}
}

// TestSettledChargeStopsQueue (QC2-07-04): a recorded charge that reaches
// the cap stops leasing at once, so a slot no worker can take is dropped.
func TestSettledChargeStopsQueue(t *testing.T) {
	r := &schedRun{m: &schemas.Manifest{}, queue: newSlotQueue([]schemas.Slot{{SlotID: "s_gpu"}}), freed: make(chan struct{})}
	r.s = &Scheduler{logger: nopLogger{}}
	r.m.Experiment.Budget.TotalUSD = 1
	r.record(&SlotResult{Run: startedRun(), Metrics: map[string]schemas.Value{costMetric: schemas.Observed(1, schemas.EvidenceGateway)}}, 0)
	if !r.sum.BudgetStopped || r.sum.Pending != 1 {
		t.Fatalf("summary %+v: want stopped with the queued slot pending", r.sum)
	}
}

// TestRetryReservesAgain (QC2-07-05): a retry gives back the attempt's
// reservation and takes a new one, so retries in flight are bounded by the
// cap together with every other reservation.
func TestRetryReservesAgain(t *testing.T) {
	r := &schedRun{m: &schemas.Manifest{}, freed: make(chan struct{})}
	r.s = &Scheduler{logger: nopLogger{}}
	r.m.Experiment.Budget.TotalUSD, r.m.Experiment.Budget.ReserveUSD = 1, 0.125
	holds := make([]*budgetHold, 8)
	for i := range holds {
		est, ok := r.reserve(context.Background())
		if !ok {
			t.Fatalf("first attempt %d not admitted", i)
		}
		holds[i] = &budgetHold{est: est, held: true}
	}
	var mu sync.Mutex
	admitted := 0
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ { // seven abort after spending exactly their estimate
		r.mu.Lock()
		r.observeLocked(0.125)
		r.mu.Unlock()
		wg.Add(1)
		go func(h *budgetHold) {
			defer wg.Done()
			if r.reserveAgain(ctx, h) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}(holds[i])
	}
	wg.Wait()
	r.mu.Lock()
	total := r.sum.SpentUSD + r.reserved
	r.mu.Unlock()
	if total > 1+0.125+1e-9 {
		t.Fatalf("admitted %d retries: spent plus reserved %.3f exceeds the cap by more than one estimate", admitted, total)
	}
	if admitted == 7 {
		t.Fatal("all seven retries admitted on reused reservations")
	}
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// TestRunnerSpendSharedFile (QC2-07-09): two runner processes on one runs
// root add to one total instead of overwriting each other's.
func TestRunnerSpendSharedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".runner", "spend.json")
	a, err := newRunnerSpend(1, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newRunnerSpend(1, p)
	if err != nil {
		t.Fatal(err)
	}
	cost := func(v float64) *SlotResult {
		return &SlotResult{Run: startedRun(), Metrics: map[string]schemas.Value{costMetric: schemas.Observed(v, schemas.EvidenceGateway)}}
	}
	if err := a.add(cost(0.9), nil); err != nil {
		t.Fatal(err)
	}
	if err := b.add(cost(0.1), nil); err != nil {
		t.Fatal(err)
	}
	again, err := newRunnerSpend(1, p)
	if err != nil {
		t.Fatal(err)
	}
	if reached, spent := again.reached(); !reached || math.Abs(spent-1) > 1e-9 {
		t.Fatalf("restarted runner sees %.2f (reached %t), want 1.00 from both", spent, reached)
	}
	if reached, _ := a.reached(); !reached {
		t.Fatal("first runner does not see the second runner's spend")
	}
}

// TestRunnerSpendRejectsNonFiniteCap (QC2-07-11).
func TestRunnerSpendRejectsNonFiniteCap(t *testing.T) {
	for _, c := range []float64{math.NaN(), math.Inf(1)} {
		if _, err := newRunnerSpend(c, ""); err == nil {
			t.Errorf("cap %v accepted", c)
		}
	}
}

// TestRejectedUploadsBoundSlot (QC2-07-12): uploads refused by Complete
// count toward the slot's attempt bound, so a deterministic rejection
// cannot loop over workers forever.
func TestRejectedUploadsBoundSlot(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	ws := []*Worker{rw.worker(t, "w1", newRemoteSide(t)), rw.worker(t, "w2", newRemoteSide(t))}
	counts := map[string]int{}
	for i := 0; i < 4*len(rw.m.Slots)*maxAttempts; i++ {
		w := ws[(i/maxAttempts)%2]
		l := lease(t, w)
		if l == nil {
			continue
		}
		counts[l.Slot.SlotID]++
		out, err := rw.c.Complete(context.Background(), l.LeaseID, &schemas.WorkerComplete{WorkerID: w.ID(), RunID: "nope"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Accepted {
			t.Fatal("empty upload accepted")
		}
	}
	for slot, n := range counts {
		if n > maxAttempts {
			t.Fatalf("slot %s leased %d times after rejected uploads, bound %d", slot, n, maxAttempts)
		}
	}
	if sum := done(); sum.Errors < 1 {
		t.Fatalf("summary %+v: want the rejected slot ended as an error", sum)
	}
}

// TestReconcileClampsCrashedPass is QC2-04-03: a run whose final outcome is
// crash never keeps an observed verified_success of 1.
func TestReconcileClampsCrashedPass(t *testing.T) {
	v := schemas.ReconcileOutcomeMetric("verified_success", schemas.Observed(1, schemas.EvidenceOutcome), schemas.OutcomeCrash)
	if got, ok := v.Float(); !ok || got != 0 {
		t.Fatalf("crash outcome kept %+v", v)
	}
	if sameValue(v, schemas.Observed(1, schemas.EvidenceOutcome)) {
		t.Fatal("sameValue")
	}
}
