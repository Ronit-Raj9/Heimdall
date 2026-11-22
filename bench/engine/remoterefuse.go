package engine

import (
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X3: a refused lease is not final. A worker refuses an experiment when its
// project does not reproduce the manifest yet (a benchmark added after its
// last project download) or when a check failed for a passing reason (a
// docker image inspect killed on a busy host). The coordinator offers the
// experiment to that worker again after refuseRetry, and the worker checks it
// again after the same time. After maxRefusals refusals by every worker that
// could take it, the queued slots fail with the reason, so the job ends with
// it instead of waiting forever.
const (
	refuseRetry = 60 * time.Second
	maxRefusals = 3
)

// leaseRefusal is one worker's refusals of one experiment: the last reason,
// when it came, and how many there were since the worker registered.
type leaseRefusal struct {
	reason string
	at     time.Time
	n      int
}

// refusedLocked reports whether r is kept from w for now: within refuseRetry
// of its last refusal, or for good after maxRefusals (until the worker
// registers again). The caller holds c.mu.
func (c *Coordinator) refusedLocked(w *remoteWorker, r *schedRun) bool {
	f := w.refused[r.m.ExpID]
	if f == nil {
		return false
	}
	return f.n >= maxRefusals || c.now().Sub(f.at) < refuseRetry
}

// noteRefusalLocked records a refusal of l's experiment by its worker and
// returns the job log line for it. The caller holds c.mu.
func (c *Coordinator) noteRefusalLocked(l *remoteLease, reason string) string {
	w := c.workers[l.worker]
	if w == nil {
		return ""
	}
	f := w.refused[l.r.m.ExpID]
	if f == nil {
		f = &leaseRefusal{}
		w.refused[l.r.m.ExpID] = f
	}
	f.reason, f.at = reason, c.now()
	f.n++
	if f.n >= maxRefusals {
		return fmt.Sprintf("worker %s could not take this run: %s. It refused it %d times, so Heimdall stops offering it to this worker.", l.worker, reason, f.n)
	}
	return fmt.Sprintf("worker %s could not take this run: %s. Heimdall offers it again in 1 minute.", l.worker, reason)
}

// everyWorkerRefusedLocked reports whether no worker is left for r: every
// registered worker that is not lost and serves r's pool refused it
// maxRefusals times, and no lease of r is out. The caller holds c.mu.
func (c *Coordinator) everyWorkerRefusedLocked(r *schedRun) bool {
	for _, l := range c.leases {
		if l.r == r {
			return false
		}
	}
	any := false
	for _, id := range c.order {
		w := c.workers[id]
		if w == nil || w.info.State == schemas.WorkerLost || !c.openTo(w, r) {
			continue
		}
		f := w.refused[r.m.ExpID]
		if f == nil || f.n < maxRefusals {
			return false
		}
		any = true
	}
	return any
}

// failRefused ends every queued slot of r with the last refusal reason.
func (r *schedRun) failRefused(reason string) {
	err := fmt.Errorf("every worker refused this run %d times: %s", maxRefusals, reason)
	for _, slot := range r.queue.takeAll() {
		r.slotError(slot, err)
	}
}

// refusalFresh reports whether a worker's cached refusal may still be
// reused; after refuseRetry the experiment is checked again.
func (w *Worker) refusalFresh(e *workerExp) bool {
	return w.now().Sub(e.refusedAt) < refuseRetry
}
