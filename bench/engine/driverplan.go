package engine

import (
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X3 (docs/kubernetes.md): the environment driver at plan time. Every
// planned task, condition and agent config is put to the suite
// environment's driver, which refuses what it cannot run with the reason,
// and the driver that will run the experiment is frozen into the manifest
// (nil for Docker when the experiment names none, so Docker manifests hash
// as before).
func (p *Planner) planDriver(e *schemas.Experiment, suite *schemas.Suite, conds, compatConds []condPlan, m *schemas.Manifest) (*schemas.EnvironmentChoice, error) {
	var agents []*schemas.AgentConfig
	for _, id := range sortedKeys(m.Arms) {
		cfg := m.Arms[id].Config
		agents = append(agents, &cfg)
	}
	// Only the combinations the scheduler builds are put to the driver: the
	// main arms on the main conditions, the compatibility agent on its own
	// (audit 8-B-10).
	slots := driverSlots(suite, conds, agents)
	if m.Compatibility != nil {
		cfg := m.Compatibility.Config
		slots = append(slots, driverSlots(suite, compatConds, []*schemas.AgentConfig{&cfg})...)
	}
	return environments.PlanDriver(p.reg.Environments, suite, e, slots)
}

// driverSlots crosses conds' tasks with agents.
func driverSlots(suite *schemas.Suite, conds []condPlan, agents []*schemas.AgentConfig) []environments.DriverSlot {
	var slots []environments.DriverSlot
	for _, c := range conds {
		for _, id := range c.tasks {
			t := suite.TaskByID(id)
			if t == nil {
				continue
			}
			for _, a := range agents {
				slots = append(slots, environments.DriverSlot{Suite: suite, Task: t, Probe: c.probe, Agent: a})
			}
		}
	}
	return slots
}
