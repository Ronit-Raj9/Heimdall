package engine

import (
	"context"

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
