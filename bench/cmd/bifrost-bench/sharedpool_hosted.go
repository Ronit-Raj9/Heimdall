package main

// Shared runner pool and guardrails (web parity lane L03, F3), hosted
// wiring: the quota store over the database, the shared pool service, its
// platform routes, and the coordinator gate that stops new leases while
// the kill switch is on. Local mode never reaches this file.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// sharedQuotas holds the quota store of each hosted environment.
var sharedQuotas sync.Map // *hostedEnv -> quota.Store

// lastHostedQuota is the newest hosted quota store, for the service hooks
// (they do not see the hosted environment; serve builds one).
var lastHostedQuota atomic.Value // quota.Store

// hostedQuotaDefaults are the limits of an org with no row: the shared pool
// is on for everyone when the server runs a shared runner.
func hostedQuotaDefaults() quota.Limits {
	l := quota.DefaultLimits
	l.SharedPool = sharedpool.Secret() != ""
	return l
}

// quotaOf returns env's quota store, building it on first use.
func quotaOf(env *hostedEnv) quota.Store {
	if v, ok := sharedQuotas.Load(env); ok {
		return v.(quota.Store)
	}
	var q quota.Store = quota.NewStub()
	if env.db != nil {
		def := hostedQuotaDefaults()
		q = quota.Open(env.db, quota.Options{Defaults: &def})
	}
	v, _ := sharedQuotas.LoadOrStore(env, q)
	lastHostedQuota.Store(v.(quota.Store))
	return v.(quota.Store)
}

// sharedPoolService builds the shared pool service of env, or nil when the
// server has no master key to keep the per-org runner tokens with.
func sharedPoolService(env *hostedEnv, q quota.Store, images *sharedpool.ImageStore) *sharedpool.Service {
	if env.db == nil {
		return nil
	}
	mk, err := keystore.MasterKey()
	if err != nil {
		if sharedpool.Secret() != "" {
			env.base.logger.Warn("shared runner pool is off: no master key for the per-org runner tokens", "error", err.Error())
		}
		return nil
	}
	keys, err := keystore.Open(env.db, mk)
	if err != nil {
		env.base.logger.Warn("shared runner pool is off: keystore", "error", err.Error())
		return nil
	}
	d := env.db
	return &sharedpool.Service{Quota: q, Keys: keys, Minter: runnersOf(env).svc, Images: images,
		Platform: platformAssignment(platformConnections(env)), // X19a: an org's own sandbox platform (workerplatforms_hosted.go)
		Orgs: func(ctx context.Context) ([]string, error) {
			rows, err := d.Query(ctx, `SELECT id FROM orgs ORDER BY id`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					return nil, err
				}
				out = append(out, id)
			}
			return out, rows.Err()
		}}
}

// gatedWorkers is an org's worker API that hands out no lease while the
// platform kill switch is on. Running leases keep their heartbeats and
// uploads, so slots already running finish.
type gatedWorkers struct {
	handlers.WorkerAPI
	q quota.Store
	// caps refuses new leases once the org reached a daily cap (X01); nil:
	// no cap check.
	caps func(ctx context.Context) error
	// shared refuses new shared worker leases once the org used its plan's
	// shared worker minutes (Y16, billing_hosted.go); nil: no check.
	shared func(ctx context.Context) error
}

// OrgID keeps the hosted worker API's runner token authentication.
func (g gatedWorkers) OrgID() string {
	if h, ok := g.WorkerAPI.(handlers.HostedWorkerAPI); ok {
		return h.OrgID()
	}
	return ""
}

func (g gatedWorkers) Lease(ctx context.Context, workerID string) (*schemas.WorkerLease, error) {
	if on, err := g.q.KillSwitch(ctx); err == nil && on {
		return nil, nil // no lease for now; the runner polls again
	}
	if g.caps != nil && g.caps(ctx) != nil {
		return nil, nil // X01: the org reached a daily cap; leases resume tomorrow or when the cap is raised
	}
	if g.shared != nil {
		if sc, ok := schemas.RunnerScopeFrom(ctx); ok && sc.Pool == schemas.SharedRunnerPool && g.shared(ctx) != nil {
			return nil, nil // Y16: the plan's shared worker minutes are used up; the Billing tab shows it
		}
	}
	return g.WorkerAPI.Lease(ctx, workerID)
}

// gateWorkers wraps api with the kill switch gate and the org's daily caps
// (caps may be nil).
func gateWorkers(api handlers.WorkerAPI, q quota.Store, caps func(context.Context) error) handlers.WorkerAPI {
	if api == nil || q == nil {
		return api
	}
	if _, hosted := api.(handlers.HostedWorkerAPI); !hosted {
		return api // the gate must not drop the runner token authentication
	}
	return gatedWorkers{WorkerAPI: api, q: q, caps: caps}
}

// dailyCaps is the org's daily cap check over its run index: today's sealed
// runs and spend against quota max_runs_per_day and max_spend_usd_per_day.
// Both caps are 0 (off) unless a server admin sets them. A quota read error
// leaves the caps unchecked: the caps are a spend guard, not a key check.
func dailyCaps(q quota.Store, orgID string, index handlers.ReadIndex, now func() time.Time) func(context.Context) error {
	return func(ctx context.Context) error {
		l, err := q.Get(ctx, orgID)
		if err != nil || (l.MaxRunsPerDay <= 0 && l.MaxSpendUSDPerDay <= 0) {
			return nil
		}
		runs, spent := handlers.UsageToday(index, now())
		return handlers.DailyCapRefusal(l.MaxRunsPerDay, l.MaxSpendUSDPerDay, runs, spent)
	}
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		q := quotaOf(env)
		var images *sharedpool.ImageStore
		if env.db != nil {
			images = &sharedpool.ImageStore{DB: env.db}
		}
		dir := tenancy.DBDirectory{DB: env.db}
		h := handlers.NewSharedPoolHandler(env.base.cfg.Server.Mode, q, images, sharedPoolService(env, q, images), runnerAudit(env))
		if env.db != nil {
			h = h.WithOrgCheck(func(ctx context.Context, id string) bool { _, err := dir.OrgByID(ctx, id); return err == nil })
		}
		return h, nil
	})
	// Wraps the jobs (or runners) area's launcher: same launcher, worker
	// API gated by the kill switch. This file sorts after runnerhosted.go
	// and servejobs.go, so their init has already set orgLauncher.
	if prev := orgLauncher; prev != nil {
		orgLauncher = func(env *hostedEnv, oa *app, ix *store.Index) (handlers.Launcher, handlers.WorkerAPI) {
			l, api := prev(env, oa, ix)
			q := quotaOf(env)
			var caps func(context.Context) error
			if ix != nil {
				caps = dailyCaps(q, oa.orgID, indexAdapter{ix: ix, root: oa.store.Root()}, time.Now)
			}
			launch := withSharedMinutes(env, oa.orgID, caps) // Y16: launches also check the plan's shared worker minutes (billing_hosted.go)
			if ll, ok := l.(*launcher); ok && launch != nil {
				ll.caps = launch
			}
			gated := gateWorkers(api, q, caps)
			if g, ok := gated.(gatedWorkers); ok {
				g.shared = sharedMinutesGate(env, oa.orgID)
				gated = g
			}
			return l, gated
		}
	}
	serviceHooks = append(serviceHooks, func(a *app, s *handlers.Services) {
		if !s.Hosted {
			return
		}
		if q, ok := lastHostedQuota.Load().(quota.Store); ok && q != nil {
			s.Quota = q
		}
	})
}
