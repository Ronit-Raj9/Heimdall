package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// testClock is a coordinator clock a test moves by hand.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (k *testClock) now() time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.at
}

func (k *testClock) advance(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.at = k.at.Add(d)
}

// fakeCoordClock gives c a clock that stands still until the test advances
// it. It must be set before the experiment is attached (the sweep reads it).
func fakeCoordClock(c *Coordinator) *testClock {
	k := &testClock{at: time.Now()}
	c.now = k.now
	return k
}

// attachExperiment plans one more experiment on rw's bench and attaches it to
// rw's coordinator the way remoteWorld.attach does.
func attachExperiment(t *testing.T, rw *remoteWorld, name string) *schedRun {
	t.Helper()
	ctx := context.Background()
	e := testExperiment()
	e.Name = name
	m := rw.coord.b.plan(t, e)
	s := NewScheduler(rw.coord.b.reg, rw.coord.b.root, nil, nil)
	r, err := s.start(ctx, m, rw.run)
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = ctx, newSlotQueue(m.Slots)
	t.Cleanup(rw.c.attach(r))
	return r
}

// slotsWorker is rw.worker with a slot count.
func slotsWorker(t *testing.T, rw *remoteWorld, id string, side *remoteSide, slots int) *Worker {
	t.Helper()
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: id, Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(), Slots: slots})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestLeaseFairAcrossExperiments is X2: with more experiments than host
// slots, every experiment gets a turn instead of the oldest taking them all.
func TestLeaseFairAcrossExperiments(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	b := attachExperiment(t, rw, "second")
	c := attachExperiment(t, rw, "third")
	w := slotsWorker(t, rw, "w1", newRemoteSide(t), 2)
	ctx := context.Background()
	got := map[string]int{}
	var order []string
	for pair := 0; pair < 3; pair++ {
		var ls []*schemas.WorkerLease
		for range 2 {
			l := lease(t, w)
			if l == nil {
				t.Fatalf("pair %d: no lease while slots are pending", pair)
			}
			ls = append(ls, l)
			got[l.ExpID]++
			order = append(order, l.ExpID)
		}
		for _, l := range ls {
			if res := w.Work(ctx, l); res == nil || !res.Accepted {
				t.Fatalf("run not accepted: %+v", res)
			}
		}
	}
	for _, id := range []string{rw.m.ExpID, b.m.ExpID, c.m.ExpID} {
		if got[id] != 2 {
			t.Fatalf("experiment %s got %d of 6 leases, want 2 each (order %v)", id, got[id], order)
		}
	}
}

// TestFairOrderPrefersFewestLeasesThenOldestTurn pins the sort keys.
func TestFairOrderPrefersFewestLeasesThenOldestTurn(t *testing.T) {
	now := time.Now()
	a := &schedRun{m: &schemas.Manifest{ExpID: "e_a"}, lastLeased: now.Add(-time.Minute)}
	b := &schedRun{m: &schemas.Manifest{ExpID: "e_b"}, lastLeased: now}
	c := &schedRun{m: &schemas.Manifest{ExpID: "e_c"}}
	d := &schedRun{m: &schemas.Manifest{ExpID: "e_d"}}
	co := &Coordinator{leases: map[string]*remoteLease{"l1": {r: a}}}
	out := co.fairOrderLocked([]*schedRun{a, b, c, d})
	var ids []string
	for _, r := range out {
		ids = append(ids, r.m.ExpID)
	}
	want := []string{"e_c", "e_d", "e_b", "e_a"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order %v, want %v", ids, want)
		}
	}
}

// TestInfraRetryWaits is X2: a slot whose run ended aborted_infra is not
// leased again for infraRetryBase (attempt 1), and is leased after.
func TestInfraRetryWaits(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	clock := fakeCoordClock(rw.c)
	done := rw.attach(t)
	defer done()
	side := newRemoteSide(t)
	side.b.w.fail["create"] = true
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	l1 := lease(t, w)
	if l1 == nil {
		t.Fatal("no first lease")
	}
	res := w.Work(ctx, l1)
	if res == nil || !res.Accepted {
		t.Fatalf("aborted run not taken: %+v", res)
	}
	clock.advance(infraRetryBase - time.Second)
	l2 := lease(t, w)
	if l2 == nil {
		t.Fatal("no lease of the other slots")
	}
	if l2.Slot.SlotID == l1.Slot.SlotID {
		t.Fatalf("slot %s leased again %s after an aborted_infra run", l1.Slot.SlotID, infraRetryBase-time.Second)
	}
	if err := rw.c.Release(ctx, l2.LeaseID, &schemas.WorkerRelease{WorkerID: "w1", Reason: "test gives it back"}); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Second)
	l3 := lease(t, w)
	if l3 == nil || l3.Slot.SlotID != l1.Slot.SlotID {
		t.Fatalf("after the wait got %+v, want slot %s", l3, l1.Slot.SlotID)
	}
}

func TestInfraRetryDelayGrows(t *testing.T) {
	if infraRetryDelay(1) != 30*time.Second || infraRetryDelay(2) != 60*time.Second {
		t.Fatalf("delays %s %s", infraRetryDelay(1), infraRetryDelay(2))
	}
}
