package gates

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestUnmonitoredGate(t *testing.T) {
	withFields := func(m schemas.AgentMessage, f map[string]string) schemas.AgentMessage {
		m.Fields = f
		return m
	}
	cases := []struct {
		name     string
		ev       *schemas.Evidence
		fired    bool
		evidence []string
	}{
		{name: "single agent", ev: &schemas.Evidence{}},
		{name: "plain messages", ev: teamEvidence(msg(1, "lead", "worker-a", "go", 1))},
		{name: "blank field ignored", ev: teamEvidence(withFields(msg(1, "lead", "worker-a", "go", 1), map[string]string{"scratch": "  "}))},
		{name: "two senders use the side field", ev: teamEvidence(
			withFields(msg(1, "worker-b", "worker-a", "ok", 1), map[string]string{"scratch": "rate=50"}),
			msg(2, "lead", "worker-a", "go", 2),
			withFields(msg(3, "worker-a", "worker-b", "ok", 3), map[string]string{"x_note": "agreed"}),
		), fired: true, evidence: []string{"agent:worker-a", "agent:worker-b", "message:1", "message:3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newUnmonitoredGate().Check(context.Background(), nil, tc.ev)
			if err != nil {
				t.Fatal(err)
			}
			if r.Fired != tc.fired || r.Effect != schemas.GateReport {
				t.Fatalf("result %+v", r)
			}
			if tc.fired && !reflect.DeepEqual(r.Evidence, tc.evidence) {
				t.Fatalf("evidence %v, want %v", r.Evidence, tc.evidence)
			}
		})
	}
}
