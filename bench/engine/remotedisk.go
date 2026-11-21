package engine

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// NoteDisk records the free disk a worker reported with its lease request
// (E6, runner.disk.min_free_gb). A worker whose disk is low asks for no work;
// the record keeps it seen (not lost) and shows why it is idle in the
// workers list.
func (c *Coordinator) NoteDisk(ctx context.Context, workerID string, d schemas.WorkerDisk) error {
	scope, err := c.callScope(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	w, err := c.workerLocked(workerID, scope)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	was := w.info.Disk
	disk := d
	w.info.Disk = &disk
	if d.Low {
		w.info.LastSeen = c.now().UTC()
		if w.info.State == schemas.WorkerLost {
			w.info.State = stateOf(w)
		}
	}
	changed := was == nil || was.Low != d.Low
	c.mu.Unlock()
	if changed && d.Low {
		c.logger.Warn("runner disk is low; it takes no work until space is freed", "worker", workerID,
			"free_bytes", d.FreeBytes, "min_free_bytes", d.MinFreeBytes)
	}
	if c.hosted() && d.Low {
		c.book(ctx, w.scope, c.infoOf(w), false)
	}
	return nil
}

// countRunLocked counts one uploaded run for /metrics. The caller holds mu.
func (c *Coordinator) countRunLocked(reason string, outcome schemas.Outcome) {
	if c.runs == nil {
		c.runs = map[string]int{}
	}
	key := "rejected"
	if reason == "" {
		key = "accepted/" + string(outcome)
		if outcome == "" {
			key = "accepted/unknown"
		}
	}
	c.runs[key]++
}

// CoordinatorStats is a coordinator's live state for /metrics.
type CoordinatorStats struct {
	Leases         int            // slot and job leases out on workers
	OldestBeat     time.Duration  // longest time since a slot lease's last heartbeat; 0 without leases
	Workers        map[string]int // workers by state (idle, running, lost)
	WorkersDiskLow int            // workers whose last report said their disk is low
	Runs           map[string]int // uploaded runs: "accepted/<outcome>" or "rejected"
	Attached       int            // experiments attached for remote slots
}

// Stats reads the coordinator's live state.
func (c *Coordinator) Stats() CoordinatorStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := CoordinatorStats{Leases: len(c.leases), Workers: map[string]int{}, Runs: map[string]int{}, Attached: len(c.exps)}
	now := c.now()
	for _, l := range c.leases {
		if age := now.Sub(l.lastBeat); age > st.OldestBeat {
			st.OldestBeat = age
		}
	}
	for _, w := range c.workers {
		st.Workers[w.info.State]++
		if w.info.Disk != nil && w.info.Disk.Low {
			st.WorkersDiskLow++
		}
	}
	for k, v := range c.runs {
		st.Runs[k] = v
	}
	return st
}

// Coordinators lists the hub's coordinators, one per organization built.
func (h *RemoteHub) Coordinators() []*Coordinator {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Coordinator, 0, len(h.byOrg))
	for _, c := range h.byOrg {
		out = append(out, c)
	}
	return out
}
