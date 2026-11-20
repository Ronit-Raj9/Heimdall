package utils

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestTeamHelpers(t *testing.T) {
	cfgTeam := []schemas.TeamMember{{ID: "a"}, {ID: "b"}}
	taskTeam := []schemas.TeamMember{{ID: "x"}, {ID: "y"}}
	if TeamOf(nil) != nil || TeamOf(&schemas.RunContext{}) != nil {
		t.Error("no run, no team")
	}
	if got := TeamOf(&schemas.RunContext{Agent: &schemas.AgentConfig{Team: cfgTeam}, Task: &schemas.Task{Team: taskTeam}}); got[0].ID != "a" {
		t.Errorf("the config's team wins: %v", got)
	}
	if got := TeamOf(&schemas.RunContext{Agent: &schemas.AgentConfig{}, Task: &schemas.Task{Team: taskTeam}}); got[0].ID != "x" {
		t.Errorf("task team: %v", got)
	}
	// X5b: a single or best-of-N baseline arm runs alone on a team task; an
	// unset topology keeps the task's team; the agent's own team always wins.
	for _, tc := range []struct {
		name     string
		agent    *schemas.AgentConfig
		wantLead string // first member id, empty for no team
	}{
		{"solo arm on team task", &schemas.AgentConfig{Topology: schemas.TopologySingle}, ""},
		{"best-of-N arm on team task", &schemas.AgentConfig{Topology: schemas.TopologyBestOfN}, ""},
		{"unset topology on team task", &schemas.AgentConfig{}, "x"},
		{"team topology on team task", &schemas.AgentConfig{Topology: schemas.TopologyTeam}, "x"},
		{"agent's own team wins over single", &schemas.AgentConfig{Topology: schemas.TopologySingle, Team: cfgTeam}, "a"},
		{"no agent config", nil, "x"},
	} {
		got := TeamOf(&schemas.RunContext{Agent: tc.agent, Task: &schemas.Task{Team: taskTeam}})
		switch {
		case tc.wantLead == "" && got != nil:
			t.Errorf("%s: want no team, got %v", tc.name, got)
		case tc.wantLead != "" && (len(got) == 0 || got[0].ID != tc.wantLead):
			t.Errorf("%s: want team led by %s, got %v", tc.name, tc.wantLead, got)
		}
	}
	for _, tc := range []struct{ in, tok, want string }{
		{"http://toolproxy:9200/s/db/mcp", "t1", "http://toolproxy:9200/m/t1/s/db/mcp"},
		{"http://toolproxy:9200/s/db/mcp", "", "http://toolproxy:9200/s/db/mcp"},
		{"http://other:8080/x", "t1", "http://other:8080/x"},
		{"http://toolproxy:9200/x/weather", "t1", "http://toolproxy:9200/m/t1/x/weather"}, // finding 3-D-06
	} {
		if got := MemberRouteURL(tc.in, tc.tok); got != tc.want {
			t.Errorf("MemberRouteURL(%s) = %s", tc.in, got)
		}
	}
	if m := MemberRoutes(map[string]string{"a": "t1", "b": "t2"}); m["t1"] != "a" || m["t2"] != "b" || MemberRoutes(nil) != nil {
		t.Errorf("MemberRoutes = %v", m)
	}
	if ids := TeamIDs(cfgTeam); len(ids) != 2 || ids[1] != "b" {
		t.Errorf("TeamIDs = %v", ids)
	}
}

// TestA2ATopologyAndSlots covers 3-A-01, 3-A-11 and 3-A-16.
func TestA2ATopologyAndSlots(t *testing.T) {
	a2a := &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://e/card", Team: []schemas.A2AMember{{ID: "w", BudgetUSD: 0.5}}}}
	task := &schemas.Task{Team: []schemas.TeamMember{{ID: "a"}, {ID: "b"}}}
	rc := &schemas.RunContext{Agent: a2a, Task: task}
	if TeamOf(rc) != nil {
		t.Fatal("an A2A arm never takes the task's native team")
	}
	if n := RunKeySlots(rc); n != 2 {
		t.Fatalf("RunKeySlots = %d, want 2", n)
	}
	if got := ArmTopology(a2a); got != schemas.TopologyTeam {
		t.Fatalf("ArmTopology = %q", got)
	}
	if got := ArmMemberBudgets(a2a); got != 0.5 {
		t.Fatalf("ArmMemberBudgets = %v", got)
	}
	if n := RunKeySlots(&schemas.RunContext{Agent: &schemas.AgentConfig{}, Task: task}); n != 2 {
		t.Fatalf("native task team RunKeySlots = %d", n)
	}
}

// TestRunSecrets covers 3-A-13: the MCP key is in the sanitizer's set.
func TestRunSecrets(t *testing.T) {
	rc := &schemas.RunContext{
		Key:          &schemas.RunKey{Value: "k-run", Members: []schemas.MemberKey{{Value: "k-m"}, {Value: "k-run"}}},
		Services:     []schemas.ServiceHandle{{GatewayKey: "k-mcp"}, {}},
		FaultRuntime: &schemas.AdapterFaultRuntime{Token: "t-fault"},
	}
	got := strings.Join(RunSecrets(rc), ",")
	if got != "k-run,k-m,k-mcp,t-fault" {
		t.Fatalf("RunSecrets = %s", got)
	}
}
