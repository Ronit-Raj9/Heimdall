package marble

import (
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func rec(ids []string, rels ...[]string) record {
	r := record{Scenario: scenarioDatabase, TaskID: 1, Relationships: rels}
	for _, id := range ids {
		r.Agents = append(r.Agents, upAgent{AgentID: id})
	}
	return r
}

func TestBuildRoutesComplete(t *testing.T) {
	// Upstream research lists each pair once, coding lists both directions:
	// both are the same undirected graph.
	once := rec([]string{"agent1", "agent2", "agent3"},
		[]string{"agent1", "agent2", "collaborate with"}, []string{"agent1", "agent3", "collaborate with"}, []string{"agent2", "agent3", "collaborate with"})
	both := rec([]string{"agent1", "agent2", "agent3"},
		[]string{"agent1", "agent2", "collaborates with"}, []string{"agent2", "agent1", "collaborates with"},
		[]string{"agent1", "agent3", "collaborates with"}, []string{"agent3", "agent1", "collaborates with"},
		[]string{"agent2", "agent3", "collaborates with"}, []string{"agent3", "agent2", "collaborates with"})
	for name, r := range map[string]record{"each pair once": once, "both directions": both} {
		rt, skip := buildRoutes(r, "")
		if skip != "" {
			t.Fatalf("%s: %s", name, skip)
		}
		if !rt.Complete || rt.Lead != "agent1" || rt.Mode != "graph" {
			t.Errorf("%s: routes = %+v", name, rt)
		}
		for _, m := range rt.Members {
			if len(rt.Allowed[m]) != 2 || slices.Contains(rt.Allowed[m], m) {
				t.Errorf("%s: %s may message %v", name, m, rt.Allowed[m])
			}
		}
		if len(rt.Edges) != len(r.Relationships) {
			t.Errorf("%s: edges %d, want %d as declared", name, len(rt.Edges), len(r.Relationships))
		}
	}
}

func TestBuildRoutesPartialGraph(t *testing.T) {
	// A chain agent1 - agent2 - agent3 (and a self-loop, which is no route).
	r := rec([]string{"agent1", "agent2", "agent3"},
		[]string{"agent1", "agent2", "parent"}, []string{"agent2", "agent3", "parent"}, []string{"agent3", "agent3", "self"})
	rt, skip := buildRoutes(r, "")
	if skip != "" {
		t.Fatal(skip)
	}
	if rt.Complete {
		t.Error("a chain is not complete")
	}
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{"agent1", "agent2", true}, {"agent2", "agent1", true}, {"agent2", "agent3", true}, {"agent3", "agent2", true},
		{"agent1", "agent3", false}, {"agent3", "agent1", false}, {"agent3", "agent3", false},
	} {
		if got := rt.may(tc.from, tc.to); got != tc.want {
			t.Errorf("may(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	if p := rt.pathToLead("agent3"); !slices.Equal(p, []string{"agent3", "agent2", "agent1"}) {
		t.Errorf("path of agent3 = %v", p)
	}
	if p := rt.pathToLead("agent1"); !slices.Equal(p, []string{"agent1"}) {
		t.Errorf("path of the lead = %v", p)
	}
	if u := rt.unreachable(); len(u) != 0 {
		t.Errorf("unreachable = %v", u)
	}
	// With agent3 as lead, agent1 reaches it through agent2.
	rt3, _ := buildRoutes(r, "agent3")
	if p := rt3.pathToLead("agent1"); !slices.Equal(p, []string{"agent1", "agent2", "agent3"}) {
		t.Errorf("path to lead agent3 = %v", p)
	}
	team := rt3.team()
	if len(team) != 3 || team[2].Role != schemas.AgentRoleLead || team[0].Role != schemas.AgentRoleWorker || team[0].BudgetUSD != 0 {
		t.Errorf("team = %+v", team)
	}
}

func TestBuildRoutesRefusals(t *testing.T) {
	ids := []string{"agent1", "agent2"}
	for _, tc := range []struct {
		name string
		r    record
		lead string
		want string
	}{
		{"unknown agent", rec(ids, []string{"agent1", "agent9", "collaborate with"}), "", `names agent "agent9"`},
		{"not a triple", rec(ids, []string{"agent1", "agent2"}), "", "has 2 elements"},
		{"bad id", rec([]string{"Agent1", "agent2"}), "", `agent id "Agent1" is not a valid team member id`},
		{"repeated id", rec([]string{"agent1", "agent1"}), "", "used twice"},
		{"no agents", rec(nil), "", "declares no agents"},
		{"unknown lead", rec(ids, []string{"agent1", "agent2", "x"}), "agent7", `lead "agent7" is not an agent`},
	} {
		if _, skip := buildRoutes(tc.r, tc.lead); !strings.Contains(skip, tc.want) {
			t.Errorf("%s: skip = %q, want it to contain %q", tc.name, skip, tc.want)
		}
	}
	// A member with no relationship at all cannot reach the lead.
	rt, skip := buildRoutes(rec([]string{"agent1", "agent2", "agent3"}, []string{"agent1", "agent2", "x"}), "")
	if skip != "" {
		t.Fatal(skip)
	}
	if u := rt.unreachable(); !slices.Equal(u, []string{"agent3"}) {
		t.Errorf("unreachable = %v", u)
	}
}

func TestLeakTerm(t *testing.T) {
	r := rec([]string{"agent1", "agent2"})
	r.Task.Content = "Write a proposal."
	if got := leakTerm(r); got != "" {
		t.Errorf("clean text: %q", got)
	}
	r.Agents[1].Profile = "My work aligns closely with expert human Graders."
	if got := leakTerm(r); got != "grader" {
		t.Errorf("profile with a leak term: %q", got)
	}
	w := &writer{set: settings{scenario: scenarioResearch}}
	if _, skip := w.check(r); !strings.Contains(skip, `mentions "grader"`) {
		t.Errorf("check skip = %q", skip)
	}
}
