package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
	sc, counts := schemas.GatewayAs[keySlotCounter](p.reg.Gateway)
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
				if err := refuseTaskMemberAgents(task); err != nil {
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

// checkTeamCombos refuses, at plan time, every slot whose effective team
// (utils.TeamOf: the arm's own team, else the task's) would meet a feature
// team runs do not support, so no such plan freezes and fails late. The
// environment keeps its own refusals as a resume defense. Refused:
// multi-session tasks, tasks that run as root, snapshot grading
// (environments.ExecTeam), remote_http arms (no per-member remote egress
// forwarder), neutral realism (members launch with the standard framing)
// and adapter fault probes (members have no fault controller). Every
// problem is reported, once per arm and task or condition.
func checkTeamCombos(e *schemas.Experiment, suite *schemas.Suite, arms map[string]schemas.ArmManifest, conds []condPlan, compat *schemas.ArmManifest, compatConds []condPlan) error {
	if suite == nil {
		return nil
	}
	type armCase struct {
		id    string
		cfg   schemas.AgentConfig
		conds []condPlan
	}
	cases := make([]armCase, 0, len(arms)+1)
	for _, id := range sortedKeys(arms) {
		cases = append(cases, armCase{id, arms[id].Config, conds})
	}
	if compat != nil {
		cases = append(cases, armCase{CompatArm, compat.Config, compatConds})
	}
	neutral := schemas.EffectiveRealism(e, suite) == schemas.RealismNeutral
	snapshot := utils.VerifierMode(suite) == schemas.VerifierSnapshot
	var errs []error
	for _, ac := range cases {
		cfg := ac.cfg
		seen := map[string]bool{}
		for _, c := range ac.conds {
			fault := c.probe != nil && c.probe.Layer == schemas.LayerAdapter && c.probe.Apply.AdapterFault != nil
			faultSeen := false
			for _, id := range c.tasks {
				task := suite.TaskByID(id)
				if task == nil || len(utils.TeamOf(&schemas.RunContext{Agent: &cfg, Task: task})) == 0 {
					continue
				}
				if fault && !faultSeen {
					faultSeen = true
					errs = append(errs, fmt.Errorf("arm %s, condition %s: team runs do not support adapter fault probes (docs/multi-agent.md)", ac.id, c.id))
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				var why []string
				if len(task.Sessions) > 0 {
					why = append(why, "multi-session tasks")
				}
				if utils.TaskRunsAsRoot(task) {
					why = append(why, "tasks that run as root")
				}
				if snapshot {
					why = append(why, "snapshot grading")
				}
				if cfg.Adapter == "remote_http" {
					why = append(why, "remote_http agents")
				}
				if neutral {
					why = append(why, "realism: neutral")
				}
				if len(why) > 0 {
					errs = append(errs, fmt.Errorf("arm %s, task %s: team runs do not support %s (docs/multi-agent.md)", ac.id, id, strings.Join(why, ", ")))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// checkResourceBudgets refuses an effective agent config (after overrides)
// without positive CPU and memory budgets, as spec/agent.schema.json
// requires: the planner decodes configs without the schema, and Docker
// skips --cpus and --memory for a zero, so such a plan would run its
// agents with no CPU or memory limit. It is pure.
func checkResourceBudgets(cfg *schemas.AgentConfig) error {
	if !(cfg.Budgets.CPUs > 0) {
		return fmt.Errorf("budgets.cpus must be positive, got %v: an agent without it runs with no CPU limit", cfg.Budgets.CPUs)
	}
	if !(cfg.Budgets.MemGB > 0) {
		return fmt.Errorf("budgets.mem_gb must be positive, got %v: an agent without it runs with no memory limit", cfg.Budgets.MemGB)
	}
	return nil
}

// refuseTaskMemberAgents refuses a task team member that names its own agent
// config. Only an arm config's team has its member configs resolved and
// frozen with the arm (resolveTeam); a task team member always runs the
// arm's config, so a reference there would be silently ignored.
func refuseTaskMemberAgents(task *schemas.Task) error {
	var errs []error
	for _, m := range task.Team {
		if m.Agent != "" {
			errs = append(errs, fmt.Errorf("team member %s sets agent %q, which a task's own team does not support; every member runs the arm's config, so declare the team on the agent config instead (docs/multi-agent.md)", m.ID, m.Agent))
		}
	}
	return errors.Join(errs...)
}
