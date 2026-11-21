package main

import (
	"context"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X23 Maxim integration: when an org has connected Maxim with automatic
// sending on, every run that seals is sent to its log repository
// (handlers.MaximPushSealed). The hook remembers each org's services as
// they are built, and the launcher calls maximOnSeal from its seal event.

type maximOrg struct {
	svc  handlers.Services
	root string
}

var (
	maximOrgsMu sync.Mutex
	maximOrgs   = map[string]maximOrg{}
)

const maximSealTimeout = 2 * time.Minute

func init() {
	serviceHooks = append(serviceHooks, rememberMaximOrg)
}

// rememberMaximOrg keeps the org's services for the seal hook.
func rememberMaximOrg(a *app, s *handlers.Services) {
	if a == nil || a.cfg == nil || a.cfg.Root == "" {
		return
	}
	maximOrgsMu.Lock()
	maximOrgs[s.OrgID] = maximOrg{svc: *s, root: a.cfg.Root}
	maximOrgsMu.Unlock()
}

// maximOnSeal sends a sealed run in the background. It never blocks the
// run; a failed send is recorded in the org's push log, where the
// Integrations page shows it.
// The launcher rebuilds its read index before it calls this, so the run is
// readable.
func maximOnSeal(orgID, runID string, logger schemas.Logger) {
	maximOrgsMu.Lock()
	o, ok := maximOrgs[orgID]
	maximOrgsMu.Unlock()
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), maximSealTimeout)
		defer cancel()
		if _, err := handlers.MaximPushSealed(ctx, o.svc, o.root, runID, true); err != nil && logger != nil {
			logger.Warn("maxim: send sealed run failed", "org", orgID, "run", runID, "error", err.Error())
		}
	}()
}
