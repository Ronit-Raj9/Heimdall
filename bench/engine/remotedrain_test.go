package engine

import (
	"context"
	"testing"

	drainmode "github.com/maximhq/bifrost/bench/drain"
)

// TestRemoteDrainStopsNewLeasesOnly: with drain mode on the coordinator
// hands out no new lease, while a lease already out keeps its heartbeats and
// its upload, and is counted once. Turning drain off leases again.
func TestRemoteDrainStopsNewLeasesOnly(t *testing.T) {
	t.Cleanup(func() { drainmode.Stop() })
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	ctx := context.Background()
	out := lease(t, w)
	if out == nil {
		t.Fatal("no lease before drain")
	}
	drainmode.Start("test")
	if st := rw.c.Stats(); st.Leases != 1 {
		t.Fatalf("leases in flight %d, want 1", st.Leases)
	}
	if l := lease(t, w); l != nil {
		t.Fatalf("lease %s handed out while draining", l.LeaseID)
	}
	if res := w.Work(ctx, out); res == nil || !res.Accepted {
		t.Fatalf("in-flight run while draining: %+v", res)
	}
	if st := rw.c.Stats(); st.Leases != 0 {
		t.Fatalf("leases in flight %d after the run finished, want 0", st.Leases)
	}
	if l := lease(t, w); l != nil {
		t.Fatalf("lease %s handed out while draining", l.LeaseID)
	}
	drainmode.Stop()
	for range len(rw.m.Slots) - 1 {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease after drain mode was turned off")
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run after drain: %+v", res)
		}
	}
	if sum := done(); sum.Remote != len(rw.m.Slots) || sum.LeaseLost != 0 {
		t.Fatalf("summary %+v", sum)
	}
}
