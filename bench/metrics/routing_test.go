package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// routedRows is a fixture run under a strong to cheap fallback chain: turn 1
// on strong, turn 2 strong failed (503) and the cheap fallback answered,
// turn 3 on cheap (session moved), turn 4 failed on both.
func routedRows() []schemas.GatewayRequest {
	row := func(id, parent, model, status string, fallback int, latency float64) schemas.GatewayRequest {
		return schemas.GatewayRequest{ID: id, ParentID: parent, Kind: kindMessage, Provider: "anthropic", ModelRequested: model,
			ModelServed: model, Status: status, FallbackIndex: fallback, LatencyMS: latency}
	}
	return []schemas.GatewayRequest{
		row("t1", "", "claude-sonnet-4-6", statusSuccess, 0, 100),
		row("t2", "", "claude-sonnet-4-6", "error", 0, 5),
		row("t2f", "t2", "claude-haiku-4-5", statusSuccess, 1, 40),
		row("t3", "", "claude-haiku-4-5", statusSuccess, 0, 60),
		row("t4", "", "claude-sonnet-4-6", "error", 0, 5),
		row("t4f", "t4", "claude-haiku-4-5", "error", 1, 5),
	}
}

func TestRoutingMetrics(t *testing.T) {
	usd := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: routedRows(), Complete: true}, Cost: []schemas.CostRow{
		{RequestID: "t1", USD: usd(0.03)}, {RequestID: "t2", FaultInject: true, USD: schemas.NotApplicable()},
		{RequestID: "t2f", USD: usd(0.002)}, {RequestID: "t3", USD: usd(0.004)},
	}}
	ctx := context.Background()
	check := func(name string, v schemas.Value, want float64) {
		t.Helper()
		if got, ok := v.Float(); !ok || got != want {
			t.Errorf("%s = %+v, want %g", name, v, want)
		}
	}
	v, _ := newFallbackUsedMetric().Compute(ctx, nil, ev)
	check(FallbackUsed, v, 1)
	v, _ = newServedModelSwitchesMetric().Compute(ctx, nil, ev)
	check(ServedModelSwitches, v, 1) // t1 strong, t2f cheap (switch), t3 cheap
	m := newRoutedTargetMetric()
	v, _ = m.Compute(ctx, nil, ev)
	check(RoutedTarget, v, 2)
	routed := &schemas.RunContext{Agent: &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Routing: &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain}}}}
	if d, _ := m.ComputeDerived(ctx, &schemas.RunContext{Agent: &schemas.AgentConfig{}}, ev); d != nil {
		t.Errorf("unrouted run got per-target values: %v", d)
	}
	d, err := m.ComputeDerived(ctx, routed, ev)
	if err != nil {
		t.Fatal(err)
	}
	check("share strong", d["routed_target_anthropic_claude_sonnet_4_6"], 1.0/3)
	check("share cheap", d["routed_target_anthropic_claude_haiku_4_5"], 2.0/3)
	check("cost strong", d["routed_cost_usd_anthropic_claude_sonnet_4_6"], 0.03)
	check("cost cheap", d["routed_cost_usd_anthropic_claude_haiku_4_5"], 0.006)
	check("latency cheap", d["routed_latency_ms_anthropic_claude_haiku_4_5"], 50)
	if len(d) != 6 {
		t.Errorf("derived = %v", d)
	}

	t.Run("primary only, no fallback", func(t *testing.T) {
		ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: routedRows()[:1], Complete: true}}
		v, _ := newFallbackUsedMetric().Compute(ctx, nil, ev)
		check(FallbackUsed, v, 0)
		v, _ = newServedModelSwitchesMetric().Compute(ctx, nil, ev)
		check(ServedModelSwitches, v, 0)
	})
	t.Run("incomplete log is unknown", func(t *testing.T) {
		ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: routedRows(), Reason: "rows still processing"}}
		for _, m := range []schemas.Metric{newFallbackUsedMetric(), newServedModelSwitchesMetric(), newRoutedTargetMetric()} {
			if v, _ := m.Compute(ctx, nil, ev); v.State != schemas.ValueUnknown {
				t.Errorf("%s = %+v, want unknown", m.GetName(), v)
			}
		}
		if d, _ := newRoutedTargetMetric().ComputeDerived(ctx, routed, ev); d != nil {
			t.Errorf("derived on incomplete log = %v", d)
		}
	})
	t.Run("no answered turn", func(t *testing.T) {
		ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: routedRows()[4:], Complete: true}}
		if v, _ := newServedModelSwitchesMetric().Compute(ctx, nil, ev); v.State != schemas.ValueNotApplicable {
			t.Errorf("switches = %+v", v)
		}
	})
	t.Run("unpriced attempt makes the target cost unknown", func(t *testing.T) {
		ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: routedRows()[:1], Complete: true},
			Cost: []schemas.CostRow{{RequestID: "t1", USD: schemas.Unknown("no price", schemas.EvidenceGateway)}}}
		d, _ := newRoutedTargetMetric().ComputeDerived(ctx, routed, ev)
		if v := d["routed_cost_usd_anthropic_claude_sonnet_4_6"]; v.State != schemas.ValueUnknown {
			t.Errorf("cost = %+v", v)
		}
	})
}

func TestTargetSlug(t *testing.T) {
	if got := TargetSlug("Anthropic/claude-haiku-4.5"); got != "anthropic_claude_haiku_4_5" {
		t.Errorf("slug = %q", got)
	}
}
