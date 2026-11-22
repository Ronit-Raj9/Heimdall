package handlers

import (
	"strings"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/schemas"
)

// evalFailover reports whether the evaluation runs the provider failover
// option (lane C07, package failover).
func evalFailover(spec schemas.EvaluationSpec) bool {
	return spec.Failover != nil && spec.Failover.Enabled && len(spec.Arms) == 1
}

// failoverTarget is the provider and model of the arm's primary model, a
// "<provider>/<model>" model naming its provider as armFor reads it.
func failoverTarget(a schemas.EvaluationArm) (provider, model string) {
	provider, model = strings.TrimSpace(a.Provider), strings.TrimSpace(a.Model)
	if provider == "" {
		if p, m := schemas.SplitProviderModel(model); p != "" {
			provider, model = p, m
		}
	}
	return provider, model
}

// normalizeFailover checks the failover option and adds its primary outage
// fault. An option that is off is dropped, so the saved spec stays as it was.
func normalizeFailover(spec *schemas.EvaluationSpec) error {
	o := spec.Failover
	if o == nil {
		return nil
	}
	if !o.Enabled {
		spec.Failover = nil
		return nil
	}
	if spec.Mode != schemas.EvaluationModeScore || len(spec.Arms) != 1 {
		return badf("provider failover runs on one agent (score mode); it adds its own second arm with fallbacks")
	}
	if spec.Stream != nil {
		return badf("provider failover cannot be combined with a task stream")
	}
	provider, model := failoverTarget(spec.Arms[0])
	if provider == "" || model == "" || provider == "openai-compatible" {
		return badf("provider failover needs the agent's model with its Bifrost provider, like openai/gpt-4o")
	}
	for i := range o.Fallbacks {
		o.Fallbacks[i].Model = strings.TrimSpace(o.Fallbacks[i].Model)
		o.Fallbacks[i].Provider = strings.TrimSpace(o.Fallbacks[i].Provider)
		if p, m := schemas.SplitProviderModel(o.Fallbacks[i].Model); p != "" && o.Fallbacks[i].Provider == "" {
			o.Fallbacks[i].Provider, o.Fallbacks[i].Model = p, m
		}
	}
	if err := failover.Validate(*o, provider, model); err != nil {
		return badf("provider failover: %v", err)
	}
	if !contains(spec.Faults, faults.PrimaryOutage) {
		spec.Faults = append(spec.Faults, faults.PrimaryOutage)
	}
	return nil
}
