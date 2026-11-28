package handlers

import (
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// WorkerStatus is Services.WorkerStatus's answer.
type WorkerStatus struct {
	Known    bool       // some runner row of the pool exists
	Online   bool       // a runner of the pool is live or was seen recently
	LastSeen *time.Time // the latest heartbeat of the pool's runners
}

// EvaluationQueue is the queue part of GET /evaluations/{id} for a queued
// evaluation (fix wave M3-21). Ahead counts this organization's queued
// evaluations created before it; the order across organizations is not
// fixed (fair queueing by running jobs), so no global position is given.
type EvaluationQueue struct {
	Ahead          int        `json:"ahead"`
	WorkerOnline   *bool      `json:"worker_online,omitempty"`
	WorkerLastSeen *time.Time `json:"worker_last_seen,omitempty"`
}

// evaluationWithQueue is GET /evaluations/{id}: the evaluation, plus Queue
// while it waits.
type evaluationWithQueue struct {
	*schemas.Evaluation
	Queue *EvaluationQueue `json:"queue,omitempty"`
}

func (h *evaluationsHandler) withQueue(ctx *fasthttp.RequestCtx, ev *schemas.Evaluation) any {
	if ev == nil || !waitsForWorker(ev) {
		return ev
	}
	q := &EvaluationQueue{}
	if evs, err := h.store().List(); err == nil {
		q.Ahead = queuedAhead(ev, evs, h.env.Services.OrgID)
	}
	if f := h.env.Services.WorkerStatus; f != nil {
		work, cancel := workContext(ctx, readTimeout)
		ws, err := f(work, workerTargetOf(ev))
		cancel()
		// No runner row at all is "no worker connected" too (N2: an hour of
		// silence on the live site came from an org whose worker never
		// connected, so Known was false and nothing was said).
		if err == nil {
			online := ws.Known && ws.Online
			q.WorkerOnline, q.WorkerLastSeen = &online, ws.LastSeen
		}
	}
	return evaluationWithQueue{Evaluation: ev, Queue: q}
}

// waitsForWorker: queued, or running with no benchmark that has spent
// anything yet (no run reached the model, so it may still wait for a worker
// slot). The page shows the queue facts only in that state (N2).
func waitsForWorker(ev *schemas.Evaluation) bool {
	switch ev.Status {
	case schemas.EvaluationQueued:
		return true
	case schemas.EvaluationRunning:
		for _, c := range ev.Children {
			if c.SpentUSD > 0 {
				return false
			}
		}
		return true
	}
	return false
}

// queuedAhead counts the org's queued evaluations created before ev.
func queuedAhead(ev *schemas.Evaluation, evs []*schemas.Evaluation, org string) int {
	n := 0
	for _, o := range evs {
		if o == nil || o.ID == ev.ID || o.Status != schemas.EvaluationQueued {
			continue
		}
		if org != "" && o.Org != "" && o.Org != org {
			continue
		}
		if o.CreatedAt.Before(ev.CreatedAt) {
			n++
		}
	}
	return n
}

// workerTargetOf is the worker target of ev as Services.WorkerSlots reads
// it: its pool, or the shared worker.
func workerTargetOf(ev *schemas.Evaluation) string {
	if ev.Spec.Worker == "" {
		return schemas.WorkerTargetShared
	}
	return ev.Spec.Worker
}
