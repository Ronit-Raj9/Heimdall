package main

// Deploy area: the checks behind GET /readyz (handlers/readiness.go,
// docs/hosting.md). /healthz needs none: it answers while the process serves.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
)

// readyChecks lists what the server needs before it takes traffic: the runs
// root writable in every mode and, in hosted mode, the database reachable
// with every migration applied.
func readyChecks(a *app) []handlers.ReadyCheck {
	checks := []handlers.ReadyCheck{handlers.RunsRootWritable(a.store.Root())}
	if a.cfg.Auth.IsHosted() {
		checks = append(checks, dbReadyChecks(&readyDB{cfg: a.cfg.DatabaseConfig()})...)
	}
	return checks
}

// readyDB is a one-connection pool of its own for the readiness checks,
// opened on first use and kept for the process. A probe never waits behind
// the server's busy pool, and the checks need no handle from the serve
// wiring.
type readyDB struct {
	cfg db.Config
	mu  sync.Mutex
	d   *db.DB
}

func (r *readyDB) get(ctx context.Context) (*db.DB, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.d != nil {
		return r.d, nil
	}
	cfg := r.cfg
	cfg.MaxOpenConns = 1
	d, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	r.d = d
	return d, nil
}

// dbReadyChecks are "database" (connect and ping) and "migrations" (every
// migration this binary registers is applied, none edited since). A
// migration applied by a newer binary and unknown to this one is allowed:
// migrations are additive, so a rolled-back server runs on that schema.
func dbReadyChecks(r *readyDB) []handlers.ReadyCheck {
	return []handlers.ReadyCheck{
		{Name: "database", Check: func(ctx context.Context) error {
			d, err := r.get(ctx)
			if err != nil {
				return err
			}
			return d.SQL().PingContext(ctx)
		}},
		{Name: "migrations", Check: func(ctx context.Context) error {
			d, err := r.get(ctx)
			if err != nil {
				return err
			}
			states, err := d.MigrationStatus(ctx)
			if err != nil {
				return err
			}
			return migrationsCurrent(states)
		}},
	}
}

// migrationsCurrent fails on a pending or edited migration and names them.
func migrationsCurrent(states []db.MigrationState) error {
	var pending, edited []string
	for _, s := range states {
		id := fmt.Sprintf("%s/%04d_%s", s.Set, s.Version, s.Name)
		switch {
		case !s.Applied:
			pending = append(pending, id)
		case s.Problem == "edited":
			edited = append(edited, id)
		}
	}
	var parts []string
	if len(pending) > 0 {
		parts = append(parts, fmt.Sprintf("%d pending (run `bifrost-bench db migrate`): %s", len(pending), strings.Join(pending, ", ")))
	}
	if len(edited) > 0 {
		parts = append(parts, fmt.Sprintf("%d edited after they were applied: %s", len(edited), strings.Join(edited, ", ")))
	}
	if len(parts) > 0 {
		return fmt.Errorf("migrations not current: %s", strings.Join(parts, "; "))
	}
	return nil
}
