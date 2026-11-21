package k8s

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// 2-D-06: a stalled attach stream must not outlive the wall budget plus the
// drain window, whatever happens to the kill request.
func TestStalledAttachEndsAfterWall(t *testing.T) {
	f := newFakeAPI(t)
	f.stall = make(chan struct{})
	t.Cleanup(func() { close(f.stall) })
	d := testDriver(t, f)
	d.attachDrain = 200 * time.Millisecond
	rc := testRun(t)
	ctx := t.Context()
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := &schemas.RunPlan{Image: "bench/coding-core-base:1", Argv: []string{"sleep", "600"}, WallS: 1}
	type out struct {
		res *schemas.ExecResult
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := d.Exec(ctx, rc, plan)
		done <- out{res, err}
	}()
	select {
	case o := <-done:
		if o.res == nil || !o.res.TimedOut {
			t.Fatalf("result %+v, want a timed-out run", o.res)
		}
		if o.err != nil { // QC2-10-08: the driver's own drain close is no infrastructure error
			t.Fatalf("timed-out run returned an error: %v", o.err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Exec still blocked in attach long after the wall budget")
	}
}
