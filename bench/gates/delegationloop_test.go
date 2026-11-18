package gates

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDelegationLoopGate(t *testing.T) {
	hashed := func(m schemas.AgentMessage, h string) schemas.AgentMessage {
		m.BodySHA256 = h
		return m
	}
	edge := func(kind, from, to, hash string, s int, ref string) schemas.AgentEdge {
		return schemas.AgentEdge{Kind: kind, From: from, To: to, PayloadSHA256: hash, At: at(s), Ref: ref}
	}
	cases := []struct {
		name     string
		ev       func() *schemas.Evidence
		fired    bool
		evidence []string
		detail   string
	}{
		{name: "lead broadcasts one task", ev: func() *schemas.Evidence {
			return teamEvidence(hashed(msg(1, "lead", "worker-a", "do T", 1), "h"), hashed(msg(2, "lead", "worker-b", "do T", 2), "h"))
		}},
		{name: "two unchanged hand-ons are allowed", ev: func() *schemas.Evidence {
			return teamEvidence(hashed(msg(1, "lead", "worker-a", "T", 1), "h"), hashed(msg(2, "worker-a", "worker-b", "T", 2), "h"),
				hashed(msg(3, "worker-b", "reviewer", "T", 3), "h"))
		}},
		{name: "third unchanged hand-on fires", ev: func() *schemas.Evidence {
			ev := teamEvidence(hashed(msg(1, "lead", "worker-a", "T", 1), "h"), hashed(msg(2, "worker-a", "worker-b", "T", 2), "h"),
				hashed(msg(3, "worker-b", "reviewer", "T", 3), "h"))
			ev.Agents.Agents = append(ev.Agents.Agents, schemas.AgentNode{ID: "helper"})
			ev.Messages = append(ev.Messages, hashed(msg(4, "reviewer", "helper", "T", 4), "h"))
			return ev
		}, fired: true, evidence: []string{"agent:reviewer", "message:4", "message:3", "message:2", "message:1"}, detail: "3 unchanged hand-ons"},
		{name: "cycle back to the lead", ev: func() *schemas.Evidence {
			return teamEvidence(hashed(msg(1, "lead", "worker-a", "T", 1), "h"), hashed(msg(2, "worker-a", "lead", "T", 2), "h"))
		}, fired: true, evidence: []string{"agent:worker-a", "message:2", "message:1"}, detail: "a cycle back to lead"},
		{name: "narrowed task is not a hand-on", ev: func() *schemas.Evidence {
			return teamEvidence(hashed(msg(1, "lead", "worker-a", "T", 1), "h"), hashed(msg(2, "worker-a", "lead", "T part 1 done", 2), "h2"))
		}},
		{name: "spawn edges re-delegating", ev: func() *schemas.Evidence {
			ev := teamEvidence()
			ev.Agents.Edges = []schemas.AgentEdge{
				edge(schemas.AgentEdgeSpawn, "lead", "worker-a", "p", 1, "call_1"),
				edge(schemas.AgentEdgeSpawn, "worker-a", "worker-b", "p", 2, "call_2"),
				edge(schemas.AgentEdgeSpawn, "worker-b", "reviewer", "p", 3, "call_3"),
				edge(schemas.AgentEdgeSpawn, "reviewer", "worker-a", "p", 4, ""),
			}
			return ev
		}, fired: true, evidence: []string{"agent:reviewer", "edge:3", "call_3", "call_2", "call_1"}, detail: "a cycle back to worker-a"},
		{name: "edges without payload hash are skipped", ev: func() *schemas.Evidence {
			ev := teamEvidence()
			ev.Agents.Edges = []schemas.AgentEdge{
				edge(schemas.AgentEdgeSpawn, "lead", "worker-a", "", 1, ""),
				edge(schemas.AgentEdgeSpawn, "worker-a", "lead", "", 2, ""),
			}
			return ev
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newDelegationLoopGate().Check(context.Background(), nil, tc.ev())
			if err != nil {
				t.Fatal(err)
			}
			if r.Fired != tc.fired || r.Effect != schemas.GateReport {
				t.Fatalf("result %+v", r)
			}
			if tc.fired && !reflect.DeepEqual(r.Evidence, tc.evidence) {
				t.Fatalf("evidence %v, want %v", r.Evidence, tc.evidence)
			}
			if tc.detail != "" && !strings.Contains(r.Detail, tc.detail) {
				t.Fatalf("detail %q, want %q", r.Detail, tc.detail)
			}
		})
	}
}
