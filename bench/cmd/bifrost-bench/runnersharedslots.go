package main

import (
	"context"
	"strconv"

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

// sharedWorkerSlots is the slot count the shared worker reports for one org
// (N13): the org's own cap, at most the host pool every org shares, so the
// Workers page and the wizard never promise more runs at once than the host
// runs. The org's fair-share weight stays its own cap (orgHostSlot).
func (h *sharedHost) sharedWorkerSlots(slots int) int {
	if h.hostSlots == nil {
		return slots
	}
	return min(slots, h.hostSlots.Cap())
}

// sharedWorkerLabels are the shared worker's labels: "shared", its
// container runtime and, with a host pool, "host-slots=<n>", so pages can
// say the host cap is shared by every organization (N13).
func (h *sharedHost) sharedWorkerLabels() []string {
	labels := []string{"shared", "runtime=" + h.runtime}
	if h.hostSlots != nil {
		labels = append(labels, "host-slots="+strconv.Itoa(h.hostSlots.Cap()))
	}
	return labels
}
