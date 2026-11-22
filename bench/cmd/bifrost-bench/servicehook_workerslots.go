package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
)

// Worker slots (N90, N93; fix wave 2 decision D6): the one source of how
// many runs the worker an evaluation goes to runs at once. It reads the
// org's runner rows of the target's pool: the sum of the slots of the
// runners seen within workerSlotsFresh, else (every heartbeat is older)
// the largest slots any of them registered, so a stale row still gives a
// number. Local mode has no runner rows and leaves Services.WorkerSlots nil.

// workerSlotsFresh is how recently a runner must have been seen to count.
const workerSlotsFresh = 10 * time.Minute

// runnerLister is the runners area as the slot lookup uses it
// (runners.Service).
type runnerLister interface {
	ListRunners(ctx context.Context, orgID string) ([]schemas.RunnerView, error)
}

// hostedWorkerRunners is the hosted server's runners area, set by the
// hosted area below; nil in local mode.
var hostedWorkerRunners atomic.Pointer[runnerLister]

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env.db != nil {
			var l runnerLister = runnersOf(env).svc
			hostedWorkerRunners.Store(&l)
		}
		return nil, nil
	})
	serviceHooks = append(serviceHooks, func(_ *app, s *handlers.Services) {
		if !s.Hosted {
			return
		}
		p := hostedWorkerRunners.Load()
		if p == nil {
			return
		}
		l, org := *p, s.OrgID
		s.WorkerSlots = func(ctx context.Context, target string) (int, error) {
			return workerSlotsFor(ctx, l, org, target, time.Now())
		}
	})
}

// workerPoolOf is the runner pool a target runs on: "" and "shared" the
// shared worker's pool, "worker:<pool>" (or a bare pool name) that pool;
// ok is false for a sandbox platform, whose runs are not bounded by a
// runner's slots.
func workerPoolOf(target string) (pool string, ok bool) {
	kind, name, err := schemas.ParseWorkerTarget(target)
	if err != nil { // a bare pool name: one of the org's own workers
		return target, true
	}
	switch kind {
	case schemas.WorkerTargetShared:
		return schemas.SharedRunnerPool, true
	case schemas.WorkerTargetWorker:
		return name, true
	}
	return "", false
}

// workerSlotsFor is how many runs the org's worker for target runs at once
// (0: not known or not bounded).
func workerSlotsFor(ctx context.Context, l runnerLister, org, target string, now time.Time) (int, error) {
	pool, ok := workerPoolOf(target)
	if !ok || l == nil {
		return 0, nil
	}
	rs, err := l.ListRunners(ctx, org)
	if err != nil {
		return 0, err
	}
	return poolSlots(rs, pool, now), nil
}

// poolSlots is the sum of the slots of pool's runners seen within
// workerSlotsFresh, else the largest slots any of them registered.
func poolSlots(rs []schemas.RunnerView, pool string, now time.Time) int {
	fresh, largest := 0, 0
	for _, r := range rs {
		if r.Pool != pool || r.Slots <= 0 {
			continue
		}
		largest = max(largest, r.Slots)
		if r.Live || now.Sub(r.LastSeen) <= workerSlotsFresh {
			fresh += r.Slots
		}
	}
	if fresh > 0 {
		return fresh
	}
	return largest
}
