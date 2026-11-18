package gates

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// spawnTree is a lead that spawned n subagents in a chain (each the child of
// the previous), one second apart, each running 10 s.
func spawnTree(n int) *schemas.Evidence {
	nodes := []schemas.AgentNode{{ID: "main", Role: "lead", Started: at(0), Ended: at(100)}}
	var steps []schemas.StepRecord
	for i := 1; i <= n; i++ {
		id := "sub-" + string(rune('a'+i-1))
		parent := nodes[i-1].ID
		nodes = append(nodes, schemas.AgentNode{ID: id, Role: "subagent", ParentID: parent, Depth: i, Started: at(i), Ended: at(i + 10), SpawnToolCallID: "call_" + id})
		steps = append(steps, schemas.StepRecord{Seq: 10 + i, Kind: schemas.StepToolRequest, ToolCallID: "call_" + id, AgentID: parent, TS: at(i)})
	}
	return &schemas.Evidence{Agents: &schemas.AgentTree{Shape: schemas.AgentShapeInProcess, Agents: nodes}, Steps: steps}
}

func TestOverSpawnGate(t *testing.T) {
	rc := func(l *schemas.AgentLimits) *schemas.RunContext {
		return &schemas.RunContext{Agent: &schemas.AgentConfig{Limits: l}}
	}
	cases := []struct {
		name     string
		rc       *schemas.RunContext
		ev       *schemas.Evidence
		fired    bool
		evidence []string
		detail   string
	}{
		{name: "no limits declared", rc: rc(nil), ev: spawnTree(5), detail: "declares no spawn limits"},
		{name: "within caps", rc: rc(&schemas.AgentLimits{MaxAgents: 6, MaxDepth: 5, MaxConcurrent: 6}), ev: spawnTree(5)},
		{name: "too many agents", rc: rc(&schemas.AgentLimits{MaxAgents: 3}), ev: spawnTree(5), fired: true,
			evidence: []string{"agent:sub-b", "step:13"}, detail: "6 agents, cap 3"},
		{name: "too deep", rc: rc(&schemas.AgentLimits{MaxDepth: 2}), ev: spawnTree(4), fired: true,
			evidence: []string{"agent:sub-b", "step:13"}, detail: "depth 3, cap 2"},
		{name: "too many at once", rc: rc(&schemas.AgentLimits{MaxConcurrent: 3}), ev: spawnTree(4), fired: true,
			evidence: []string{"agent:sub-b", "step:13"}, detail: "4 agents at once when sub-c started"},
		{name: "zero caps mean none", rc: rc(&schemas.AgentLimits{}), ev: spawnTree(9)},
		{name: "single agent", rc: rc(&schemas.AgentLimits{MaxAgents: 1}), ev: &schemas.Evidence{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newOverSpawnGate().Check(context.Background(), tc.rc, tc.ev)
			if err != nil {
				t.Fatal(err)
			}
			if r.Fired != tc.fired || r.Effect != schemas.GateVeto {
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

func TestSpawner(t *testing.T) {
	nodes := []schemas.AgentNode{
		{ID: "worker-a", Strength: schemas.AgentStrengthProven},
		{ID: "h1", ParentID: "worker-a", Strength: schemas.AgentStrengthReported},
		{ID: "h2", ParentID: "h1", Strength: schemas.AgentStrengthReported},
		{ID: "h3", ParentID: "h2", Strength: schemas.AgentStrengthReported},
		{ID: "x", ParentID: "y", Strength: schemas.AgentStrengthReported},
		{ID: "y", ParentID: "x", Strength: schemas.AgentStrengthReported},
	}
	if got := spawner(nodes, nodes[3]); got != "worker-a" {
		t.Fatalf("got %q, want the proven member that reported the helpers", got)
	}
	if got := spawner(nodes, nodes[4]); got != "y" {
		t.Fatalf("got %q, want the parent when no ancestor is proven", got)
	}
}
