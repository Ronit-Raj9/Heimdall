package engine

import (
	"encoding/json"
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

// TestCheckTaskTeamsA2A covers 3-A-01, 3-A-05 and 3-A-16: an A2A team
// counts its entry and workers against the slots, an A2A arm never also
// runs a task's native team, and a routed arm never runs a task team.
func TestCheckTaskTeamsA2A(t *testing.T) {
	suite := &schemas.Suite{Tasks: []*schemas.Task{
		{ID: "teamed", Team: []schemas.TeamMember{{ID: "agent1", Role: "lead"}, {ID: "agent2"}}},
		{ID: "plain"},
	}}
	a2aArm := func(workers int, topology string) schemas.ArmManifest {
		var team []schemas.A2AMember
		for i := 0; i < workers; i++ {
			team = append(team, schemas.A2AMember{ID: "w" + string(rune('a'+i)), CardURL: "http://w/card"})
		}
		return schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "a2a", Topology: topology, A2A: &schemas.A2AAgent{CardURL: "http://e/card", Team: team}}}
	}
	conds := func(tasks ...string) []condPlan { return []condPlan{{id: "clean", tasks: tasks}} }
	p := NewPlanner(&schemas.Registry{Gateway: slotGateway{n: 2}}, t.TempDir(), nil)
	if err := p.checkTaskTeams(map[string]schemas.ArmManifest{"a": a2aArm(1, "")}, suite, conds("plain")); err != nil {
		t.Fatalf("entry plus one worker fits two slots: %v", err)
	}
	err := p.checkTaskTeams(map[string]schemas.ArmManifest{"a": a2aArm(2, "")}, suite, conds("plain"))
	if err == nil || !strings.Contains(err.Error(), "needs 3 gateway key slots, the gateway has 2") {
		t.Fatalf("an A2A team of three on two slots: %v", err)
	}
	err = p.checkTaskTeams(map[string]schemas.ArmManifest{"a": a2aArm(1, "")}, suite, conds("teamed"))
	if err == nil || !strings.Contains(err.Error(), "task teamed: the task declares a native team, but the arm is an A2A agent") {
		t.Fatalf("A2A arm on a native-team task: %v", err)
	}
	if err := p.checkTaskTeams(map[string]schemas.ArmManifest{"a": a2aArm(0, schemas.TopologySingle)}, suite, conds("teamed")); err != nil {
		t.Fatalf("a single-topology A2A arm runs alone: %v", err)
	}
	routed := schemas.ArmManifest{Config: schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Routing: &schemas.RoutingPolicy{}}}}
	err = p.checkTaskTeams(map[string]schemas.ArmManifest{"r": routed}, suite, conds("teamed"))
	if err == nil || !strings.Contains(err.Error(), "gateway.routing cannot be combined with the task's team") {
		t.Fatalf("routed arm on a team task: %v", err)
	}
}

// TestCheckTeamMembers covers 3-A-06 and 3-A-17.
func TestCheckTeamMembers(t *testing.T) {
	arm := &schemas.AgentConfig{Adapter: "scripted", Model: "m-a", Gateway: schemas.GatewayPolicy{Provider: "anthropic"}}
	ok := []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w", Model: "m-b"}}
	if err := checkTeamMembers(arm, ok); err != nil {
		t.Fatalf("a worker on another model is fine: %v", err)
	}
	for _, tc := range []struct {
		name string
		team []schemas.TeamMember
		want string
	}{
		{"lead model", []schemas.TeamMember{{ID: "lead", Role: "lead", Model: "m-b"}, {ID: "w"}}, "the lead runs on the run key"},
		{"lead config model", []schemas.TeamMember{{ID: "w"}, {ID: "lead", Role: "lead", Config: &schemas.AgentConfig{Adapter: "scripted", Model: "m-b"}}}, "its model is \"m-b\""},
		{"adapter", []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w", Config: &schemas.AgentConfig{Adapter: "command", Model: "m-a"}}}, "adapter \"command\" differs from the arm's \"scripted\""},
		{"provider", []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w", Config: &schemas.AgentConfig{Adapter: "scripted", Gateway: schemas.GatewayPolicy{Provider: "openai"}}}}, "gateway provider \"openai\""},
	} {
		if err := checkTeamMembers(arm, tc.team); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestRedactPlanKnownSecrets covers 3-A-12: a run key rendered into a file,
// an application-specific env name or argv never reaches plan.json.
func TestRedactPlanKnownSecrets(t *testing.T) {
	const fake = "sk-bf-inert-fixture-0001"
	plan := &schemas.RunPlan{
		Argv:  []string{"agent", "--api=" + fake},
		Env:   map[string]string{"MY_APP_CFG": "base=http://gw k=" + fake, "PLAIN": "x"},
		Files: map[string]string{"/home/agent/.cfg.toml": "api = \"" + fake + "\"\n"},
	}
	out := redactPlan(plan, fake)
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), fake) {
		t.Fatalf("plan copy keeps the key: %s", b)
	}
	if plan.Files["/home/agent/.cfg.toml"] == out.Files["/home/agent/.cfg.toml"] || !strings.Contains(plan.Env["MY_APP_CFG"], fake) {
		t.Fatal("the execution plan must keep its values")
	}
	if out.Env["PLAIN"] != "x" {
		t.Fatalf("an unrelated value changed: %q", out.Env["PLAIN"])
	}
}
