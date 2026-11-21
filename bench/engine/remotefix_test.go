package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func registerDirect(t *testing.T, c *Coordinator, id, version string) {
	t.Helper()
	if _, err := c.Register(context.Background(), &schemas.WorkerRegister{WorkerID: id, Version: version, Slots: 1}); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteReregisterKeepsSlot is 2-C-01: a worker that restarts and
// registers again while it holds a lease must get that slot back (its old
// store lease is closed), not have it dropped as held by a live owner.
func TestRemoteReregisterKeepsSlot(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	ctx := context.Background()
	registerDirect(t, rw.c, "w1", BenchVersion)
	l1, err := rw.c.Lease(ctx, "w1")
	if err != nil || l1 == nil {
		t.Fatalf("lease: %+v %v", l1, err)
	}
	registerDirect(t, rw.c, "w1", BenchVersion)
	l2, err := rw.c.Lease(ctx, "w1")
	if err != nil || l2 == nil {
		t.Fatalf("lease after restart: %+v %v", l2, err)
	}
	if l2.Slot.SlotID != l1.Slot.SlotID {
		t.Fatalf("restart dropped slot %s; next lease got %s", l1.Slot.SlotID, l2.Slot.SlotID)
	}
	if sum := done(); sum.LeaseHeld != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

// TestRemoteRefusalClearedOnRegistration is 2-C-05: a refusal must not keep
// a corrected worker that registers again from the experiment forever.
func TestRemoteRefusalClearedOnRegistration(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	ctx := context.Background()
	registerDirect(t, rw.c, "w1", BenchVersion)
	l, err := rw.c.Lease(ctx, "w1")
	if err != nil || l == nil {
		t.Fatalf("lease: %+v %v", l, err)
	}
	if err := rw.c.Release(ctx, l.LeaseID, &schemas.WorkerRelease{WorkerID: "w1", Reason: "suite lock hash differs", Refuse: true}); err != nil {
		t.Fatal(err)
	}
	if again, _ := rw.c.Lease(ctx, "w1"); again != nil {
		t.Fatal("refused experiment offered again before a new registration")
	}
	registerDirect(t, rw.c, "w1", BenchVersion)
	if fixed, err := rw.c.Lease(ctx, "w1"); err != nil || fixed == nil {
		t.Fatalf("no lease after clean registration: %+v %v", fixed, err)
	}
}

// TestBudgetStopDropsUnleasableSlots is 2-A-09: once the cap stops leasing,
// queued slots no worker here can take (GPU slots without a GPU worker) are
// counted pending at once, so a waiting CPU worker leaves.
func TestBudgetStopDropsUnleasableSlots(t *testing.T) {
	slots := []schemas.Slot{{SlotID: "s_cpu", OrderIndex: 0}, {SlotID: "s_gpu1", OrderIndex: 1}, {SlotID: "s_gpu2", OrderIndex: 2}}
	r := &schedRun{m: &schemas.Manifest{}, queue: newSlotQueue(slots)}
	r.m.Experiment.Budget.TotalUSD = 1
	isCPU := func(s *schemas.Slot) bool { return s.SlotID == "s_cpu" }
	if s, ok := r.queue.nextWhere(context.Background(), isCPU, true); !ok || s.SlotID != "s_cpu" {
		t.Fatal("cpu slot")
	}
	left := make(chan bool, 1)
	go func() {
		_, ok := r.queue.nextWhere(context.Background(), isCPU, true)
		left <- ok
	}()
	r.mu.Lock()
	r.sum.SpentUSD = 2
	r.mu.Unlock()
	if !r.capReached() {
		t.Fatal("cap not reached")
	}
	select {
	case ok := <-left:
		if ok {
			t.Fatal("worker got a slot after the stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CPU worker still waits on GPU slots after the budget stopped")
	}
	if r.sum.Pending != 2 || !r.sum.BudgetStopped {
		t.Fatalf("summary %+v", r.sum)
	}
}

// TestRemoteReturnedSlotStopsAfterLimit is 2-C-08: a slot given back without
// a run (an archive over the upload cap) must not be leased forever.
func TestRemoteReturnedSlotStopsAfterLimit(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	ctx := context.Background()
	registerDirect(t, rw.c, "w1", BenchVersion)
	var first string
	for i := 0; i < maxAttempts; i++ {
		l, err := rw.c.Lease(ctx, "w1")
		if err != nil || l == nil {
			t.Fatalf("lease %d: %+v %v", i, l, err)
		}
		if first == "" {
			first = l.Slot.SlotID
		}
		if l.Slot.SlotID != first {
			t.Fatalf("lease %d got %s, want %s", i, l.Slot.SlotID, first)
		}
		if err := rw.c.Release(ctx, l.LeaseID, &schemas.WorkerRelease{WorkerID: "w1", Reason: "upload failed: archive too large"}); err != nil {
			t.Fatal(err)
		}
	}
	l, err := rw.c.Lease(ctx, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if l != nil && l.Slot.SlotID == first {
		t.Fatalf("slot %s leased a %dth time", first, maxAttempts+1)
	}
	if sum := done(); sum.Errors != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

// TestAttemptsLeftIgnoresOwnerStopped is 2-A-08: the remote path decides a
// retry by the same chargeable count as settle, so an owner-stopped run does
// not use one of the slot's attempts, whatever the attempt ordinal.
func TestAttemptsLeftIgnoresOwnerStopped(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment())
	slot := &m.Slots[0]
	r := &schedRun{s: NewScheduler(b.reg, b.root, nil, nil), m: m}
	add := func(id string, attempt int, stopped bool) {
		run := schemas.Run{RunID: id, SlotID: slot.SlotID, ExpID: m.ExpID, Attempt: attempt, Outcome: schemas.OutcomeAbortedInfra}
		if stopped {
			run.Failures = []schemas.PartFailure{{Stage: schemas.StageLeased, Part: "engine", Error: ErrRunCancelled.Error() + ": stop"}}
		}
		b.store.mu.Lock()
		b.store.runs[id] = &fakeRun{run: run, sealed: true}
		b.store.bySlot[slot.SlotID] = append(b.store.bySlot[slot.SlotID], id)
		b.store.mu.Unlock()
	}
	add("r_1", 1, true)
	add("r_2", 2, false)
	add("r_3", 3, false) // the remote abort numbered 3 is only the second chargeable one
	if !r.attemptsLeft(context.Background(), slot) {
		t.Fatal("no retry after two chargeable aborts and one owner-stopped run")
	}
	add("r_4", 4, false)
	if r.attemptsLeft(context.Background(), slot) {
		t.Fatal("retry after three chargeable aborts")
	}
}

// TestRemoteSameBuildRestartClearsRejections is QC2-08-03: a worker stopped
// by rejected uploads is leased again after a same-build re-registration
// (its environment was fixed), and Workers shows the count until then.
func TestRemoteSameBuildRestartClearsRejections(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	ctx := context.Background()
	registerDirect(t, rw.c, "w1", BenchVersion)
	rw.c.mu.Lock()
	rw.c.workers["w1"].rejected = maxRejections
	rw.c.mu.Unlock()
	if ws := rw.c.Workers(); len(ws) != 1 || ws[0].Rejected != maxRejections {
		t.Fatalf("workers %+v", ws)
	}
	if l, _ := rw.c.Lease(ctx, "w1"); l != nil {
		t.Fatal("worker at the rejection limit was leased")
	}
	registerDirect(t, rw.c, "w1", BenchVersion)
	if l, err := rw.c.Lease(ctx, "w1"); err != nil || l == nil {
		t.Fatalf("no lease after a same-build re-registration: %+v %v", l, err)
	}
	if ws := rw.c.Workers(); ws[0].Rejected != 0 {
		t.Fatalf("rejected %d after re-registration", ws[0].Rejected)
	}
}

// TestRemoteConcurrentAdmitOnFreshRunner: a fresh runner with two slots
// leases two slots of one experiment at once. Both admit it; the second must
// not see the first one's half-created experiment folder (made before its
// manifest is written) and refuse the experiment for good, which left every
// other slot without a runner (live test l7, hosted runner).
func TestRemoteConcurrentAdmitOnFreshRunner(t *testing.T) {
	for i := 0; i < 5; i++ {
		rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
		done := rw.attach(t)
		side := newRemoteSide(t)
		w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
			WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, Slots: 2,
				PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.register(context.Background()); err != nil {
			t.Fatal(err)
		}
		l1, l2 := lease(t, w), lease(t, w)
		if l1 == nil || l2 == nil {
			t.Fatal("want two leases")
		}
		var wg sync.WaitGroup
		reasons := make([]string, 2)
		for j, l := range []*schemas.WorkerLease{l1, l2} {
			wg.Add(1)
			go func(j int, l *schemas.WorkerLease) {
				defer wg.Done()
				_, reasons[j], _ = w.admit(context.Background(), l)
			}(j, l)
		}
		wg.Wait()
		done()
		for j, r := range reasons {
			if r != "" {
				t.Fatalf("round %d: lease %d refused: %s", i, j, r)
			}
		}
	}
}

// revokeAfterRegister sends a wrong token on every call but register, as a
// coordinator sees a runner whose token was revoked after it registered.
type revokeAfterRegister struct{ base http.RoundTripper }

func (r revokeAfterRegister) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/register") {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer revoked")
	}
	return r.base.RoundTrip(req)
}

// TestRemoteRevokedTokenStopsRunner: a token refused after registration
// (revoked or expired: 401) stops the runner with the error instead of
// asking for leases forever (live test l7: a revoked hosted runner logged
// "lease request failed ... 401" every poll and never exited).
func TestRemoteRevokedTokenStopsRunner(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	side := newRemoteSide(t)
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, Slots: 2,
			PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(),
			HTTP: &http.Client{Timeout: 5 * time.Second, Transport: revokeAfterRegister{http.DefaultTransport}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = w.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("runner with a revoked token kept running until the test deadline")
	}
	if !errors.Is(err, schemas.ErrWorkerAuth) {
		t.Fatalf("run with a token revoked after registration: %v", err)
	}
}
