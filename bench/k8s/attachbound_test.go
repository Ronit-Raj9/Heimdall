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
	done := make(chan *schemas.ExecResult, 1)
	go func() {
		res, _ := d.Exec(ctx, rc, plan)
		done <- res
	}()
	select {
	case res := <-done:
		if res == nil || !res.TimedOut {
			t.Fatalf("result %+v, want a timed-out run", res)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Exec still blocked in attach long after the wall budget")
	}
}
