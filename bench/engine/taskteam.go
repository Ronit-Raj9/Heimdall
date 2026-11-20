package engine

import (
	"errors"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// checkTaskTeams refuses, at plan time, an arm that would run a task's own
// team (Task.Team, docs/multi-agent.md) that is not valid or that has more
// members than the gateway has key slots for the arm's provider. A team of N
// takes N slots at once, so without this check the plan would freeze and
// every such run would fail at key minting instead. An arm whose config
// declares a team of its own, or that is a single-agent or best-of-N
// baseline, never runs the task's team (utils.TeamOf) and is not checked
// here; resolveTeam checks a config team. Tasks are checked once each, in
// condition order, and every problem is reported.
func (p *Planner) checkTaskTeams(arms map[string]schemas.ArmManifest, suite *schemas.Suite, conds []condPlan) error {
	if suite == nil {
		return nil
	}
	sc, counts := p.reg.Gateway.(keySlotCounter)
	var errs []error
	for _, armID := range sortedKeys(arms) {
		cfg := arms[armID].Config
		if counts { // 3-A-01: an A2A team takes its entry and worker slots at once (utils.RunKeySlots)
			if need := utils.RunKeySlots(&schemas.RunContext{Agent: &cfg}); need > 1 {
				if n := sc.KeySlots(cfg.Gateway.Provider); need > n {
					errs = append(errs, fmt.Errorf("arm %s: its A2A team of %d agents (entry and a2a.team) needs %d gateway key slots, the gateway has %d (gateway.key_slots) for provider %q",
						armID, need, need, n, cfg.Gateway.Provider))
				}
			}
		}
		seen := map[string]bool{}
		for _, c := range conds {
			for _, id := range c.tasks {
				if seen[id] {
					continue
				}
				seen[id] = true
				task := suite.TaskByID(id)
				if task == nil || len(task.Team) == 0 {
					continue
				}
				if utils.IsA2A(&cfg) && cfg.Topology != schemas.TopologySingle && cfg.Topology != schemas.TopologyBestOfN { // 3-A-16: one run, one delegation mechanism
					errs = append(errs, fmt.Errorf("arm %s, task %s: the task declares a native team, but the arm is an A2A agent (it delegates through a2a.team); mixing both in one run is not supported: set the arm's topology to single, or leave the task out", armID, id))
					continue
				}
				team := utils.TeamOf(&schemas.RunContext{Agent: &cfg, Task: task})
				if len(team) == 0 || !slices.Equal(utils.TeamIDs(team), utils.TeamIDs(task.Team)) {
					continue
				}
				if cfg.Gateway.Routing != nil { // 3-A-05: the policy would bind the lead's key only
					errs = append(errs, fmt.Errorf("arm %s, task %s: gateway.routing cannot be combined with the task's team: the policy applies to the lead's key only, not per member; set topology single or leave the task out", armID, id))
					continue
				}
				if err := ValidateTeam(team, task); err != nil {
					errs = append(errs, fmt.Errorf("arm %s, task %s: %w", armID, id, err))
					continue
				}
				if err := checkTeamMembers(&cfg, team); err != nil {
					errs = append(errs, fmt.Errorf("arm %s, task %s: %w", armID, id, err))
					continue
				}
				if !counts {
					continue
				}
				if n := sc.KeySlots(cfg.Gateway.Provider); len(team) > n {
					errs = append(errs, fmt.Errorf("arm %s, task %s: its team of %d members needs %d gateway key slots, the gateway has %d (gateway.key_slots) for provider %q; raise gateway.key_slots or leave the task out (docs/multi-agent.md)",
						armID, id, len(team), len(team), n, cfg.Gateway.Provider))
				}
			}
		}
	}
	return errors.Join(errs...)
}
