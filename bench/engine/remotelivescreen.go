package engine

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LiveLease checks that leaseID is a live slot lease held by workerID, for
// a worker's live screen frame (Z03, livescreen.go), and reports whether
// the lease's experiment is a debug run: only a debug run may be taken
// over. The answer comes from the coordinator's own plan, never from the
// worker.
func (c *Coordinator) LiveLease(ctx context.Context, leaseID, workerID string) (debug bool, err error) {
	scope, err := c.callScope(ctx)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.leaseLocked(leaseID, workerID, scope)
	if err != nil {
		return false, err
	}
	if l.closing {
		return false, fmt.Errorf("lease %s is ending: %w", leaseID, schemas.ErrLeaseGone)
	}
	return l.r != nil && l.r.m != nil && l.r.m.Experiment.Debug != nil, nil
}
