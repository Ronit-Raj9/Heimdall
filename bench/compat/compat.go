// Package compat answers whether an agent can run a benchmark: the agent's
// capabilities (its capabilities: list or the defaults of its adapter)
// against what the benchmark needs (suite.Requires). The planner, the
// compatibility API and evaluations all use Check, so they never disagree.
// Full adds the output rules of agents that run outside the workspace
// (outputs.go), which the planner applies per task.
package compat

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Check reports whether agent can run benchmark s. missing lists the
// capabilities s needs that agent lacks, and reason says so in one plain
// sentence ("" when ok). A nil agent or suite is not ok.
func Check(agent *schemas.AgentConfig, s *schemas.Suite) (ok bool, missing []schemas.Capability, reason string) {
	if agent == nil {
		return false, nil, "no agent config"
	}
	if s == nil {
		return false, nil, "no benchmark"
	}
	missing = schemas.EffectiveCapabilities(agent).Missing(suite.Requires(s))
	if len(missing) == 0 {
		return true, nil, ""
	}
	return false, missing, Reason(agent.ID, s.ID, missing)
}

// Reason is the planner's refusal text: "agent <id> cannot run benchmark
// <id>: needs <cap>[, <cap>]".
func Reason(agentID, suiteID string, missing []schemas.Capability) string {
	names := make([]string, 0, len(missing))
	for _, c := range missing {
		names = append(names, string(c))
	}
	return fmt.Sprintf("agent %s cannot run benchmark %s: needs %s", agentID, suiteID, strings.Join(names, ", "))
}
