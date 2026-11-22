package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// failSetup turns the worker side's environment create failure on or off.
func failSetup(side *remoteSide, on bool) {
	side.b.w.mu.Lock()
	defer side.b.w.mu.Unlock()
	side.b.w.fail["create"] = on
}

// TestSameSetupErrorStops is X2: three runs in a row that fail the same way
// before the agent starts stop the experiment's remaining slots with the
// reason, and another experiment still leases.
func TestSameSetupErrorStops(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	fakeCoordClock(rw.c)
	evs := make(chan Event, 256)
	rw.run.Events = evs
	done := rw.attach(t)
	side := newRemoteSide(t)
	failSetup(side, true)
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		l := lease(t, w)
		if l == nil {
			t.Fatalf("lease %d: none", i)
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run %d not taken: %+v", i, res)
		}
	}
	if l := lease(t, w); l != nil {
		t.Fatalf("slot %s leased after the stop", l.Slot.SlotID)
	}
	var errs []string
	for len(evs) > 0 {
		if ev := <-evs; ev.Kind == EventError {
			errs = append(errs, ev.Message)
		}
	}
	stopped := 0
	for _, m := range errs {
		if strings.HasPrefix(m, "stopped: 3 runs in a row failed the same way before the agent started: ") &&
			strings.Contains(m, "injected create failure") && strings.HasSuffix(m, "requeue the stopped runs on the comparison page") {
			stopped++
		}
	}
	if stopped != len(rw.m.Slots) {
		t.Fatalf("%d of %d slots stopped with the reason; errors %q", stopped, len(rw.m.Slots), errs)
	}
	rw.run.Events = nil
	other := attachExperiment(t, rw, "other")
	l := lease(t, w)
	if l == nil || l.ExpID != other.m.ExpID {
		t.Fatalf("another experiment does not lease after the stop: %+v", l)
	}
	if sum := done(); sum.Errors != len(rw.m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
}

// TestSameSetupErrorNoStopAfterAgentRan: once a run reached the agent the
// setup stop never applies; failed slots keep their retries.
func TestSameSetupErrorNoStopAfterAgentRan(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	clock := fakeCoordClock(rw.c)
	done := rw.attach(t)
	defer done()
	side := newRemoteSide(t)
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if i == 1 {
			failSetup(side, true)
		}
		l := lease(t, w)
		if l == nil {
			t.Fatalf("lease %d: none", i)
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run %d not taken: %+v", i, res)
		}
	}
	clock.advance(2 * infraRetryBase)
	if l := lease(t, w); l == nil {
		t.Fatal("failed slots not retried although an agent ran earlier")
	}
}

func TestInfraSignatureNormalizes(t *testing.T) {
	run := func(id, msg string, stage schemas.Stage) *schemas.Run {
		return &schemas.Run{RunID: id, Outcome: schemas.OutcomeAbortedInfra,
			Failures: []schemas.PartFailure{{Stage: stage, Part: "docker_workspace", Error: msg}}}
	}
	a, beforeA := infraSignature(run("r_abc123def", "start services: agentdojo (not ready after 120s): bench-r_abc123def-svcprobe exit 1 on 3f9a2b7c8d", schemas.StagePrepare))
	b, beforeB := infraSignature(run("r_zzz999yyy", "start services: agentdojo (not ready after 90s): bench-r_zzz999yyy-svcprobe exit 2 on 0011aabbcc", schemas.StageArm))
	if !beforeA || !beforeB {
		t.Fatal("prepare and arm failures are before the agent")
	}
	if a != b || a == "" {
		t.Fatalf("signatures differ:\n%s\n%s", a, b)
	}
	if _, before := infraSignature(run("r_1", "x", schemas.StageRun)); before {
		t.Fatal("a run stage failure is not before the agent")
	}
	if _, before := infraSignature(&schemas.Run{Outcome: schemas.OutcomePass}); before {
		t.Fatal("a passed run is not a setup failure")
	}
	if long, _ := infraSignature(run("r_1", strings.Repeat("x", 500), schemas.StagePrepare)); len(long) > 200 {
		t.Fatalf("signature %d bytes", len(long))
	}
}
