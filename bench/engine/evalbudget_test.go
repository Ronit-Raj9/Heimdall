package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
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
