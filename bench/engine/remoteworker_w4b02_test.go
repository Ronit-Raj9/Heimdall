package engine

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// F1.1 part B: a lease whose preparation waits for a container image shows
// the live stage "image" with the plain progress line.
func TestPreparingImageIsLive(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	ev := make(chan Event, 512)
	rw.run.Events = ev
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	w.opts.LiveEventEvery = 20 * time.Millisecond
	plan := w.replan
	w.replan = func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
		utils.ImageProgress(ctx)("Preparing the container image x")
		time.Sleep(100 * time.Millisecond) // one heartbeat forwards the event
		return plan(ctx, e)
	}
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	if res := w.Work(context.Background(), l); res == nil || !res.Accepted {
		t.Fatalf("run %+v", res)
	}
	for _, e := range drainLive(ev, 100*time.Millisecond) {
		if e.Kind == EventStage && e.Stage == StageImage && e.Message == "Preparing the container image x" {
			return
		}
	}
	t.Fatal("no live image stage reached the coordinator")
}
