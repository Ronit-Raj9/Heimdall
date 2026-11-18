package engine

import (
	"errors"
	"fmt"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RemoteHub holds the hosted coordinators, one per organization (Runners
// area, docs/runners.md). Tenancy builds an organization's runtime over its
// own store and asks the hub for that organization's coordinator; the
// coordinator then outlives the runtime, so leases survive a runtime being
// rebuilt. The runner admin API reaches live state through the hub.
type RemoteHub struct {
	opts RemoteOptions

	mu    sync.Mutex
	byOrg map[string]*Coordinator
}

// NewRemoteHub returns a hub whose coordinators take opts (limits, logger,
// book); OrgID and StagingDir are set per organization, and Token must be
// empty.
func NewRemoteHub(opts RemoteOptions) *RemoteHub {
	opts.Token, opts.OrgID, opts.StagingDir = "", "", ""
	return &RemoteHub{opts: opts, byOrg: map[string]*Coordinator{}}
}

// For returns the organization's coordinator, creating it over reg's store
// (its staging folder under that store's root) on first use.
func (h *RemoteHub) For(orgID string, reg *schemas.Registry) (*Coordinator, error) {
	if orgID == "" {
		return nil, errors.New("remote hub: empty organization id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.byOrg[orgID]; ok {
		return c, nil
	}
	opts := h.opts
	opts.OrgID = orgID
	c, err := NewCoordinator(reg, opts)
	if err != nil {
		return nil, fmt.Errorf("organization %s: %w", orgID, err)
	}
	h.byOrg[orgID] = c
	return c, nil
}

// Get returns the organization's coordinator if one was built.
func (h *RemoteHub) Get(orgID string) (*Coordinator, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.byOrg[orgID]
	return c, ok
}

// Workers lists the organization's live workers; none when it has no
// coordinator yet.
func (h *RemoteHub) Workers(orgID string) []schemas.WorkerInfo {
	if c, ok := h.Get(orgID); ok {
		return c.Workers()
	}
	return []schemas.WorkerInfo{}
}

// Forget removes one of the organization's workers (Coordinator.Forget).
func (h *RemoteHub) Forget(orgID, workerID, why string) bool {
	if c, ok := h.Get(orgID); ok {
		return c.Forget(workerID, why)
	}
	return false
}

// ForgetToken removes the organization's workers registered with a runner
// token (Coordinator.ForgetToken).
func (h *RemoteHub) ForgetToken(orgID, tokenID, why string) int {
	if c, ok := h.Get(orgID); ok {
		return c.ForgetToken(tokenID, why)
	}
	return 0
}
