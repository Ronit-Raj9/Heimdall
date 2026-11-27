package main

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Worker status (fix wave M3-21): whether the worker an evaluation waits
// for is online, from the same runner rows and freshness as the worker
// slots (servicehook_workerslots.go). Hosted only.
func init() {
	serviceHooks = append(serviceHooks, func(_ *app, s *handlers.Services) {
		if !s.Hosted {
			return
		}
		p := hostedWorkerRunners.Load()
		if p == nil {
			return
		}
		l, org := *p, s.OrgID
		s.WorkerStatus = func(ctx context.Context, target string) (handlers.WorkerStatus, error) {
			pool, ok := workerPoolOf(target)
			if !ok {
				return handlers.WorkerStatus{}, nil
			}
			rs, err := l.ListRunners(ctx, org)
			if err != nil {
				return handlers.WorkerStatus{}, err
			}
			return poolStatus(rs, pool, time.Now()), nil
		}
	})
}

// poolStatus reads pool's runners: online when one is live or was seen
// within workerSlotsFresh; LastSeen the latest heartbeat.
func poolStatus(rs []schemas.RunnerView, pool string, now time.Time) handlers.WorkerStatus {
	var out handlers.WorkerStatus
	for _, r := range rs {
		if r.Pool != pool {
			continue
		}
		out.Known = true
		if r.Live || (!r.LastSeen.IsZero() && now.Sub(r.LastSeen) <= workerSlotsFresh) {
			out.Online = true
		}
		if !r.LastSeen.IsZero() && (out.LastSeen == nil || r.LastSeen.After(*out.LastSeen)) {
			t := r.LastSeen.UTC()
			out.LastSeen = &t
		}
	}
	return out
}
