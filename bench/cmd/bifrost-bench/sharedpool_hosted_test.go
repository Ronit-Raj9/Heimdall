package main

import (
	"context"
	"testing"

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
	api := gateWorkers(inner, q)
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
