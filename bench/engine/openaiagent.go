package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// A08: plan-time checks for openai_agent arms (adapters/openaiagent.go).

// openaiTaskErrs refuses a task an OpenAI-compatible endpoint arm cannot
// run: a bad openai block, a diff or artifact output (the endpoint is not
// beside the workspace), or a state output without MCP tools to offer it.
func openaiTaskErrs(suite *schemas.Suite, t *schemas.Task, arms map[string]schemas.ArmManifest, compat *schemas.ArmManifest) []error {
	kind := utils.OutputKind(suite, t)
	var errs []error
	check := func(id string, cfg *schemas.AgentConfig) {
		if !utils.IsOpenAIAgent(cfg) {
			return
		}
		if err := utils.CheckOpenAIAgent(cfg.OpenAI); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", id, err))
			return
		}
		if ok, why := utils.OpenAIOutputAllowed(cfg.OpenAI, kind); !ok {
			errs = append(errs, fmt.Errorf("task %s has output %s, but arm %s runs an OpenAI-compatible endpoint: %s", t.ID, kind, id, why))
			return
		}
		if kind == schemas.OutputState && !hasMCPService(suite, t) {
			errs = append(errs, fmt.Errorf("task %s has output state, but has no MCP tools that arm %s's OpenAI-compatible endpoint could call", t.ID, id))
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

// hasMCPService reports whether task t has a visible MCP service.
func hasMCPService(suite *schemas.Suite, t *schemas.Task) bool {
	for _, s := range utils.TaskServices(suite, t) {
		if s.Role == schemas.ServiceRoleMCP && !s.Hidden {
			return true
		}
	}
	return false
}
