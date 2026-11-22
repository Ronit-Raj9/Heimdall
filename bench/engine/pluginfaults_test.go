package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
)

type c04Proxy struct{ plans map[string]schemas.FaultPlan }

func (f *c04Proxy) SetPlan(_ context.Context, slot string, p schemas.FaultPlan) error {
	f.plans[slot] = p
	return nil
}
func (f *c04Proxy) ClearPlan(context.Context, string) error { return nil }
func (f *c04Proxy) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) {
	return nil, nil
}

func c04State(parser string) *runState {
	return &runState{rc: &schemas.RunContext{
		Run:        &schemas.Run{RunID: "run-7"},
		Key:        &schemas.RunKey{KeySlot: "slot-1"},
		Agent:      &schemas.AgentConfig{Parser: parser},
		Experiment: &schemas.Experiment{ID: "e_x"},
		Slot:       &schemas.Slot{Arm: "a"},
	}}
}

func TestRoutePluginFaults(t *testing.T) {
	ctx := context.Background()
	armed := schemas.FaultPlan{Mode: faultproxy.ModePersistent, FromCall: 1, Status: 503, RunID: "run-7"}

	// Claude Code sends ANTHROPIC_CUSTOM_HEADERS: the plan moves to the plugin.
	proxy := &c04Proxy{plans: map[string]schemas.FaultPlan{}}
	pf := bifrost.NewPluginFaults(proxy)
	p := &Pipeline{reg: &schemas.Registry{Fault: pf}}
	if err := pf.SetPlan(ctx, "slot-1", armed); err != nil {
		t.Fatal(err)
	}
	st := c04State(adapters.ParserClaudeCode)
	if err := p.routePluginFaults(ctx, st); err != nil {
		t.Fatal(err)
	}
	if st.rc.Run.Gateway.FaultPath != bifrost.FaultPathPlugin || st.rc.FaultHeaders[bifrost.HeaderFaultPlan] == "" ||
		st.rc.FaultHeaders[bifrost.HeaderEvaluationID] != "e_x" || st.rc.FaultHeaders[bifrost.HeaderArm] != "a" {
		t.Fatalf("plugin path: %q %v", st.rc.Run.Gateway.FaultPath, st.rc.FaultHeaders)
	}
	if proxy.plans["slot-1"].Mode != faultproxy.ModeNone {
		t.Fatalf("proxy should pass through, got %+v", proxy.plans["slot-1"])
	}
	if h := adapters.BifrostHeaders(st.rc); h[bifrost.HeaderFaultPlan] == "" {
		t.Fatalf("agent headers miss the plan: %v", h)
	}

	// An agent that may not send headers keeps the proxy plan.
	proxy2 := &c04Proxy{plans: map[string]schemas.FaultPlan{}}
	pf2 := bifrost.NewPluginFaults(proxy2)
	p2 := &Pipeline{reg: &schemas.Registry{Fault: pf2}}
	_ = pf2.SetPlan(ctx, "slot-1", armed)
	st2 := c04State("generic")
	if err := p2.routePluginFaults(ctx, st2); err != nil {
		t.Fatal(err)
	}
	if st2.rc.Run.Gateway.FaultPath != bifrost.FaultPathProxy || st2.rc.FaultHeaders != nil || proxy2.plans["slot-1"].Mode != faultproxy.ModePersistent {
		t.Fatalf("proxy path: %q %v %+v", st2.rc.Run.Gateway.FaultPath, st2.rc.FaultHeaders, proxy2.plans["slot-1"])
	}

	// Default proxy mode: nothing recorded, nothing changed.
	p3 := &Pipeline{reg: &schemas.Registry{Fault: proxy2}}
	st3 := c04State(adapters.ParserClaudeCode)
	if err := p3.routePluginFaults(ctx, st3); err != nil || st3.rc.Run.Gateway.FaultPath != "" {
		t.Fatalf("proxy mode: %v %q", err, st3.rc.Run.Gateway.FaultPath)
	}
}
