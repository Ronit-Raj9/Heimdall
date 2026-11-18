package bifrost

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X5b: a single-agent or best-of-N baseline arm on a task that declares a
// team runs alone (utils.TeamOf), so under equal_budget it keeps the cap on
// its own run key; an arm that runs the task's team gets the cap on the team
// instead (MintMemberKeys), never on the run key.
func TestRunBudgetTeamTask(t *testing.T) {
	team := []schemas.TeamMember{{ID: "agent1", Role: "lead"}, {ID: "agent2"}, {ID: "agent3"}}
	fair := &schemas.Experiment{Fairness: schemas.FairnessEqualBudget}
	for _, tc := range []struct {
		name string
		rc   *schemas.RunContext
		want float64
	}{
		{"solo baseline on team task", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{Team: team},
			Agent: &schemas.AgentConfig{Topology: schemas.TopologySingle, Budgets: schemas.Budgets{USD: 2}}}, 2},
		{"best-of-N baseline on team task", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{Team: team},
			Agent: &schemas.AgentConfig{Topology: schemas.TopologyBestOfN, Budgets: schemas.Budgets{USD: 2}}}, 2},
		{"team arm on team task", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{Team: team},
			Agent: &schemas.AgentConfig{Topology: schemas.TopologyTeam, Budgets: schemas.Budgets{USD: 2}}}, 0},
		{"unset topology on team task", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{Team: team},
			Agent: &schemas.AgentConfig{Budgets: schemas.Budgets{USD: 2}}}, 0},
		{"config team", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{},
			Agent: &schemas.AgentConfig{Team: team, Budgets: schemas.Budgets{USD: 2}}}, 0},
		{"single agent, plain task", &schemas.RunContext{Experiment: fair, Task: &schemas.Task{},
			Agent: &schemas.AgentConfig{Budgets: schemas.Budgets{USD: 2}}}, 2},
		{"no fairness rule", &schemas.RunContext{Experiment: &schemas.Experiment{}, Task: &schemas.Task{Team: team},
			Agent: &schemas.AgentConfig{Topology: schemas.TopologySingle, Budgets: schemas.Budgets{USD: 2}}}, 0},
	} {
		if got := runBudget(tc.rc); got != tc.want {
			t.Errorf("%s: runBudget = %v, want %v", tc.name, got, tc.want)
		}
		if tc.rc.Agent.Topology == schemas.TopologySingle && tc.rc.Experiment.Fairness != "" {
			if n := teamSlots(tc.rc); n != 1 {
				t.Errorf("%s: a solo baseline takes one key slot, got %d", tc.name, n)
			}
		}
	}
}
