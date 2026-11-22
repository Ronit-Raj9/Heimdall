package main

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

type fakeRunnerList []schemas.RunnerView

func (f fakeRunnerList) ListRunners(context.Context, string) ([]schemas.RunnerView, error) {
	return f, nil
}

// TestWorkerSlotsSharedPoolStaleRunner (N90, D6): the shared pool's slots
// come from its runners seen within 10 minutes; when every row is stale
// (a frozen heartbeat) the largest registered slots still give a number;
// a platform target is not bounded; an own worker's pool counts only its
// own runners.
func TestWorkerSlotsSharedPoolStaleRunner(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	stale := fakeRunnerList{
		{Name: "shared-runner-org_a", Pool: "shared", Slots: 2, LastSeen: now.Add(-36 * time.Hour)},
		{Name: "old-shared", Pool: "shared", Slots: 1, LastSeen: now.Add(-48 * time.Hour)},
		{Name: "box", Pool: "gpu", Slots: 3, LastSeen: now.Add(-time.Minute)},
	}
	ctx := context.Background()
	for _, target := range []string{"", "shared"} {
		if n, err := workerSlotsFor(ctx, stale, "org_a", target, now); err != nil || n != 2 {
			t.Fatalf("stale shared pool for %q: %d %v, want 2", target, n, err)
		}
	}
	if n, _ := workerSlotsFor(ctx, stale, "org_a", "worker:gpu", now); n != 3 {
		t.Fatalf("own worker pool: %d, want 3", n)
	}
	if n, _ := workerSlotsFor(ctx, stale, "org_a", "gpu", now); n != 3 {
		t.Fatalf("bare pool name: %d, want 3", n)
	}
	if n, _ := workerSlotsFor(ctx, stale, "org_a", "platform:modal", now); n != 0 {
		t.Fatalf("platform target bounded: %d", n)
	}
	fresh := fakeRunnerList{
		{Name: "a", Pool: "shared", Slots: 2, LastSeen: now.Add(-time.Minute)},
		{Name: "b", Pool: "shared", Slots: 2, Live: true, LastSeen: now.Add(-time.Hour)},
		{Name: "c", Pool: "shared", Slots: 5, LastSeen: now.Add(-time.Hour)},
	}
	if n, _ := workerSlotsFor(ctx, fresh, "org_a", "", now); n != 4 {
		t.Fatalf("fresh shared pool: %d, want 4 (stale rows left out)", n)
	}
	if n, _ := workerSlotsFor(ctx, fakeRunnerList{}, "org_a", "", now); n != 0 {
		t.Fatalf("no runners: %d", n)
	}
}

// TestWorkerTargetChoiceSlots (N93): every worker choice says how many runs
// it runs at once; a sandbox platform choice is not bounded.
func TestWorkerTargetChoiceSlots(t *testing.T) {
	now := time.Now()
	w := workerTargets{runners: fakeRunnerList{
		{Name: "shared-runner-org_a", Pool: "shared", Slots: 2, LastSeen: now},
		{Name: "box", Pool: "gpu", Slots: 3, LastSeen: now},
	}}
	v := &schemas.WorkerTargetView{Target: "shared", Choices: []schemas.WorkerTargetChoice{
		{Target: "shared", Kind: "shared"}, {Target: "platform:modal", Kind: "platform"}, {Target: "worker:gpu", Kind: "worker"},
	}}
	w.fillSlots(context.Background(), "org_a", v)
	if v.Choices[0].Slots != 2 || v.Choices[1].Slots != 0 || v.Choices[2].Slots != 3 {
		t.Fatalf("choice slots %+v", v.Choices)
	}
}
