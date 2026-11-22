package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/hostslots"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Two workers with two slots each share one host slot (Z10, the shared
// worker's host-wide limit): every slot still runs, never more than one at
// a time, and an idle worker does not keep the host slot while it waits.
func TestWorkersShareHostSlot(t *testing.T) {
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 2})
	rw := newRemoteWorld(t, e, RemoteOptions{})
	pool := hostslots.New(1)
	var held, peak, takes atomic.Int32
	gate := func(org string) func(ctx context.Context) (func(), error) {
		acquire := pool.For(org)
		return func(ctx context.Context) (func(), error) {
			rel, err := acquire(ctx)
			if err != nil {
				return nil, err
			}
			takes.Add(1)
			n := held.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			var once sync.Once
			return func() { once.Do(func() { held.Add(-1); rel() }) }, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i, org := range []string{"org_a", "org_b"} {
		side := newRemoteSide(t)
		w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
			WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: []string{"w1", "w2"}[i], Version: BenchVersion,
				PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(), Slots: 2, HostSlot: gate(org)})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Run(ctx); err != nil {
				t.Errorf("worker: %v", err)
			}
		}()
	}
	sum, err := NewScheduler(rw.coord.b.reg, rw.coord.b.root, nil, nil).Run(context.Background(), rw.m,
		RunOptions{Concurrency: 4, Remote: rw.c, RemoteOnly: true})
	cancel()
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(rw.m.Slots) || sum.Remote != len(rw.m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	if peak.Load() != 1 || takes.Load() < int32(len(rw.m.Slots)) {
		t.Fatalf("host slot peak %d, taken %d times", peak.Load(), takes.Load())
	}
	if total, _ := pool.InUse("org_a"); total != 0 || held.Load() != 0 {
		t.Fatalf("host slots still held after the workers stopped: %d", total)
	}
}
