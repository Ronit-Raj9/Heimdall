package main

import (
	"context"

	"github.com/maximhq/bifrost/bench/hostslots"
)

// sharedHostPool is the shared worker's host-wide run limit (Z10): n from
// -host-slots (HEIMDALL_SHARED_HOST_SLOTS), or 0 for one slot per 2 CPUs
// and 4 GB of memory of the host (docs/hosting.md).
func sharedHostPool(n int) *hostslots.Pool {
	if n <= 0 {
		n = hostslots.HostSize()
	}
	return hostslots.New(n)
}

// orgHostSlot is one org's view of the host pool. Its weight is its slot
// count (its own cap, already limited by its max concurrent runs), so when
// the host is contended each org gets slots in proportion to that cap.
func (h *sharedHost) orgHostSlot(org string, slots int) func(ctx context.Context) (func(), error) {
	if h.hostSlots == nil {
		return nil
	}
	h.hostSlots.SetWeight(org, slots)
	return h.hostSlots.For(org)
}
