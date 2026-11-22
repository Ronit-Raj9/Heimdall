package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeClock is a settable clock for the coordinator or a worker.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{at: time.Now()} }

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *fakeClock) add(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
}

// refuseOnce leases a slot to worker id and gives it back refused.
func refuseOnce(t *testing.T, c *Coordinator, id, reason string) {
	t.Helper()
	ctx := context.Background()
	l, err := c.Lease(ctx, id)
	if err != nil || l == nil {
		t.Fatalf("lease: %+v %v", l, err)
	}
	if err := c.Release(ctx, l.LeaseID, &schemas.WorkerRelease{WorkerID: id, Reason: reason, Refuse: true}); err != nil {
		t.Fatal(err)
	}
}

// drainEvents returns every event sent so far.
func drainEvents(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// TestRefusedLeaseOfferedAgain is X3: a refusal keeps the experiment from
// the worker for a minute only; after the third refusal by every worker the
// queued slots fail with the reason instead of waiting forever.
func TestRefusedLeaseOfferedAgain(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	clk := newFakeClock()
	rw.c.now = clk.now
	events := make(chan Event, 256)
	rw.run.Events = events
	done := rw.attach(t)
	ctx := context.Background()
	registerDirect(t, rw.c, "w1", BenchVersion)
	const reason = "load suite: suite draft-x not found"
	for i := 1; i <= 2; i++ {
		refuseOnce(t, rw.c, "w1", reason)
		clk.add(30 * time.Second)
		if l, _ := rw.c.Lease(ctx, "w1"); l != nil {
			t.Fatalf("refusal %d: offered again after 30 s", i)
		}
		clk.add(31 * time.Second)
	}
	refuseOnce(t, rw.c, "w1", reason)
	clk.add(2 * time.Minute)
	if l, _ := rw.c.Lease(ctx, "w1"); l != nil {
		t.Fatal("offered again after the third refusal")
	}
	if n := len(rw.r.queue.pending); n != 0 {
		t.Fatalf("%d slots still queued after every worker refused 3 times", n)
	}
	failed := map[string]bool{}
	for _, ev := range drainEvents(events) {
		if ev.Kind == EventError && strings.Contains(ev.Message, "every worker refused this run 3 times: "+PlainRefusal(reason)) {
			failed[ev.SlotID] = true
		}
	}
	if len(failed) != len(rw.m.Slots) {
		t.Fatalf("slots failed with the reason: %d, want %d", len(failed), len(rw.m.Slots))
	}
	if sum := done(); sum.Errors != len(rw.m.Slots) {
		t.Fatalf("summary errors %d, want %d", sum.Errors, len(rw.m.Slots))
	}
}

// TestRefusalWaitsForOtherWorkers: while another worker has not refused the
// experiment 3 times, the slots stay queued for it.
func TestRefusalWaitsForOtherWorkers(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	clk := newFakeClock()
	rw.c.now = clk.now
	done := rw.attach(t)
	defer done()
	registerDirect(t, rw.c, "w1", BenchVersion)
	registerDirect(t, rw.c, "w2", BenchVersion)
	for range 3 {
		refuseOnce(t, rw.c, "w1", "local suite lock differs")
		clk.add(61 * time.Second)
	}
	if n := len(rw.r.queue.pending); n != len(rw.m.Slots) {
		t.Fatalf("pending %d, want %d: w2 never refused", n, len(rw.m.Slots))
	}
	if l, _ := rw.c.Lease(context.Background(), "w2"); l == nil {
		t.Fatal("w2 got no lease")
	}
}

// TestRefusalEventInLog: the refusal is in the job's event log with the
// reason and the retry.
func TestRefusalEventInLog(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	events := make(chan Event, 64)
	rw.run.Events = events
	done := rw.attach(t)
	defer done()
	registerDirect(t, rw.c, "w1", BenchVersion)
	refuseOnce(t, rw.c, "w1", "suite lock hash differs")
	want := "worker w1 could not take this run: The benchmark changed after this run was planned: start it again. Details: suite lock hash differs. Heimdall offers it again in 1 minute."
	for _, ev := range drainEvents(events) {
		if ev.Kind == EventError && ev.Message == want && ev.SlotID != "" {
			return
		}
	}
	t.Fatalf("no event %q", want)
}

// refusingSide is a worker side whose project does not reproduce the
// manifest (another suite lock), so admit refuses.
func refusingSide(t *testing.T) *remoteSide {
	side := newRemoteSide(t)
	side.b.suite.LockHash = "lock-other"
	return side
}

// TestWorkerRefusalCacheExpires: the worker reuses a refusal for a minute,
// then syncs its project and checks the experiment again.
func TestWorkerRefusalCacheExpires(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", refusingSide(t))
	clk := newFakeClock()
	w.now = clk.now
	var syncs int
	w.opts.Sync = func(context.Context) error { syncs++; return nil }
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	ctx := context.Background()
	if _, why, permanent := w.admit(ctx, l); why == "" || !permanent || syncs != 1 {
		t.Fatalf("first admit: why %q permanent %v syncs %d", why, permanent, syncs)
	}
	clk.add(30 * time.Second)
	if _, why, _ := w.admit(ctx, l); !strings.Contains(why, "suite lock hash") || syncs != 1 {
		t.Fatalf("cached admit: why %q syncs %d", why, syncs)
	}
	clk.add(31 * time.Second)
	if _, why, _ := w.admit(ctx, l); why == "" || syncs != 2 {
		t.Fatalf("admit after a minute: why %q syncs %d, want a new check", why, syncs)
	}
}

// TestTransientRefusalRetried: a check that fails once (a docker image
// inspect killed on a busy host) no longer stops the experiment for good.
func TestTransientRefusalRetried(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	cclk := newFakeClock()
	rw.c.now = cclk.now
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	wclk := newFakeClock()
	w.now = wclk.now
	var syncs int
	w.opts.Sync = func(context.Context) error { syncs++; return nil }
	plan := w.replan
	var calls int
	w.replan = func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("build agent image bench/agent-x:1: inspect image: docker image: signal: killed")
		}
		return plan(ctx, e)
	}
	ctx := context.Background()
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	if res := w.Work(ctx, l); res != nil {
		t.Fatalf("first lease ran: %+v", res)
	}
	cclk.add(61 * time.Second)
	wclk.add(61 * time.Second)
	l2 := lease(t, w)
	if l2 == nil {
		t.Fatal("experiment not offered again after the refusal")
	}
	if res := w.Work(ctx, l2); res == nil || !res.Accepted {
		t.Fatalf("second lease did not run: %+v", res)
	}
	if syncs != 2 {
		t.Fatalf("syncs %d, want 2", syncs)
	}
}

// TestPlainRefusalText: the worker reasons a person can act on read as a
// plain sentence, with the raw reason kept after "Details: " (N151).
func TestPlainRefusalText(t *testing.T) {
	cases := map[string]string{
		"load suite: suite zz-test-mcp: folder /data/heimdall-runner/projects/org_1/benchmarks/zz-test-mcp not found": "The worker's copy of your project does not have this benchmark yet. Details: load suite: suite zz-test-mcp: folder /data/heimdall-runner/projects/org_1/benchmarks/zz-test-mcp not found",
		"manifest hashes to sha256:a; it records sha256:b and the lease sha256:b":                                     "The benchmark changed after this run was planned: start it again. Details: manifest hashes to sha256:a; it records sha256:b and the lease sha256:b",
		"local suite lock hash x is not the manifest's y":                                                             "The benchmark changed after this run was planned: start it again. Details: local suite lock hash x is not the manifest's y",
		"part versions differ from the manifest's: grader x":                                                          "The worker runs a different Heimdall version than the server. Details: part versions differ from the manifest's: grader x",
		"docker daemon not reachable":                                                                                 "docker daemon not reachable",
	}
	for raw, want := range cases {
		if got := PlainRefusal(raw); got != want {
			t.Errorf("PlainRefusal(%q)\n got %q\nwant %q", raw, got, want)
		}
	}
}
