package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// drainLive returns the events on ch until it stays empty for wait.
func drainLive(ch <-chan Event, wait time.Duration) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(wait):
			return out
		}
	}
}

// TestRemoteLiveEventsReachOrgHub: a hosted runner's heartbeat carries its
// run's live events; they land on its organization's event stream with the
// lease's slot id, forged kinds are dropped, and another organization's
// runner cannot post to the lease.
func TestRemoteLiveEventsReachOrgHub(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a, b := newHostedOrg(t, hub, "org_a"), newHostedOrg(t, hub, "org_b")
	mA, mB := a.side.b.plan(t, pooled("")), b.side.b.plan(t, pooled(""))
	evA, evB := make(chan Event, 64), make(chan Event, 64)
	_, doneA := attachTo(t, a.side, a.c, mA, RunOptions{Events: evA})
	_, doneB := attachTo(t, b.side, b.c, mB, RunOptions{Events: evB})
	defer doneA()
	defer doneB()
	ctx := context.Background()

	wa := hostedRunner(t, a.url, tokADefault, "a-1", "")
	wb := hostedRunner(t, b.url, tokBDefault, "b-1", "")
	la := lease(t, wa)
	if la == nil {
		t.Fatal("no org_a lease")
	}
	drainLive(evA, 50*time.Millisecond) // the leased stage event
	at := time.Now().UTC().Add(-time.Second)
	beat := schemas.WorkerHeartbeat{WorkerID: "a-1", Events: []schemas.WorkerEvent{
		{Kind: EventRunStarted, RunID: "r_live1", Message: "attempt 1", At: at},
		{Kind: EventStage, RunID: "r_live1", Stage: schemas.StageRun, Message: "started", At: at},
		{Kind: EventRunSealed, RunID: "r_live1", Message: "forged"}, // never the runner's to send
		{Kind: EventStage, RunID: "../bad", Stage: schemas.StageRun},
	}}
	path := workerAPI + "/leases/" + la.LeaseID + "/heartbeat"
	if err := wa.call(ctx, http.MethodPost, path, beat, nil); err != nil {
		t.Fatal(err)
	}
	got := drainLive(evA, 100*time.Millisecond)
	if len(got) != 2 || got[0].Kind != EventRunStarted || got[1].Kind != EventStage || got[1].Stage != schemas.StageRun {
		t.Fatalf("org_a events %+v", got)
	}
	for _, ev := range got {
		if ev.SlotID != la.Slot.SlotID || ev.RunID != "r_live1" || !ev.At.Equal(at) {
			t.Fatalf("event %+v, want slot %s", ev, la.Slot.SlotID)
		}
	}

	// org_b's runner, on its own server and on org_a's, is refused.
	wb.opts.Server = a.url
	beat.WorkerID = "b-1"
	if err := wb.call(ctx, http.MethodPost, path, beat, nil); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("org_b runner on org_a's lease: %v", err)
	}
	if err := b.c.Heartbeat(schemas.WithHeartbeatEvents(scoped(tokADefault), beat.Events), la.LeaseID, "a-1"); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("org_a scope on org_b: %v", err)
	}
	if err := b.c.Heartbeat(schemas.WithHeartbeatEvents(scoped(tokBDefault), beat.Events), la.LeaseID, "b-1"); !errors.Is(err, schemas.ErrLeaseGone) {
		t.Fatalf("org_b runner with org_a's lease: %v", err)
	}
	if got := drainLive(evA, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("refused posts reached org_a: %+v", got)
	}
	if got := drainLive(evB, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("org_b got events: %+v", got)
	}
}

// TestRemoteLiveEventCaps: one heartbeat publishes at most
// MaxHeartbeatEvents events; messages are cut and redacted, future times
// clamped.
func TestRemoteLiveEventCaps(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	ev := make(chan Event, 2*schemas.MaxHeartbeatEvents)
	rw.run.Events = ev
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	drainLive(ev, 50*time.Millisecond)
	many := make([]schemas.WorkerEvent, schemas.MaxHeartbeatEvents+50)
	for i := range many {
		many[i] = schemas.WorkerEvent{Kind: EventStage, RunID: "r1", Stage: schemas.StageRun, Message: "m"}
	}
	key := "sk-" + strings.Repeat("a", 30)
	many[len(many)-1] = schemas.WorkerEvent{Kind: EventStage, RunID: "r1", Stage: schemas.StageRun,
		Message: "failed: key " + key + " " + strings.Repeat("x", 3000), At: time.Now().Add(time.Hour)}
	if err := w.call(context.Background(), http.MethodPost, workerAPI+"/leases/"+l.LeaseID+"/heartbeat",
		schemas.WorkerHeartbeat{WorkerID: "w1", Events: many}, nil); err != nil {
		t.Fatal(err)
	}
	got := drainLive(ev, 100*time.Millisecond)
	if len(got) != schemas.MaxHeartbeatEvents {
		t.Fatalf("published %d events, want %d", len(got), schemas.MaxHeartbeatEvents)
	}
	last := got[len(got)-1]
	if len(last.Message) > schemas.MaxWorkerEventMessage || strings.Contains(last.Message, key) || !strings.Contains(last.Message, "[redacted]") {
		t.Fatalf("message %d bytes: %.80q", len(last.Message), last.Message)
	}
	if last.At.After(time.Now()) {
		t.Fatalf("future time kept: %v", last.At)
	}
}

// TestLiveBufferBounds: the worker keeps at most maxLiveBuffer events,
// hands out at most MaxHeartbeatEvents per beat, and keeps only progress
// kinds.
func TestLiveBufferBounds(t *testing.T) {
	b := &liveBuffer{}
	b.setSecrets(map[string]string{"K": "supersecretvalue"})
	b.add(Event{Kind: EventRunSealed, RunID: "r"})
	if b.pending() {
		t.Fatal("seal event buffered")
	}
	for i := 0; i < maxLiveBuffer+10; i++ {
		b.add(Event{Kind: EventStage, Stage: schemas.StageRun, Message: "uses supersecretvalue"})
	}
	if b.dropped != 10 {
		t.Fatalf("dropped %d", b.dropped)
	}
	first := b.take()
	if len(first) != schemas.MaxHeartbeatEvents || strings.Contains(first[0].Message, "supersecretvalue") {
		t.Fatalf("take %d, %q", len(first), first[0].Message)
	}
	b.putBack(first)
	n := 0
	for b.pending() {
		n += len(b.take())
	}
	if n != maxLiveBuffer {
		t.Fatalf("took %d events, want %d", n, maxLiveBuffer)
	}
}

// TestRemoteWorkerForwardsLiveEvents: a worker running a leased slot
// forwards its pipeline events before the run is uploaded, so the
// coordinator's stream shows run_started and stages ahead of run_sealed.
func TestRemoteWorkerForwardsLiveEvents(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	ev := make(chan Event, 512)
	rw.run.Events = ev
	done := rw.attach(t)
	defer done()
	side := newRemoteSide(t)
	side.b.w.execDelay = 300 * time.Millisecond
	w := rw.worker(t, "w1", side)
	w.opts.LiveEventEvery = 20 * time.Millisecond
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	if res := w.Work(context.Background(), l); res == nil || !res.Accepted {
		t.Fatalf("run %+v", res)
	}
	got := drainLive(ev, 100*time.Millisecond)
	started, stages, sealedAt := -1, 0, -1
	for i, e := range got {
		switch e.Kind {
		case EventRunStarted:
			if started < 0 {
				started = i
			}
			if e.SlotID != l.Slot.SlotID || e.RunID == "" {
				t.Fatalf("run_started %+v", e)
			}
		case EventStage:
			if e.Stage != schemas.StageLeased {
				stages++
			}
		case EventRunSealed:
			sealedAt = i
		}
	}
	if started < 0 || stages == 0 || sealedAt < started {
		t.Fatalf("started %d stages %d sealed %d in %+v", started, stages, sealedAt, got)
	}
}
