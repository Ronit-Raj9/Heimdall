package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func supportRC(sim *schemas.SimulatorSpec, escalation string) *schemas.RunContext {
	yes := true
	return &schemas.RunContext{
		Suite: &schemas.Suite{ID: "s", Track: schemas.TrackSupport, Simulator: sim},
		Task: &schemas.Task{ID: "t", Escalation: escalation, Services: []schemas.ServiceSpec{
			{ID: "helpdesk", Role: schemas.ServiceRoleMCP, Port: 8000},
			{ID: "customer", Role: schemas.ServiceRoleUser, Port: 8000, Proxy: &yes},
		}},
	}
}

func call(server, tool string, isErr bool) schemas.ToolCall {
	return schemas.ToolCall{Server: server, Tool: tool, Protocol: "mcp", IsError: isErr}
}

func val(t *testing.T, m schemas.Metric, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	v, err := m.Compute(t.Context(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func wantObserved(t *testing.T, v schemas.Value, want float64) {
	t.Helper()
	if v.State != schemas.ValueObserved || v.V == nil || *v.V != want {
		t.Fatalf("got %+v, want observed %v", v, want)
	}
}

func wantState(t *testing.T, v schemas.Value, state schemas.ValueState) {
	t.Helper()
	if v.State != state {
		t.Fatalf("got %+v, want %s", v, state)
	}
}

func TestEscalationMetrics(t *testing.T) {
	esc := &schemas.Evidence{ToolCalls: []schemas.ToolCall{call("customer", "respond_to_user", false), call("helpdesk", "escalate_to_human", false)}}
	failed := &schemas.Evidence{ToolCalls: []schemas.ToolCall{call("helpdesk", "escalate_to_human", true)}}
	none := &schemas.Evidence{ToolCalls: []schemas.ToolCall{call("helpdesk", "get_order", false)}}

	wantObserved(t, val(t, newEscalatedMetric(), supportRC(nil, ""), esc), 1)
	wantObserved(t, val(t, newEscalatedMetric(), supportRC(nil, ""), failed), 0)
	wantObserved(t, val(t, newEscalatedMetric(), supportRC(nil, ""), none), 0)
	wantState(t, val(t, newEscalatedMetric(), supportRC(nil, ""), &schemas.Evidence{}), schemas.ValueUnknown)
	wantState(t, val(t, newEscalatedMetric(), &schemas.RunContext{Suite: &schemas.Suite{}, Task: &schemas.Task{}}, esc), schemas.ValueNotApplicable)

	wantObserved(t, val(t, newEscalationCorrectMetric(), supportRC(nil, EscalationExpected), esc), 1)
	wantObserved(t, val(t, newEscalationCorrectMetric(), supportRC(nil, EscalationExpected), none), 0)
	wantObserved(t, val(t, newEscalationCorrectMetric(), supportRC(nil, EscalationNotExpected), esc), 0)
	wantObserved(t, val(t, newEscalationCorrectMetric(), supportRC(nil, EscalationNotExpected), none), 1)
	wantState(t, val(t, newEscalationCorrectMetric(), supportRC(nil, ""), esc), schemas.ValueNotApplicable)

	for _, name := range []string{"escalate_to_human", "transfer_to_human_agents", "handoff_to_agent", "desk.escalate"} {
		if !IsEscalationTool(name) {
			t.Errorf("%s is an escalation tool", name)
		}
	}
	for _, name := range []string{"get_order", "respond_to_user", "deescalation_notes"} {
		if IsEscalationTool(name) {
			t.Errorf("%s is not an escalation tool", name)
		}
	}
}

func TestPolicyViolation(t *testing.T) {
	m := newPolicyViolationMetric()
	rc := supportRC(nil, "")
	wantObserved(t, val(t, m, rc, &schemas.Evidence{Grade: &schemas.Grade{Dimensions: map[string]float64{"policy": 0, "db": 1}}}), 1)
	wantObserved(t, val(t, m, rc, &schemas.Evidence{Grade: &schemas.Grade{Dimensions: map[string]float64{"policy": 1}}}), 0)
	wantState(t, val(t, m, rc, &schemas.Evidence{Grade: &schemas.Grade{Dimensions: map[string]float64{"db": 1}}}), schemas.ValueNotApplicable)
	wantState(t, val(t, m, rc, &schemas.Evidence{}), schemas.ValueUnknown)
	wantState(t, val(t, m, rc, &schemas.Evidence{Grade: &schemas.Grade{InvalidEvaluator: true}}), schemas.ValueUnknown)
}

func TestUserTurns(t *testing.T) {
	m := newUserTurnsMetric()
	ev := &schemas.Evidence{ToolCalls: []schemas.ToolCall{
		call("customer", "respond_to_user", false), call("customer", "respond_to_user", false),
		call("customer", "respond_to_user", true), call("helpdesk", "get_order", false),
	}}
	wantObserved(t, val(t, m, supportRC(nil, ""), ev), 2)
	wantState(t, val(t, m, supportRC(nil, ""), &schemas.Evidence{}), schemas.ValueUnknown)
	rc := supportRC(nil, "")
	rc.Task.Services[1].Proxy = nil // not proxied: not witnessed
	rc.Task.Services = rc.Task.Services[1:]
	wantState(t, val(t, m, rc, ev), schemas.ValueUnknown)
	// An mcp server marked gateway: {} plays the user (acme-helpdesk, tau2).
	tau2 := &schemas.RunContext{Suite: &schemas.Suite{Track: schemas.TrackSupport}, Task: &schemas.Task{Services: []schemas.ServiceSpec{{ID: "tau2", Role: schemas.ServiceRoleMCP, Port: 8000, Gateway: &schemas.ServiceGateway{}}}}}
	wantObserved(t, val(t, m, tau2, &schemas.Evidence{ToolCalls: []schemas.ToolCall{call("tau2", "respond_to_user", false)}}), 1)
}

func TestSimulatorCost(t *testing.T) {
	m := newSimulatorCostMetric()
	llm := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim"}
	usd := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	ev := &schemas.Evidence{State: []schemas.StateFile{
		{Service: "customer", ID: "transcript.json"},
		{Service: "customer", ID: "_gateway.json", Gateway: &schemas.ServiceGatewayUsage{USD: usd(0.25)}},
	}}
	wantObserved(t, val(t, m, supportRC(llm, ""), ev), 0.25)
	wantState(t, val(t, m, supportRC(nil, ""), ev), schemas.ValueNotApplicable) // scripted user
	wantState(t, val(t, m, supportRC(llm, ""), &schemas.Evidence{}), schemas.ValueUnknown)
	bad := &schemas.Evidence{State: []schemas.StateFile{{Service: "customer", Gateway: &schemas.ServiceGatewayUsage{USD: schemas.Unknown("rows incomplete", schemas.EvidenceGateway)}}}}
	if v := val(t, m, supportRC(llm, ""), bad); v.State != schemas.ValueUnknown || v.Reason != "rows incomplete" {
		t.Fatalf("got %+v", v)
	}
	// An experiment override switches a scripted suite to llm.
	rc := supportRC(&schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}, "")
	rc.Experiment = &schemas.Experiment{Simulator: llm}
	wantObserved(t, val(t, m, rc, ev), 0.25)
}
