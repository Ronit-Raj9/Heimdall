package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAdapterFaultRefusesTeams: an adapter fault probe is refused for an arm
// that runs as a team (its own or the task's) and allowed for a solo arm or
// a single-topology baseline on a team task (audit 8-B-11).
func TestAdapterFaultRefusesTeams(t *testing.T) {
	suite := &schemas.Suite{Tasks: []*schemas.Task{{ID: "solo"}, {ID: "teamed", Team: []schemas.TeamMember{{ID: "a"}, {ID: "b"}}}}}
	probe := &schemas.ProbeSpec{ID: "p"}
	tests := []struct {
		name   string
		cfg    schemas.AgentConfig
		tasks  []string
		refuse bool
	}{
		{"solo", schemas.AgentConfig{}, []string{"solo"}, false},
		{"arm team", schemas.AgentConfig{Team: []schemas.TeamMember{{ID: "a"}}}, []string{"solo"}, true},
		{"task team", schemas.AgentConfig{}, []string{"teamed"}, true},
		{"single baseline", schemas.AgentConfig{Topology: schemas.TopologySingle}, []string{"teamed"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := adapterFaultTeamErr(&tc.cfg, suite, condPlan{id: "c", probe: probe, tasks: tc.tasks})
			if (err != nil) != tc.refuse {
				t.Fatalf("err = %v, want refuse=%v", err, tc.refuse)
			}
		})
	}
}
