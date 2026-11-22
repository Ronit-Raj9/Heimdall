package handlers

import (
	"maps"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

// withVariantArms adds the variant arms of a one-arm (score mode) evaluation
// to arms, the experiment arms of spec keyed by spec arm label (evalArms,
// after the task-image variants were applied). In this order and only when
// enabled: the cache-on copy (BifrostCache), the failover arm (evalFailover)
// and the no-memory control (evalStreamControl). The experiment keeps the
// paired sign-flip verdict, which compares arm A with arm B by id, so the
// first variant added takes the id the spec's own arm does not have (B for
// the usual arm A); later ones keep their own ids (EvaluationCacheArm,
// failover.ArmID, evalControlArm). Labels keep their suffixes, which is how
// readers find a variant (schemas.VariantArmID). freshArm is the control's
// id, "" when there is none. Specs of two or more arms come back unchanged.
func withVariantArms(spec schemas.EvaluationSpec, arms map[string]schemas.ArmSpec) (out map[string]schemas.ArmSpec, freshArm string) {
	out = maps.Clone(arms)
	if out == nil {
		out = map[string]schemas.ArmSpec{}
	}
	if len(spec.Arms) != 1 {
		return out, ""
	}
	baseID := spec.Arms[0].Label
	base, ok := out[baseID]
	if !ok {
		return out, ""
	}
	first := verdict.ArmCandidate
	if baseID == verdict.ArmCandidate {
		first = verdict.ArmBaseline
	}
	added := 0
	add := func(id string, arm schemas.ArmSpec) string {
		if added == 0 {
			id = first
		}
		added++
		out[id] = arm
		return id
	}
	if spec.BifrostCache {
		add(schemas.EvaluationCacheArm, cacheOnArm(base))
	}
	if evalFailover(spec) { // lane C07: the agent again, with Bifrost fallbacks
		provider, model := failoverTarget(spec.Arms[0])
		if set, err := failover.RoutingSet(provider, model, *spec.Failover); err == nil {
			arm := withSet(base, set)
			arm.Label += failover.ArmLabelSuffix
			add(failover.ArmID, arm)
		}
	}
	if evalStreamControl(spec) {
		// Score mode with memory carried: a visible "<agent> (no memory)"
		// control runs the same tasks in the same order with empty memory,
		// for forward transfer. It is counted in the estimate and kept off
		// every leaderboard (schemas.StreamControlArm).
		freshArm = add(evalControlArm, streamControlArm(base))
	}
	return out, freshArm
}

// suiteHasMCPServer reports whether some task of st has a visible, proxied
// MCP tool server, the servers an MCP route through Bifrost serves (the
// planner's check, engine/bifrostfeatures.go suiteHasMCP).
func suiteHasMCPServer(st *schemas.Suite) bool {
	if st == nil {
		return false
	}
	mcp := func(services []schemas.ServiceSpec) bool {
		for _, s := range services {
			if s.Role == schemas.ServiceRoleMCP && !s.Hidden && utils.ServiceProxied(s) {
				return true
			}
		}
		return false
	}
	if mcp(utils.TaskServices(st, nil)) {
		return true
	}
	for _, t := range st.Tasks {
		if mcp(utils.TaskServices(nil, t)) {
			return true
		}
	}
	return false
}
