package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Y11: plan-time checks for pull arms (adapters/pull.go).

// pullTaskErrs refuses a task a pull arm cannot run: a bad pull block, an
// output kind it cannot produce (utils.PullOutputAllowed), or a state task
// without MCP tools to offer it.
func pullTaskErrs(suite *schemas.Suite, t *schemas.Task, arms map[string]schemas.ArmManifest, compat *schemas.ArmManifest) []error {
	kind := utils.OutputKind(suite, t)
	var errs []error
	check := func(id string, cfg *schemas.AgentConfig) {
		if !utils.IsPullAgent(cfg) {
			return
		}
		if err := utils.CheckPullAgent(cfg); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", id, err))
			return
		}
		if ok, why := utils.PullOutputAllowed(cfg.Pull, kind); !ok {
			errs = append(errs, fmt.Errorf("task %s has output %s, but arm %s runs a pull agent: %s", t.ID, kind, id, why))
			return
		}
		if kind == schemas.OutputState && !hasMCPService(suite, t) {
			errs = append(errs, fmt.Errorf("task %s has output state, but has no MCP tools that arm %s's pull agent could call", t.ID, id))
		}
	}
	for _, id := range sortedKeys(arms) {
		am := arms[id]
		check(id, &am.Config)
	}
	if compat != nil {
		check(CompatArm, &compat.Config)
	}
	return errs
}
