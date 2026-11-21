package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

type leaseAPI struct {
	handlers.WorkerAPI
	leases int
}

func (l *leaseAPI) OrgID() string { return "org1" }
func (l *leaseAPI) Lease(context.Context, string) (*schemas.WorkerLease, error) {
	l.leases++
	return &schemas.WorkerLease{}, nil
}

type killQuota struct {
	quota.Store
	on bool
}

func (k *killQuota) KillSwitch(context.Context) (bool, error) { return k.on, nil }

func TestKillSwitchBlocksLeases(t *testing.T) {
	inner := &leaseAPI{}
	q := &killQuota{Store: quota.NewStub()}
	api := gateWorkers(inner, q, nil)
	if h, ok := api.(handlers.HostedWorkerAPI); !ok || h.OrgID() != "org1" {
		t.Fatal("gate dropped the hosted worker API")
	}
	if l, _ := api.Lease(context.Background(), "w"); l == nil || inner.leases != 1 {
		t.Fatal("lease blocked with kill switch off")
	}
	q.on = true
	if l, _ := api.Lease(context.Background(), "w"); l != nil || inner.leases != 1 {
		t.Fatal("lease handed out with kill switch on")
	}
}

func TestReconcileShared(t *testing.T) {
	loops := map[string]*sharedLoop{}
	started := 0
	start := func(org, token string, slots int) *sharedLoop {
		started++
		d := make(chan struct{})
		l := &sharedLoop{token: token, slots: slots, done: d}
		l.cancel = func() { close(d) }
		return l
	}
	as := &sharedpool.Assignments{Assignments: []sharedpool.Assignment{{OrgID: "a", Token: "t1", MaxConcurrentRuns: 1}, {OrgID: "b", Token: "t2"}}}
	reconcileShared(loops, as, 2, start)
	if len(loops) != 2 || started != 2 || loops["a"].slots != 1 || loops["b"].slots != 2 {
		t.Fatalf("start: %v", loops)
	}
	reconcileShared(loops, as, 2, start)
	if started != 2 {
		t.Fatal("restarted unchanged loops")
	}
	reconcileShared(loops, &sharedpool.Assignments{KillSwitch: true}, 2, start)
	if len(loops) != 0 {
		t.Fatal("kill switch kept loops")
	}
}

// fixedQuota returns fixed limits.
type fixedQuota struct {
	quota.Store
	l quota.Limits
}

func (f fixedQuota) Get(context.Context, string) (quota.Limits, error) { return f.l, nil }
func (fixedQuota) KillSwitch(context.Context) (bool, error)            { return false, nil }

// TestDailyCapsGateLeases covers X01: caps are off by default; once a server
// admin sets max_runs_per_day, a capped org gets no new lease and its start is
// refused, and leases resume when the cap is raised.
func TestDailyCapsGateLeases(t *testing.T) {
	if quota.DefaultLimits.MaxRunsPerDay != 0 || quota.DefaultLimits.MaxSpendUSDPerDay != 0 {
		t.Fatalf("caps must be off by default: %+v", quota.DefaultLimits)
	}
	inner := &leaseAPI{}
	q := &fixedQuota{Store: quota.NewStub(), l: quota.Limits{MaxRunsPerDay: 0}}
	runsToday := 3
	caps := func(ctx context.Context) error {
		l, _ := q.Get(ctx, "org1")
		return handlers.DailyCapRefusal(l.MaxRunsPerDay, l.MaxSpendUSDPerDay, runsToday, 0)
	}
	api := gateWorkers(inner, q, caps)
	if l, _ := api.Lease(context.Background(), "w"); l == nil {
		t.Fatal("caps off: lease blocked")
	}
	q.l.MaxRunsPerDay = 3
	if l, _ := api.Lease(context.Background(), "w"); l != nil || inner.leases != 1 {
		t.Fatal("lease handed out at the daily run cap")
	}
	if err := caps(context.Background()); !errors.Is(err, handlers.ErrDailyCap) {
		t.Fatalf("start at the cap: %v", err)
	}
	q.l.MaxRunsPerDay = 10
	if l, _ := api.Lease(context.Background(), "w"); l == nil {
		t.Fatal("raised cap: lease still blocked")
	}
}

// TestDailyCapsOverIndex checks dailyCaps reads the quota and is off at 0.
func TestDailyCapsOverIndex(t *testing.T) {
	q := &fixedQuota{Store: quota.NewStub()}
	check := dailyCaps(q, "org1", nil, time.Now)
	if err := check(context.Background()); err != nil {
		t.Fatalf("caps off: %v", err)
	}
	q.l.MaxRunsPerDay = 1
	if err := dailyCaps(q, "org1", nil, time.Now)(context.Background()); err != nil {
		t.Fatalf("no runs today with a cap of 1: %v", err)
	}
}
