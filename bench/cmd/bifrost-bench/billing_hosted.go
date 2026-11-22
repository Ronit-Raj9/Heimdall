package main

// Plans, metering and billing (lane Y16), hosted wiring: the billing service
// over the database, its platform routes, the dispatcher's plan gate, the
// shared worker minutes meter fed by the coordinator, the shared worker
// minutes check at lease and launch, and the hourly seat sync. Billing is
// off unless HEIMDALL_BILLING=on: then only metering runs and every limit is
// shown, never enforced. Local mode never reaches this file.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// billingEnvs holds the billing service of each hosted environment.
var billingEnvs sync.Map // *hostedEnv -> *billing.Service

// billingConfig reads the billing configuration; tests replace it.
var billingConfig = billing.ConfigFromEnv

// billingOf returns env's billing service, building it on first use; nil
// without a database.
func billingOf(env *hostedEnv) *billing.Service {
	if env == nil || env.db == nil {
		return nil
	}
	if v, ok := billingEnvs.Load(env); ok {
		return v.(*billing.Service)
	}
	cfg := billingConfig()
	svc := &billing.Service{Cfg: cfg, Store: billing.NewStore(env.db), Stripe: billing.NewStripe(cfg),
		Orgs: billingOrgs{env: env}, Data: &billingData{env: env}}
	v, loaded := billingEnvs.LoadOrStore(env, svc)
	if !loaded {
		startSeatSync(env, v.(*billing.Service))
	}
	return v.(*billing.Service)
}

// billingOrgs reads orgs, members and invitations from the database.
type billingOrgs struct{ env *hostedEnv }

func (o billingOrgs) OrgName(ctx context.Context, orgID string) (string, error) {
	org, err := o.env.db.OrgByID(ctx, orgID)
	if err != nil {
		return "", err
	}
	return org.Name, nil
}

func (o billingOrgs) Seats(ctx context.Context, orgID string) (int, int, error) {
	ms, err := o.env.db.Members(ctx, orgID)
	if err != nil {
		return 0, 0, err
	}
	var invited int
	err = o.env.db.QueryRow(ctx, `SELECT COUNT(*) FROM invitations WHERE org_id = ? AND accepted_at IS NULL AND expires_at > ?`,
		orgID, time.Now().UnixMilli()).Scan(&invited)
	if err != nil {
		return 0, 0, err
	}
	return len(ms), invited, nil
}

func (o billingOrgs) OwnerEmail(ctx context.Context, orgID string) (string, error) {
	ms, err := o.env.db.Members(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, m := range ms {
		if m.Role == "owner" && !m.Disabled {
			return m.Email, nil
		}
	}
	return "", nil
}

// billingData derives runs, evaluations and storage from the org's data:
// runs from the database results index, evaluations from the org's
// evaluation files, storage by walking the org's folders (cached).
type billingData struct {
	env *hostedEnv

	mu      sync.Mutex
	storage map[string]storageReading
}

type storageReading struct {
	bytes int64
	at    time.Time
}

// storageCacheFor is how long a storage reading is reused.
const storageCacheFor = 10 * time.Minute

func (d *billingData) RunsByDay(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error) {
	end := to.AddDate(0, 0, 1)
	rows, err := d.env.db.Query(ctx, `SELECT started_at FROM idx_runs WHERE org_id = ? AND started_at >= ? AND started_at < ?`,
		orgID, from.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		out[time.UnixMilli(at).UTC().Format(billing.DayLayout)]++
	}
	return out, rows.Err()
}

func (d *billingData) EvaluationsByDay(ctx context.Context, orgID string, from, to time.Time) (map[string]int, error) {
	p, err := d.env.layout.Paths(orgID)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	if _, err := os.Stat(evalstore.Dir(p.ProjectRoot)); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	list, err := evalstore.Open(p.ProjectRoot).List()
	if err != nil {
		return nil, err
	}
	end := to.AddDate(0, 0, 1)
	for _, ev := range list {
		at := ev.CreatedAt.UTC()
		if at.Before(from) || !at.Before(end) {
			continue
		}
		out[at.Format(billing.DayLayout)]++
	}
	return out, nil
}

func (d *billingData) StorageBytes(ctx context.Context, orgID string) (int64, error) {
	d.mu.Lock()
	if r, ok := d.storage[orgID]; ok && time.Since(r.at) < storageCacheFor {
		d.mu.Unlock()
		return r.bytes, nil
	}
	d.mu.Unlock()
	p, err := d.env.layout.Paths(orgID)
	if err != nil {
		return 0, err
	}
	var total int64
	seen := map[string]bool{}
	for _, root := range []string{p.StoreRoot, p.ProjectRoot, p.ReleasesRoot} {
		if root == "" || seen[root] || nested(root, seen) {
			continue
		}
		seen[root] = true
		n, err := dirBytes(ctx, root)
		if err != nil {
			return 0, err
		}
		total += n
	}
	d.mu.Lock()
	if d.storage == nil {
		d.storage = map[string]storageReading{}
	}
	d.storage[orgID] = storageReading{bytes: total, at: time.Now()}
	d.mu.Unlock()
	return total, nil
}

// nested reports whether root is inside a folder already counted.
func nested(root string, seen map[string]bool) bool {
	for s := range seen {
		if rel, err := filepath.Rel(s, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// dirBytes sums the sizes of the regular files under root; a missing root
// is 0.
func dirBytes(ctx context.Context, root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n, err
}

// billingMeter feeds shared worker lease time into the billing meters.
type billingMeter struct{ env *hostedEnv }

// LeaseEnded implements engine.LeaseMeter: only leases of the shared
// worker are metered (an org's own worker costs Heimdall nothing).
func (m billingMeter) LeaseEnded(u engine.LeaseUsage) {
	if !u.Shared() {
		return
	}
	svc := billingOf(m.env)
	if svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.RecordLease(ctx, u.OrgID, u.Start, u.End, u.Slots); err != nil {
		m.env.base.logger.Warn("billing: shared worker minutes not recorded", "org", u.OrgID, "error", err.Error())
	}
}

// sharedMinutesGate refuses shared worker leases of an org that used its
// plan's shared worker minutes this month (billing on only).
func sharedMinutesGate(env *hostedEnv, orgID string) func(context.Context) error {
	svc := billingOf(env)
	if svc == nil {
		return nil
	}
	return func(ctx context.Context) error { return svc.CheckSharedMinutes(ctx, orgID) }
}

// withSharedMinutes adds the shared worker minutes check to an org's launch
// caps: a launch is refused only while the org's runs go to the shared
// worker (not to its own worker).
func withSharedMinutes(env *hostedEnv, orgID string, caps func(context.Context) error) func(context.Context) error {
	svc := billingOf(env)
	if svc == nil {
		return caps
	}
	return func(ctx context.Context) error {
		if caps != nil {
			if err := caps(ctx); err != nil {
				return err
			}
		}
		if !svc.Enforced() {
			return nil
		}
		if pool, err := workerTargetsOf(env).EvaluationPool(ctx, orgID, ""); err == nil && pool != "" {
			return nil // the org's own worker runs it
		}
		return svc.CheckSharedMinutes(ctx, orgID)
	}
}

// seatSyncEvery is how often paid seats follow the member count.
const seatSyncEvery = time.Hour

var seatSyncs sync.Map // *hostedEnv -> struct{}

// startSeatSync syncs every Team org's paid seats each hour while billing
// and Stripe are configured. It stops with env.ctx.
func startSeatSync(env *hostedEnv, svc *billing.Service) {
	if env.ctx == nil || svc.Cfg.StripeReady() != nil {
		return
	}
	if _, loaded := seatSyncs.LoadOrStore(env, struct{}{}); loaded {
		return
	}
	go func() {
		defer seatSyncs.Delete(env)
		tk := time.NewTicker(seatSyncEvery)
		defer tk.Stop()
		for {
			select {
			case <-env.ctx.Done():
				return
			case <-tk.C:
				ctx, cancel := context.WithTimeout(env.ctx, 5*time.Minute)
				if _, err := svc.SyncAllSeats(ctx); err != nil {
					env.base.logger.Warn("billing: seat sync", "error", err.Error())
				}
				cancel()
			}
		}
	}()
}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		svc := billingOf(env)
		if svc != nil {
			tenancy.SetRouteGate(handlers.PlanGate(svc))
			if svc.Cfg.Enabled {
				env.base.logger.Info("billing is on: plan limits are enforced", "stripe_mode", svc.Cfg.Mode())
			}
		}
		return handlers.NewBillingHandler(env.base.cfg.Server.Mode, svc, env.base.cfg.Auth.PublicURL, runnerAudit(env)), nil
	})
}
