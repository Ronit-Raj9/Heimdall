package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// recordingMeter collects ended leases (lane Y16 metering).
type recordingMeter struct {
	mu   sync.Mutex
	got  []LeaseUsage
	seen chan struct{}
}

func (m *recordingMeter) LeaseEnded(u LeaseUsage) {
	m.mu.Lock()
	m.got = append(m.got, u)
	m.mu.Unlock()
	m.seen <- struct{}{}
}

// TestHostedLeaseMeter: every lease a hosted runner completes is metered
// once, with the runner token's org and pool and a start before its end.
func TestHostedLeaseMeter(t *testing.T) {
	meter := &recordingMeter{seen: make(chan struct{}, 64)}
	hub := NewRemoteHub(RemoteOptions{Meter: meter})
	a := newHostedOrg(t, hub, "org_a")
	m := a.side.b.plan(t, pooled(""))
	_, done := attachTo(t, a.side, a.c, m, RunOptions{})
	w := hostedRunner(t, a.url, tokADefault, "cpu-1", "")
	ctx := context.Background()
	for range m.Slots {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease")
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run %+v", res)
		}
	}
	done()
	for range m.Slots {
		select {
		case <-meter.seen:
		case <-time.After(10 * time.Second):
			t.Fatal("lease not metered")
		}
	}
	meter.mu.Lock()
	defer meter.mu.Unlock()
	if len(meter.got) != len(m.Slots) {
		t.Fatalf("metered %d leases, want %d", len(meter.got), len(m.Slots))
	}
	for _, u := range meter.got {
		if u.OrgID != "org_a" || u.Pool != schemas.DefaultRunnerPool || u.Worker != "cpu-1" || u.Kind != "run" || u.Slots != 1 ||
			u.Start.IsZero() || u.End.Before(u.Start) || u.Shared() {
			t.Fatalf("usage %+v", u)
		}
	}
}

// TestLocalCoordinatorNotMetered: the local coordinator never meters.
func TestLocalCoordinatorNotMetered(t *testing.T) {
	rm := &recordingMeter{seen: make(chan struct{}, 1)}
	c := &Coordinator{opts: RemoteOptions{Meter: rm}, now: time.Now}
	c.meterLocked(&remoteWorker{}, time.Now(), 1, "run")
	select {
	case <-rm.seen:
		t.Fatal("local lease metered")
	case <-time.After(50 * time.Millisecond):
	}
	if (LeaseUsage{Pool: schemas.SharedRunnerPool}).Shared() != true {
		t.Fatal("shared pool")
	}
}
