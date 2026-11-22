package engine

import (
	"context"
	"testing"
)

// activeProbeRunner runs the slot with the real pipeline and records the
// worker's active runs at the run's first event and when the slot is done.
type activeProbeRunner struct {
	inner      SlotRunner
	w          *Worker
	atStart    []string
	atEnd      []string
	firstRunID string
}

func (r *activeProbeRunner) RunSlot(ctx context.Context, in SlotInput) (*SlotResult, error) {
	emit := in.Emit
	in.Emit = func(ev Event) {
		if emit != nil {
			emit(ev)
		}
		if r.firstRunID == "" && ev.RunID != "" {
			r.firstRunID = ev.RunID
			r.atStart = r.w.ActiveRuns()
		}
	}
	res, err := r.inner.RunSlot(ctx, in)
	r.atEnd = r.w.ActiveRuns()
	return res, err
}

func TestWorkerActiveRuns(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	if got := w.ActiveRuns(); len(got) != 0 {
		t.Fatalf("active before any lease: %v", got)
	}
	probe := &activeProbeRunner{inner: w.runner, w: w}
	w.runner = probe
	l := lease(t, w)
	if res := w.Work(context.Background(), l); res == nil || !res.Accepted {
		t.Fatalf("work: %+v", res)
	}
	if probe.firstRunID == "" {
		t.Fatal("the slot emitted no event with a run id")
	}
	if len(probe.atStart) != 1 || probe.atStart[0] != probe.firstRunID {
		t.Fatalf("active at the run's first event %v, want [%s]", probe.atStart, probe.firstRunID)
	}
	if len(probe.atEnd) != 1 || probe.atEnd[0] != probe.firstRunID {
		t.Fatalf("active when the slot ended %v, want [%s]", probe.atEnd, probe.firstRunID)
	}
	if got := w.ActiveRuns(); len(got) != 0 {
		t.Fatalf("active after Work returned: %v", got)
	}
}
