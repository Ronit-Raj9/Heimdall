package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// budgetGateway is the fake gateway with a gateway-side evaluation budget
// that reports used up after usedAfter reads.
type budgetGateway struct {
	*fakeGateway
	mu        sync.Mutex
	opened    []string
	usd       float64
	rpm       int
	reads     int
	usedAfter int
	closed    []string
}

func (g *budgetGateway) OpenEvaluationBudget(_ context.Context, evalID string, usd float64, rpm int) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opened, g.usd, g.rpm = append(g.opened, evalID), usd, rpm
	return "cust-" + evalID, nil
}

func (g *budgetGateway) EvaluationBudgetSpent(context.Context, string) (float64, float64, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	used := g.usedAfter > 0 && g.reads >= g.usedAfter
	return float64(g.reads), g.usd, used, nil
}

func (g *budgetGateway) CloseEvaluationBudget(_ context.Context, evalID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = append(g.closed, evalID)
	return nil
}

func TestSchedulerGatewayBudgetStop(t *testing.T) {
	b := newBench(t)
	gw := &budgetGateway{fakeGateway: b.gw, usedAfter: 2}
	b.reg.Gateway = gw
	e := testExperiment()
	e.Budget.TotalUSD = 50
	e.Budget.RequestsPerMinute = 120
	m := b.plan(t, e)
	events := make(chan Event, 256)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.opened) != 1 || gw.opened[0] != m.ExpID || gw.usd != 50 || gw.rpm != 120 {
		t.Fatalf("opened %v usd %v rpm %v", gw.opened, gw.usd, gw.rpm)
	}
	if len(gw.closed) != 1 || gw.closed[0] != m.ExpID {
		t.Fatalf("closed %v", gw.closed)
	}
	if !sum.GatewayBudgetStopped || sum.StopReason != gatewayBudgetStopMessage || sum.GatewayBudgetID != "cust-"+m.ExpID {
		t.Fatalf("summary %+v", sum)
	}
	if sum.Sealed != 2 || sum.Pending != 2 || b.w.count("exec") != 2 {
		t.Fatalf("ran past the Bifrost budget: sealed %d pending %d exec %d", sum.Sealed, sum.Pending, b.w.count("exec"))
	}
	close(events)
	var stop string
	for ev := range events {
		if ev.Kind == EventBudgetStop {
			if stop != "" {
				t.Fatalf("two budget stop events: %q, %q", stop, ev.Message)
			}
			stop = ev.Message
		}
	}
	if !strings.HasPrefix(stop, "stopped by Bifrost budget") {
		t.Fatalf("budget stop event %q", stop)
	}
}

// Without a spend cap the gateway budget is never opened.
func TestSchedulerGatewayBudgetNeedsCap(t *testing.T) {
	b := newBench(t)
	gw := &budgetGateway{fakeGateway: b.gw}
	b.reg.Gateway = gw
	e := testExperiment()
	e.Budget.TotalUSD = 0
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.opened) != 0 || sum.GatewayBudgetStopped || sum.Sealed != 4 {
		t.Fatalf("opened %v summary %+v", gw.opened, sum)
	}
}

// C06 after GPU runs: a GPU worker's sealed run is followed by the same
// gateway budget check, so GPU slots stop once Bifrost reports used up.
func TestSchedulerGatewayBudgetStopsGPURuns(t *testing.T) {
	b := newBench(t)
	for i := range b.suite.Tasks {
		b.suite.Tasks[i].Resources = &schemas.Resources{GPU: &schemas.GPURequest{Count: 1}}
	}
	b.reg.GPUs = gtxPool(t)
	gw := &budgetGateway{fakeGateway: b.gw, usedAfter: 1}
	b.reg.Gateway = gw
	e := testExperiment()
	e.Budget.TotalUSD = 50
	m := b.plan(t, e)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.GatewayBudgetStopped || sum.Sealed >= len(m.Slots) {
		t.Fatalf("GPU runs went past the Bifrost budget: %+v", sum)
	}
}

// C06 after remote runs: an upload the coordinator accepts is followed by
// the gateway budget check, so no more leases go out once it is used up.
func TestRemoteGatewayBudgetStop(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	gw := &budgetGateway{fakeGateway: rw.coord.b.gw, usedAfter: 1}
	rw.r.mu.Lock()
	rw.r.evalBudget = gw
	rw.r.mu.Unlock()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	if res := w.Work(context.Background(), l); res == nil || !res.Accepted {
		t.Fatalf("run: %+v", res)
	}
	if l2 := lease(t, w); l2 != nil {
		t.Fatalf("lease %s handed out after the Bifrost budget was used up", l2.LeaseID)
	}
	if sum := done(); !sum.GatewayBudgetStopped || sum.StopReason != gatewayBudgetStopMessage || sum.Remote != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestBudgetRefusedCalls(t *testing.T) {
	log := &schemas.RequestLog{Requests: []schemas.GatewayRequest{{ID: "a", BudgetRefused: true}, {ID: "b"}, {ID: "c", BudgetRefused: true}}}
	if n := budgetRefusedCalls(log); n != 2 {
		t.Fatalf("refused %d, want 2", n)
	}
	if budgetRefusedCalls(nil) != 0 {
		t.Fatal("nil log")
	}
}
