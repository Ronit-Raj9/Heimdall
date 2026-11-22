package main

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/hostslots"
)

// TestSharedWorkerReportsHostCappedSlots (N13): with a host pool of 2 and an
// org cap of 4, the shared worker reports 2 slots and a host-slots label;
// without a host pool it reports the org cap and no such label.
func TestSharedWorkerReportsHostCappedSlots(t *testing.T) {
	h := &sharedHost{runtime: "runsc", hostSlots: hostslots.New(2)}
	if got := h.sharedWorkerSlots(4); got != 2 {
		t.Fatalf("slots %d, want 2 (host cap)", got)
	}
	if got := h.sharedWorkerSlots(1); got != 1 {
		t.Fatalf("slots %d, want 1 (org cap below the host cap)", got)
	}
	if got := h.sharedWorkerLabels(); !slices.Equal(got, []string{"shared", "runtime=runsc", "host-slots=2"}) {
		t.Fatalf("labels %v", got)
	}
	free := &sharedHost{runtime: "runsc"}
	if got := free.sharedWorkerSlots(4); got != 4 {
		t.Fatalf("slots without a host pool %d, want 4", got)
	}
	if got := free.sharedWorkerLabels(); !slices.Equal(got, []string{"shared", "runtime=runsc"}) {
		t.Fatalf("labels without a host pool %v", got)
	}
}
