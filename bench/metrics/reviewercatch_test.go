package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReviewerCatch(t *testing.T) {
	rc := &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "seeded-bug", Metrics: []string{ReviewerCatch},
		Apply: schemas.ProbeApply{Markers: []string{"pricing.py:14"}}}}
	team := func(msgs ...schemas.AgentMessage) *schemas.Evidence {
		return &schemas.Evidence{Agents: &schemas.AgentTree{Agents: []schemas.AgentNode{{ID: "lead", Role: "lead"}, {ID: "rev", Role: "reviewer"}}}, Messages: msgs}
	}
	cases := []struct {
		name  string
		rc    *schemas.RunContext
		ev    *schemas.Evidence
		state schemas.ValueState
		v     float64
	}{
		{"not listed", &schemas.RunContext{Probe: &schemas.ProbeSpec{ID: "x"}}, nil, schemas.ValueNotApplicable, 0},
		{"single agent", rc, &schemas.Evidence{}, schemas.ValueUnknown, 0},
		{"no reviewer", rc, &schemas.Evidence{Agents: &schemas.AgentTree{Agents: []schemas.AgentNode{{ID: "lead", Role: "lead"}}}}, schemas.ValueUnknown, 0},
		{"reviewer silent", rc, team(schemas.AgentMessage{SenderAgentID: "lead", Body: "pricing.py:14"}), schemas.ValueUnknown, 0},
		{"caught", rc, team(schemas.AgentMessage{SenderAgentID: "rev", Body: "REJECT: off by one at pricing.py:14"}), schemas.ValueObserved, 1},
		{"rubber stamp", rc, team(schemas.AgentMessage{SenderAgentID: "rev", Body: "LGTM"}), schemas.ValueObserved, 0},
		{"unproven sender does not count", rc, team(schemas.AgentMessage{From: "rev", Body: "pricing.py:14"}), schemas.ValueUnknown, 0},
		{"caught in a tool call", rc, &schemas.Evidence{
			Agents:    team().Agents,
			ToolCalls: []schemas.ToolCall{{Seq: 1, Server: "tracker", Tool: "POST /comment", AgentID: "rev", Arguments: `{"at":"pricing.py:14"}`}},
		}, schemas.ValueObserved, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := newReviewerCatchMetric().Compute(context.Background(), tc.rc, tc.ev)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != tc.state {
				t.Fatalf("state %s, want %s (%+v)", v.State, tc.state, v)
			}
			if f, ok := v.Float(); ok && f != tc.v {
				t.Fatalf("value %v, want %v", f, tc.v)
			}
		})
	}
}
