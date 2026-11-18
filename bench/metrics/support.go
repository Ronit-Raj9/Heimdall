package metrics

import (
	"context"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// W3 customer support metrics (docs/customer-support.md). They apply to a
// run of a support suite (track support) or of any task with a simulated
// user (a role user service); elsewhere they are not applicable. Each reads
// bench-witnessed evidence only: the tool proxy's ledger, the verifier's
// dimensions, and the gateway rows of the simulator's own key. The
// simulator's words are never read.
const (
	Escalated         = "escalated"
	EscalationCorrect = "escalation_correct"
	PolicyViolation   = "policy_violation"
	UserTurns         = "user_turns"
	SimulatorCostUSD  = "simulator_cost_usd"
)

// Escalation expectations a task declares (Task.Escalation).
const (
	EscalationExpected    = "expected"
	EscalationNotExpected = "not_expected"
)

// PolicyDimension is the verifier dimension that says whether the agent kept
// the support policy (1 kept, 0 broke it).
const PolicyDimension = "policy"

// respondTool is the conversation tool of the user simulator (simuser.py,
// tau2svc.py).
const respondTool = "respond_to_user"

// escalationToolRe matches a tool that hands the customer to a human:
// escalate_to_human, escalate_ticket, transfer_to_human_agents (tau2),
// handoff_to_agent, ...
var escalationToolRe = regexp.MustCompile(`(?i)(^|[_.-])(escalat\w*|transfer_to_human\w*|handoff\w*|hand_off\w*)`)

// IsEscalationTool reports whether a tool name hands the customer to a human.
func IsEscalationTool(name string) bool { return escalationToolRe.MatchString(name) }

var (
	_ schemas.Metric = (*escalatedMetric)(nil)
	_ schemas.Metric = (*escalationCorrectMetric)(nil)
	_ schemas.Metric = (*policyViolationMetric)(nil)
	_ schemas.Metric = (*userTurnsMetric)(nil)
	_ schemas.Metric = (*simulatorCostMetric)(nil)
)

// supportRun reports whether the support metrics apply to rc.
func supportRun(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	if rc.Suite != nil && rc.Suite.Track == schemas.TrackSupport {
		return true
	}
	return len(simuser.SimulatedServices(rc.Suite, rc.Task)) > 0
}

// escalatedCall reports whether the ledger holds an accepted escalation call.
func escalatedCall(calls []schemas.ToolCall) bool {
	for _, c := range calls {
		if IsEscalationTool(c.Tool) && !callFailed(c) {
			return true
		}
	}
	return false
}

// supportCalls is the run's proxied tool calls, or the value to return.
func supportCalls(rc *schemas.RunContext, ev *schemas.Evidence) ([]schemas.ToolCall, schemas.Value, bool) {
	if !supportRun(rc) {
		return nil, schemas.NotApplicable(), false
	}
	calls, v, ok := proxiedCalls(rc, ev)
	if !ok && v.State == schemas.ValueNotApplicable {
		return nil, schemas.Unknown("support task without a proxied service: escalation is not witnessed", schemas.EvidenceBoundary), false
	}
	return calls, v, ok
}

// escalatedMetric is 1 when the agent handed the customer to a human.
type escalatedMetric struct{}

func (m *escalatedMetric) GetName() string    { return Escalated }
func (m *escalatedMetric) GetVersion() string { return version }
func (m *escalatedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: Escalated, Version: version, Label: "Escalation rate", Kind: schemas.KindRate,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Share of support runs in which the agent called an escalation or transfer tool (escalate_*, transfer_to_human*, handoff*) and the tool accepted it, read from the tool proxy's ledger and averaged over support runs; lower means more containment, so read it beside escalation_correct.",
	}
}
func (m *escalatedMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	calls, v, ok := supportCalls(rc, ev)
	if !ok {
		return v, nil
	}
	if escalatedCall(calls) {
		return schemas.Observed(1, schemas.EvidenceBoundary), nil
	}
	return schemas.Observed(0, schemas.EvidenceBoundary), nil
}

// escalationCorrectMetric is 1 when the agent escalated exactly when the
// task says it should.
type escalationCorrectMetric struct{}

func (m *escalationCorrectMetric) GetName() string    { return EscalationCorrect }
func (m *escalationCorrectMetric) GetVersion() string { return version }
func (m *escalationCorrectMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: EscalationCorrect, Version: version, Label: "Escalation correct", Kind: schemas.KindRate,
		HigherIsBetter: true, MinEvidence: schemas.EvidenceBoundary,
		Description: "Share of runs whose escalation decision matches the task's declared expectation (escalated when task.yaml says escalation: expected, not escalated when it says not_expected), averaged over support runs of tasks that declare one.",
	}
}
func (m *escalationCorrectMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if rc == nil || rc.Task == nil || (rc.Task.Escalation != EscalationExpected && rc.Task.Escalation != EscalationNotExpected) {
		return schemas.NotApplicable(), nil
	}
	calls, v, ok := supportCalls(rc, ev)
	if !ok {
		return v, nil
	}
	if escalatedCall(calls) == (rc.Task.Escalation == EscalationExpected) {
		return schemas.Observed(1, schemas.EvidenceBoundary), nil
	}
	return schemas.Observed(0, schemas.EvidenceBoundary), nil
}

// policyViolationMetric is 1 when the verifier's policy dimension says the
// agent broke the support policy.
type policyViolationMetric struct{}

func (m *policyViolationMetric) GetName() string    { return PolicyViolation }
func (m *policyViolationMetric) GetVersion() string { return version }
func (m *policyViolationMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: PolicyViolation, Version: version, Label: "Policy violation rate", Kind: schemas.KindRate,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceOutcome,
		Description: "Share of runs whose state verifier scored the policy dimension below 1 (a refund, credit or change the policy does not allow, read from the exported service state), averaged over support runs whose verifier reports a policy dimension.",
	}
}
func (m *policyViolationMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !supportRun(rc) {
		return schemas.NotApplicable(), nil
	}
	g := orEmpty(ev).Grade
	if v, bad := gradeProblem(g); bad {
		return v, nil
	}
	p, ok := g.Dimensions[PolicyDimension]
	if !ok {
		return schemas.NotApplicable(), nil
	}
	if p < 1 {
		return schemas.Observed(1, schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(0, schemas.EvidenceOutcome), nil
}

// userTurnsMetric counts the customer's replies the agent received.
type userTurnsMetric struct{}

func (m *userTurnsMetric) GetName() string    { return UserTurns }
func (m *userTurnsMetric) GetVersion() string { return version }
func (m *userTurnsMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: UserTurns, Version: version, Label: "User turns", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Customer replies the agent received (accepted respond_to_user calls to the simulated user), read from the tool proxy's ledger, averaged over support runs whose user service is proxied.",
	}
}
func (m *userTurnsMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !supportRun(rc) {
		return schemas.NotApplicable(), nil
	}
	users := map[string]bool{}
	for _, s := range simuser.SimulatedServices(rc.Suite, rc.Task) {
		if !s.Hidden && utils.ServiceProxied(s) {
			users[s.ID] = true
		}
	}
	if len(users) == 0 {
		return schemas.Unknown("the simulated user is not proxied: user turns are not witnessed", schemas.EvidenceBoundary), nil
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), nil
	}
	n := 0
	for _, c := range ev.ToolCalls {
		if c.Tool == respondTool && c.IsToolCall() && !callFailed(c) && users[c.Server] {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceBoundary), nil
}

// simulatorCostMetric is what the simulated user's own model calls cost.
type simulatorCostMetric struct{}

func (m *simulatorCostMetric) GetName() string    { return SimulatorCostUSD }
func (m *simulatorCostMetric) GetVersion() string { return version }
func (m *simulatorCostMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: SimulatorCostUSD, Version: version, Label: "Simulator cost (USD)", Kind: schemas.KindUSD,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "US dollars the model-played user's calls cost per run, from the gateway rows of the simulator's own virtual key at pinned prices, report-only and never part of cost_usd, averaged over runs with a model-played user (a scripted user calls no model).",
	}
}
func (m *simulatorCostMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !supportRun(rc) {
		return schemas.NotApplicable(), nil
	}
	sim := simuser.Effective(rc.Suite, simExperiment(rc))
	users := map[string]bool{}
	for _, s := range simuser.SimulatedServices(rc.Suite, rc.Task) {
		if simuser.GatewayFor(s, sim) != nil {
			users[s.ID] = true
		}
	}
	if len(users) == 0 {
		return schemas.NotApplicable(), nil
	}
	total, seen := 0.0, map[string]bool{}
	for _, f := range orEmpty(ev).State {
		if f.Gateway == nil || !users[f.Service] {
			continue
		}
		if f.Gateway.USD.State != schemas.ValueObserved || f.Gateway.USD.V == nil {
			reason := f.Gateway.USD.Reason
			if reason == "" {
				reason = "simulator spend not observed"
			}
			return schemas.Unknown(reason, schemas.EvidenceGateway), nil
		}
		total += *f.Gateway.USD.V
		seen[f.Service] = true
	}
	if len(seen) < len(users) {
		return schemas.Unknown("simulator gateway rows missing", schemas.EvidenceGateway), nil
	}
	return schemas.Observed(total, schemas.EvidenceGateway), nil
}

// simExperiment is the run's experiment, from the context or its manifest.
func simExperiment(rc *schemas.RunContext) *schemas.Experiment {
	if rc.Experiment != nil {
		return rc.Experiment
	}
	if rc.Manifest != nil {
		return &rc.Manifest.Experiment
	}
	return nil
}

func newEscalatedMetric() *escalatedMetric                 { return &escalatedMetric{} }
func newEscalationCorrectMetric() *escalationCorrectMetric { return &escalationCorrectMetric{} }
func newPolicyViolationMetric() *policyViolationMetric     { return &policyViolationMetric{} }
func newUserTurnsMetric() *userTurnsMetric                 { return &userTurnsMetric{} }
func newSimulatorCostMetric() *simulatorCostMetric         { return &simulatorCostMetric{} }
