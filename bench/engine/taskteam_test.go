package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCheckTaskTeams(t *testing.T) {
	five := []schemas.TeamMember{{ID: "agent1", Role: "lead"}, {ID: "agent2"}, {ID: "agent3"}, {ID: "agent4"}, {ID: "agent5"}}
	three := five[:3]
	suite := &schemas.Suite{Tasks: []*schemas.Task{
		{ID: "database-1", Team: five},
		{ID: "coding-1", Team: three},
		{ID: "plain"},
		{ID: "bad-team", Team: []schemas.TeamMember{{ID: "Agent1"}, {ID: "agent2"}}},
		{ID: "member-agent", Team: []schemas.TeamMember{{ID: "agent1", Role: "lead"}, {ID: "agent2", Agent: "agents/missing.yaml"}}},
	}}
	arm := func(topology string, team []schemas.TeamMember) schemas.ArmManifest {
		return schemas.ArmManifest{Config: schemas.AgentConfig{Topology: topology, Team: team, Gateway: schemas.GatewayPolicy{Provider: "anthropic"}}}
	}
	conds := func(tasks ...string) []condPlan {
		return []condPlan{{id: "clean", tasks: tasks}, {id: "again", tasks: tasks}}
	}
	p := NewPlanner(&schemas.Registry{Gateway: slotGateway{n: 4}}, t.TempDir(), nil)
	for _, tc := range []struct {
		name  string
		arms  map[string]schemas.ArmManifest
		tasks []string
		want  []string // substrings of the error; nil for no error
	}{
		{"team fits", map[string]schemas.ArmManifest{"team": arm(schemas.TopologyTeam, nil)}, []string{"coding-1", "plain"}, nil},
		{"team over the slots", map[string]schemas.ArmManifest{"team": arm(schemas.TopologyTeam, nil)}, []string{"database-1", "coding-1"},
			[]string{"arm team, task database-1: its team of 5 members needs 5 gateway key slots, the gateway has 4 (gateway.key_slots)"}},
		{"unset topology runs the team", map[string]schemas.ArmManifest{"a": arm("", nil)}, []string{"database-1"}, []string{"task database-1", "team of 5"}},
		{"solo and best-of-N baselines run alone", map[string]schemas.ArmManifest{"single": arm(schemas.TopologySingle, nil), "bon": arm(schemas.TopologyBestOfN, nil)}, []string{"database-1"}, nil},
		{"config team is resolveTeam's", map[string]schemas.ArmManifest{"own": arm(schemas.TopologyTeam, three)}, []string{"database-1"}, nil},
		{"task member agent refused", map[string]schemas.ArmManifest{"team": arm(schemas.TopologyTeam, nil)}, []string{"member-agent"},
			[]string{"arm team, task member-agent: team member agent2 sets agent \"agents/missing.yaml\""}},
		{"invalid task team", map[string]schemas.ArmManifest{"team": arm(schemas.TopologyTeam, nil)}, []string{"bad-team"}, []string{"arm team, task bad-team: team: member id \"Agent1\""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := p.checkTaskTeams(tc.arms, suite, conds(tc.tasks...))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want a refusal naming %q", tc.want)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
			if n := strings.Count(err.Error(), "task database-1"); n > 1 {
				t.Errorf("one task is reported once per arm, got %d times: %v", n, err)
			}
		})
	}
	// A gateway that cannot count slots still gets the team validated.
	q := NewPlanner(&schemas.Registry{}, t.TempDir(), nil)
	if err := q.checkTaskTeams(map[string]schemas.ArmManifest{"team": arm("", nil)}, suite, conds("database-1")); err != nil {
		t.Errorf("no slot counter: %v", err)
	}
}
