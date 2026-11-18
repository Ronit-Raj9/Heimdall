package evidence

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRequestAgents(t *testing.T) {
	log := &schemas.RequestLog{Requests: []schemas.GatewayRequest{
		{ID: "root", AgentLabel: "lead"},
		{ID: "retry", ParentID: "root", AgentID: "lead-key"},
		{ID: "other"},
	}}
	got := requestAgents(log)
	for _, tc := range []struct{ id, agent, source string }{
		{"root", "lead-key", schemas.AgentSourceGatewayKey}, // the key on a retry row of the same logical request wins
		{"retry", "lead-key", schemas.AgentSourceGatewayKey},
		{"other", "", ""},
	} {
		if p := got[tc.id]; p.agent != tc.agent || p.source != tc.source {
			t.Errorf("%s = %+v, want %s %s", tc.id, p, tc.agent, tc.source)
		}
	}
	if len(requestAgents(nil)) != 0 {
		t.Error("nil log places nothing")
	}
}

func TestByTime(t *testing.T) {
	b := &agentBuilder{nodes: map[string]*schemas.AgentNode{}, avail: map[string]bool{}, shape: schemas.AgentShapeInProcess}
	b.node(schemas.AgentRoot, schemas.AgentSourceSelfReport)
	p := b.node("planner", schemas.AgentSourceSelfReport)
	p.ParentID, p.Started, p.Ended = schemas.AgentRoot, agentAt(0), agentAt(10)
	w := b.node("worker", schemas.AgentSourceSelfReport)
	w.ParentID, w.Started, w.Ended = "planner", agentAt(2), agentAt(4)
	k := b.node("member", schemas.AgentSourceGatewayKey) // a team member never places by time
	k.ParentID, k.Started, k.Ended = schemas.AgentRoot, agentAt(0), agentAt(10)
	b.depths()
	for _, tc := range []struct {
		s    float64
		want string
	}{{1, "planner"}, {3, "worker"}, {11, schemas.AgentRoot}} {
		if got := b.byTime(agentAt(tc.s)); got.agent != tc.want {
			t.Errorf("t=%v: %q, want %q", tc.s, got.agent, tc.want)
		}
	}
}

func TestCoverage(t *testing.T) {
	b := &agentBuilder{nodes: map[string]*schemas.AgentNode{}, avail: map[string]bool{schemas.AgentSourceHeader: true, schemas.AgentSourceOTLP: true}}
	cov := b.coverage([]schemas.StepRecord{
		{Seq: 1, AgentID: "a", AgentSource: schemas.AgentSourceHeader},
		{Seq: 2, AgentID: "b", AgentSource: schemas.AgentSourceOTLP},
		{Seq: 3},
		{Seq: 4, WitnessOf: 1, AgentID: "a", AgentSource: schemas.AgentSourceHeader},
	})
	if cov.Steps != 3 || cov.Attributed != 2 || cov.Unknown != 1 || cov.ByStrength[schemas.AgentStrengthDeclared] != 1 ||
		cov.ByStrength[schemas.AgentStrengthReported] != 1 || len(cov.Available) != 2 || cov.Available[0] != schemas.AgentSourceHeader {
		t.Errorf("coverage = %+v", cov)
	}
	if empty := b.coverage(nil); empty.BySource != nil || empty.Steps != 0 {
		t.Errorf("empty coverage = %+v", empty)
	}
}
