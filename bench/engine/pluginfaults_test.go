package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/failover"
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

// H02: a team member routes on its own key slot with its own config, and
// never inherits the lead's headers.
func TestRouteMemberFaults(t *testing.T) {
	ctx := context.Background()
	proxy := &c04Proxy{plans: map[string]schemas.FaultPlan{}}
	pf := bifrost.NewPluginFaults(proxy)
	p := &Pipeline{reg: &schemas.Registry{Fault: pf}}
	outage := schemas.FaultPlan{Mode: faultproxy.ModePersistent, FromCall: 1, Status: 503, Model: "gpt-x", RunID: "run-7"}
	_ = pf.SetPlan(ctx, "slot-2", outage)
	_ = pf.SetPlan(ctx, "slot-3", outage)

	paths := map[string]bool{}
	carrier := c04State(adapters.ParserCodex).rc
	carrier.Agent = &schemas.AgentConfig{Adapter: "command", AdapterCaps: &schemas.AdapterCapabilities{SupportsGatewayHeaders: true}}
	carrier.Key = &schemas.RunKey{KeySlot: "slot-2"}
	carrier.FaultHeaders = map[string]string{"x-heimdall-fault-plan": "lead"}
	if err := p.routeMemberFaults(ctx, carrier, paths); err != nil {
		t.Fatal(err)
	}
	pp, err := bifrost.DecodePluginPlan(carrier.FaultHeaders[bifrost.HeaderFaultPlan])
	if err != nil || pp.Mode != bifrost.PluginModeError || pp.Model != "gpt-x" || pp.Status != 503 {
		t.Fatalf("member plan %+v %v (%v)", pp, err, carrier.FaultHeaders)
	}
	if proxy.plans["slot-2"].Mode != faultproxy.ModeNone {
		t.Fatalf("member slot still faulted on the proxy: %+v", proxy.plans["slot-2"])
	}

	plain := c04State("generic").rc
	plain.Key = &schemas.RunKey{KeySlot: "slot-3"}
	plain.FaultHeaders = map[string]string{"x-heimdall-fault-plan": "lead"}
	if err := p.routeMemberFaults(ctx, plain, paths); err != nil {
		t.Fatal(err)
	}
	if plain.FaultHeaders != nil || proxy.plans["slot-3"].Mode != faultproxy.ModePersistent {
		t.Fatalf("plain member: %v %+v", plain.FaultHeaders, proxy.plans["slot-3"])
	}
	if got := teamFaultPath(paths); got != bifrost.FaultPathMixed {
		t.Fatalf("team path %q", got)
	}
	if teamFaultPath(map[string]bool{bifrost.FaultPathPlugin: true}) != bifrost.FaultPathPlugin || teamFaultPath(nil) != "" {
		t.Fatal("teamFaultPath")
	}
}

// C07 on the plugin path: the primary-outage plan rides as the same inline
// outage failover.Plan builds for Arm.Headers (error, status, model, from
// the first call).
func TestPrimaryOutageViaPlugin(t *testing.T) {
	ctx := context.Background()
	pf := bifrost.NewPluginFaults(&c04Proxy{plans: map[string]schemas.FaultPlan{}})
	p := &Pipeline{reg: &schemas.Registry{Fault: pf}}
	_ = pf.SetPlan(ctx, "slot-1", schemas.FaultPlan{Mode: faultproxy.ModePersistent, FromCall: 1, Status: 503, Model: "gpt-4o", RunID: "run-7"})
	st := c04State(adapters.ParserClaudeCode)
	st.rc.Probe = &schemas.ProbeSpec{ID: schemas.GenericProbePrimaryOutage}
	if err := p.routePluginFaults(ctx, st); err != nil {
		t.Fatal(err)
	}
	pp, err := bifrost.DecodePluginPlan(st.rc.FaultHeaders[bifrost.HeaderFaultPlan])
	if err != nil {
		t.Fatal(err)
	}
	arms, err := failover.Plan("openai", "gpt-4o", failover.Option{Enabled: true, Fallbacks: []schemas.RouteTarget{{Model: "gpt-4o-mini"}}}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := bifrost.DecodePluginPlan(arms[1].Headers[failover.HeaderFaultPlan])
	if err != nil {
		t.Fatal(err)
	}
	if pp.Mode != want.Mode || pp.Status != want.Status || pp.Model != want.Model || pp.FromCall != want.FromCall {
		t.Fatalf("plugin plan %+v, failover arm plan %+v", pp, want)
	}
	if st.rc.Run.Gateway.FaultPath != bifrost.FaultPathPlugin {
		t.Fatalf("path %q", st.rc.Run.Gateway.FaultPath)
	}
}
