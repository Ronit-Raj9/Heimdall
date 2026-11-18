package engine

import (
	"errors"
	"fmt"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// W3 customer support: the user simulator at plan time
// (docs/customer-support.md).

// ScenarioSimulator is the manifest scenario key of the effective user
// simulator settings (with the prompt's sha256).
const ScenarioSimulator = "simulator"

// planSimulator checks the experiment's effective user simulator against
// the suite and the selected tasks and records its hash in the manifest. One
// experiment runs one simulator for every arm, condition and task (there is
// no per-arm setting), so scripted-user and llm-user runs are never in one
// comparison; and in llm mode every selected task must carry a persona, so a
// task never silently falls back to its script. An experiment that sets a
// simulator for a suite without a simulated user is refused.
func planSimulator(e *schemas.Experiment, suite *schemas.Suite, all []condPlan, m *schemas.Manifest, logger schemas.Logger) error {
	sim := simuser.Effective(suite, e)
	backed := map[string][]schemas.ServiceSpec{}
	for _, id := range selectedTasks(all) {
		t := suite.TaskByID(id)
		if t == nil {
			continue
		}
		if svcs := simuser.SimulatedServices(nil, t); len(svcs) > 0 {
			backed[id] = svcs
		}
	}
	suiteBacked := simuser.SimulatedServices(suite, nil)
	if sim == nil {
		return nil
	}
	if e.Simulator != nil && len(backed) == 0 && len(suiteBacked) == 0 {
		return errors.New("the experiment sets a user simulator, but no selected task has a simulated user (a role user service)")
	}
	if err := simuser.Check(sim); err != nil {
		return err
	}
	var errs []error
	check := func(base string, s schemas.ServiceSpec) {
		rel, ok := s.Files[simuser.UserFile]
		if !ok {
			return // a server that holds its own user (an imported tau2 domain) has no user file
		}
		p, err := utils.ResolveInside(base, rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("service %s user file: %w", s.ID, err))
			return
		}
		if err := simuser.CheckUserFile(p, sim.Mode); err != nil {
			errs = append(errs, fmt.Errorf("service %s: %w", s.ID, err))
		}
	}
	for _, s := range suiteBacked {
		check(suite.Root, s)
	}
	ids := make([]string, 0, len(backed))
	for id := range backed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, s := range backed[id] {
			check(suite.TaskByID(id).Dir, s)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("user simulator %s: %w", sim.Mode, err)
	}
	h, err := simuser.Hash(sim)
	if err != nil {
		return err
	}
	if m.Scenario == nil {
		m.Scenario = map[string]string{}
	}
	m.Scenario[ScenarioSimulator] = h
	m.Simulator = sim
	if simuser.IsLLM(sim) {
		logger.Warn("simulated user: results are labelled "+simuser.Label(sim)+" and are never compared with scripted-user runs",
			"experiment", e.ID, "max_turns", sim.MaxTurns)
	}
	return nil
}

// selectedTasks is every task id the plan selected, once, sorted.
func selectedTasks(all []condPlan) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range all {
		for _, id := range c.tasks {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}
