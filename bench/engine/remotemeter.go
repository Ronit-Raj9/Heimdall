package engine

import (
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// LeaseMeter is told how long each lease of a hosted runner held a slot
// (lane Y16, billing): the shared worker's minutes are what the platform
// pays for. It is called on its own goroutine, outside the coordinator's
// lock, once per lease; its errors are its own to log.
type LeaseMeter interface {
	LeaseEnded(u LeaseUsage)
}

// LeaseUsage is one ended lease.
type LeaseUsage struct {
	OrgID  string
	Pool   string // the runner token's pool (schemas.SharedRunnerPool for the shared worker)
	Worker string
	Kind   string // run | job
	Start  time.Time
	End    time.Time
	Slots  int
}

// Shared reports whether the lease ran on the shared worker.
func (u LeaseUsage) Shared() bool { return u.Pool == schemas.SharedRunnerPool }

// meterLocked hands an ended lease of worker w to the meter. Local
// coordinators and leases with no start are not metered. The caller holds
// mu.
func (c *Coordinator) meterLocked(w *remoteWorker, start time.Time, slots int, kind string) {
	if c.opts.Meter == nil || !c.hosted() || w == nil || start.IsZero() {
		return
	}
	u := LeaseUsage{OrgID: c.opts.OrgID, Pool: w.scope.Pool, Worker: w.info.ID, Kind: kind, Start: start, End: c.now(), Slots: slots}
	go c.opts.Meter.LeaseEnded(u)
}
