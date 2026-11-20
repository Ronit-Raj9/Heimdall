package gates

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// checkRouted is the model fidelity rule for a run whose arm declares a
// routing policy (W9, docs/routing-experiments.md). A Bifrost row records the
// model it sent upstream, which for a routed request is the routed model, so
// the requested model is the agent config's (the policy's first target). A
// served model the policy names is an expected switch: counted and reported,
// never a fire. The gate fires, as for any run, on a served model the policy
// does not name, a fallback row when the policy declares no fallback, and a
// cache hit. Runs without a policy keep the unrouted rule unchanged.
func (g *modelFidelityGate) checkRouted(requested string, pol *schemas.RoutingPolicy, ev *schemas.Evidence) *schemas.GateResult {
	allowed := pol.EffectiveModels(requested) // a rule's unmatched requests keep the agent's model
	var swapped, undeclared, cached, expected int
	var evidence []string
	for _, r := range ev.Requests.Requests {
		if r.Kind != "message" && r.Kind != "" {
			continue
		}
		bad := false
		switch {
		case r.ModelServed == "":
		case !anyModel(allowed, r.ModelServed):
			swapped++
			bad = true
		case requested != "" && !sameModel(requested, r.ModelServed):
			expected++
		}
		if r.FallbackIndex > 0 && len(pol.Fallbacks) == 0 {
			undeclared++
			bad = true
		}
		if r.CacheHit {
			cached++
			bad = true
		}
		if bad {
			evidence = append(evidence, r.ID)
		}
	}
	note := fmt.Sprintf("routing policy %s: %d request(s) served by another policy model than %s (expected switch)", pol.Strategy, expected, orNone(requested))
	if len(evidence) == 0 {
		if expected == 0 {
			return newResult(g, false, "", nil)
		}
		return newResult(g, false, note, nil)
	}
	detail := fmt.Sprintf("%d request(s): %d served by a model outside the routing policy, %d undeclared fallback, %d cache hit; %s",
		len(evidence), swapped, undeclared, cached, note)
	return newResult(g, true, detail, evidence)
}

// anyModel reports whether served is one of models.
func anyModel(models []string, served string) bool {
	for _, m := range models {
		if sameModel(m, served) {
			return true
		}
	}
	return false
}

// orNone names an empty model.
func orNone(m string) string {
	if m == "" {
		return "(none)"
	}
	return m
}
