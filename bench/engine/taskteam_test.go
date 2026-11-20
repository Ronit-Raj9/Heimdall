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

// TestCheckTeamCombos is the 2-B-07, 2-B-10, 2-B-18 and 2-B-19 regression:
// every unsupported team combination is refused before the plan freezes,
// for the compatibility arm too, and a plain team plans.
func TestCheckTeamCombos(t *testing.T) {
	team := []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w"}}
	suite := &schemas.Suite{Tasks: []*schemas.Task{
		{ID: "plain"},
		{ID: "sessions", Sessions: []schemas.SessionSpec{{ID: "a"}, {ID: "b"}}},
		{ID: "root", Environment: &schemas.TaskEnvironment{User: "root", Image: "img:1"}},
		{ID: "own-team-sessions", Team: team, Sessions: []schemas.SessionSpec{{ID: "a"}}},
	}}
	arms := func(adapter string, team []schemas.TeamMember) map[string]schemas.ArmManifest {
		return map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{Adapter: adapter, Team: team}}}
	}
	cond := func(tasks ...string) []condPlan { return []condPlan{{id: "clean", tasks: tasks}} }
	fault := []condPlan{{id: "fault", tasks: []string{"plain"}, probe: &schemas.ProbeSpec{Layer: schemas.LayerAdapter, Apply: schemas.ProbeApply{AdapterFault: &schemas.AdapterFaultRule{}}}}}
	snap := &schemas.Suite{Tasks: suite.Tasks, Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}}
	for _, tc := range []struct {
		name   string
		e      *schemas.Experiment
		suite  *schemas.Suite
		arms   map[string]schemas.ArmManifest
		conds  []condPlan
		compat *schemas.ArmManifest
		want   string
	}{
		{"plain team plans", &schemas.Experiment{}, suite, arms("command", team), cond("plain"), nil, ""},
		{"solo arm on sessions plans", &schemas.Experiment{}, suite, arms("command", nil), cond("sessions", "root"), nil, ""},
		{"arm team on sessions", &schemas.Experiment{}, suite, arms("command", team), cond("sessions"), nil, "task sessions: team runs do not support multi-session tasks"},
		{"task team on sessions", &schemas.Experiment{}, suite, arms("command", nil), cond("own-team-sessions"), nil, "multi-session tasks"},
		{"arm team on root task", &schemas.Experiment{}, suite, arms("command", team), cond("root"), nil, "tasks that run as root"},
		{"snapshot suite", &schemas.Experiment{}, snap, arms("command", team), cond("plain"), nil, "snapshot grading"},
		{"remote http team", &schemas.Experiment{}, suite, arms("remote_http", team), cond("plain"), nil, "remote_http agents"},
		{"neutral realism", &schemas.Experiment{Realism: schemas.RealismNeutral}, suite, arms("command", team), cond("plain"), nil, "realism: neutral"},
		{"adapter fault probe", &schemas.Experiment{}, suite, arms("command", team), fault, nil, "condition fault: team runs do not support adapter fault probes"},
		{"compatibility arm", &schemas.Experiment{}, suite, arms("command", nil), cond("plain"), &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "command", Team: team}}, ""},
		{"compatibility arm on sessions", &schemas.Experiment{}, suite, arms("command", nil), nil, &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "command", Team: team}}, "arm " + CompatArm + ", task sessions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compatConds := cond("plain")
			if tc.name == "compatibility arm on sessions" {
				compatConds = cond("sessions")
			}
			err := checkTeamCombos(tc.e, tc.suite, tc.arms, tc.conds, tc.compat, compatConds)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// TestCheckResourceBudgets is the 2-B-15 regression: an effective config
// without positive CPU and memory budgets is refused.
func TestCheckResourceBudgets(t *testing.T) {
	for _, tc := range []struct {
		cpus, mem float64
		want      string
	}{{1, 2, ""}, {0, 2, "budgets.cpus"}, {1, 0, "budgets.mem_gb"}, {-1, 2, "budgets.cpus"}} {
		cfg := &schemas.AgentConfig{Budgets: schemas.Budgets{CPUs: tc.cpus, MemGB: tc.mem}}
		err := checkResourceBudgets(cfg)
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("cpus %v mem %v: %v, want %q", tc.cpus, tc.mem, err, tc.want)
		}
	}
}
