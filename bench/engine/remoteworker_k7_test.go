package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// K7: with SlotAgentImages set, the worker replans a hosted manifest with
// per-task builds deferred, calls the hook once per slot before it runs,
// and a failed hook gives that slot back (not refused) while the other
// slots still run.
func TestWorkerSlotAgentImagesPerSlot(t *testing.T) {
	rw := newDeferredImageWorld(t, nil)
	done := rw.attach(t)
	side := imageSide(t, map[string]string{testBaseImage: "sha256:base1aaaaaaaaaaaa"}, nil)
	var mu sync.Mutex
	var deferred []bool
	var calls []string
	failTask := rw.m.Slots[0].Task
	plan := NewPlanner(side.b.reg, side.b.root, nil).Plan
	replan := func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
		mu.Lock()
		deferred = append(deferred, taskAgentBuildsDeferred(ctx))
		mu.Unlock()
		return plan(ctx, e)
	}
	failed := false
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), replan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(),
			SlotAgentImages: func(_ context.Context, m *schemas.Manifest, s *schemas.Suite, slot *schemas.Slot) error {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, slot.SlotID)
				if slot.Task == failTask && !failed {
					failed = true
					return errors.New("prepare the agent container for task " + slot.Task + ": build failed")
				}
				return nil
			}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	accepted := 0
	for range len(rw.m.Slots) + 1 {
		l := lease(t, w)
		if l == nil {
			break
		}
		if res := w.Work(ctx, l); res != nil && res.Accepted {
			accepted++
		}
	}
	if sum := done(); sum.Sealed != len(rw.m.Slots) {
		t.Fatalf("summary %+v: a slot's failed image build stopped other slots", sum)
	}
	if accepted != len(rw.m.Slots) {
		t.Fatalf("accepted %d of %d", accepted, len(rw.m.Slots))
	}
	if len(deferred) != 1 || !deferred[0] {
		t.Fatalf("replans %v, want one replan with per-task builds deferred", deferred)
	}
	if len(calls) != len(rw.m.Slots)+1 {
		t.Fatalf("hook calls %v", calls)
	}
}

// Without the hook (an org's own runner, a platform worker) the replan
// builds as before; with it, only a hosted manifest defers.
func TestWorkerDefersTaskAgentBuildsOnlyWithHookAndHostedManifest(t *testing.T) {
	hosted := &schemas.Manifest{Gateway: &schemas.GatewayManifest{DeferredToRunner: true}}
	if (&Worker{}).deferTaskAgentBuilds(hosted) {
		t.Fatal("deferred without the hook")
	}
	w := &Worker{opts: WorkerOptions{SlotAgentImages: func(context.Context, *schemas.Manifest, *schemas.Suite, *schemas.Slot) error { return nil }}}
	if !w.deferTaskAgentBuilds(hosted) {
		t.Fatal("hosted manifest not deferred with the hook")
	}
	if w.deferTaskAgentBuilds(&schemas.Manifest{Gateway: &schemas.GatewayManifest{}}) {
		t.Fatal("a manifest that froze its own image ids was deferred")
	}
	frozen := &schemas.Manifest{Gateway: &schemas.GatewayManifest{DeferredToRunner: true}, TaskImages: map[string]string{"t01": "sha256:x"}}
	if w.deferTaskAgentBuilds(frozen) {
		t.Fatal("a hosted manifest with frozen task image ids was deferred")
	}
}
