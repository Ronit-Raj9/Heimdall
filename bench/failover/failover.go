// Package failover is the "Provider failover" evaluation option (lane C07).
//
// It tests whether Bifrost's own fallbacks carry an agent through an outage
// of its primary model. The evaluation runs three arms on the same task:
//
//   - clean: the primary model, no outage, no fallbacks (the baseline);
//   - failover: a hard outage of the primary model plus a Bifrost
//     fallback_chain routing rule (primary target, then the fallbacks);
//   - no-failover: the same outage with no fallbacks.
//
// The outage is injected by the heimdall Bifrost plugin when the gateway has
// it (an inline fault plan in the x-heimdall-fault-plan header), else by the
// fault proxy behind Bifrost (a persistent plan narrowed to the primary model,
// so fallback calls go through). Report compares the arms: did failover work,
// what it cost in latency and money, and task success with and without it.
//
// Fallbacks must stay on the run's provider: a Heimdall run key holds one
// provider key slot (gateways/bifrost/routing.go policyModels). It is pure:
// no I/O.
package failover

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Arm names.
const (
	ArmClean      = "clean"
	ArmFailover   = "failover"
	ArmNoFailover = "no-failover"
)

// Where the outage is injected.
const (
	ViaPlugin     = "heimdall-plugin"
	ViaFaultProxy = "fault-proxy"
)

// Header names of the heimdall Bifrost plugin (plugins/heimdall/context.go,
// lane C01). Kept as strings so bench does not import the plugin module.
const (
	HeaderArm         = "x-heimdall-arm"
	HeaderFaultPlanID = "x-heimdall-fault-plan-id"
	HeaderFaultPlan   = "x-heimdall-fault-plan"
)

// DefaultOutageStatus is the status the primary model answers during the
// outage: 503 is what Bifrost treats as a provider failure worth falling
// back on.
const DefaultOutageStatus = 503

// Option is the evaluation option as the UI and API send it.
type Option struct {
	Enabled bool `json:"enabled"`
	// Fallbacks are tried in order after the primary model fails. Provider
	// may be empty (the run's provider) but must not name another one.
	Fallbacks []schemas.RouteTarget `json:"fallbacks"`
	// OutageStatus is the status of the injected outage (default 503).
	OutageStatus int `json:"outage_status,omitempty"`
}

// Arm is the setup of one arm: the routing policy for the run key, the
// fault plan, and where the fault is applied.
type Arm struct {
	Name   string                 `json:"name"`
	Policy *schemas.RoutingPolicy `json:"policy,omitempty"` // nil: no routing rule (fixed)
	// Fault is nil on the clean arm.
	Fault *schemas.FaultPlan `json:"fault,omitempty"`
	Via   string             `json:"via,omitempty"`
	// Headers are sent on every model call of the arm through Bifrost (only
	// when Via is ViaPlugin, plus the arm tag).
	Headers map[string]string `json:"headers,omitempty"`
}

// Validate refuses an option that cannot test anything.
func (o Option) Validate(provider, primaryModel string) error {
	if !o.Enabled {
		return nil
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(primaryModel) == "" {
		return errors.New("provider failover needs the agent's provider and model")
	}
	if len(o.Fallbacks) == 0 {
		return errors.New("provider failover needs at least one fallback model")
	}
	if o.OutageStatus != 0 && (o.OutageStatus < 400 || o.OutageStatus > 599) {
		return fmt.Errorf("outage status must be an HTTP error status, got %d", o.OutageStatus)
	}
	seen := map[string]bool{bare(primaryModel): true}
	for _, f := range o.Fallbacks {
		if strings.TrimSpace(f.Model) == "" {
			return errors.New("a fallback has no model")
		}
		if f.Provider != "" && f.Provider != provider {
			return fmt.Errorf("fallback %s/%s is on another provider than the run's %s; a run key holds one provider, so pick a fallback model on %s", f.Provider, f.Model, provider, provider)
		}
		// The outage is narrowed by model name, so a fallback with the
		// primary's model would fail too and prove nothing.
		if seen[bare(f.Model)] {
			return fmt.Errorf("fallback model %s repeats the primary or another fallback", f.Model)
		}
		seen[bare(f.Model)] = true
	}
	return nil
}

// Plan returns the three arms in order clean, failover, no-failover.
// pluginPresent says the gateway runs the heimdall plugin; planID names the
// fault plan for tagging (the plugin logs it).
func Plan(provider, primaryModel string, o Option, pluginPresent bool, planID string) ([]Arm, error) {
	if !o.Enabled {
		return nil, errors.New("provider failover is off")
	}
	if err := o.Validate(provider, primaryModel); err != nil {
		return nil, err
	}
	status := o.OutageStatus
	if status == 0 {
		status = DefaultOutageStatus
	}
	outage := schemas.FaultPlan{Mode: "persistent", FromCall: 1, Status: status, Model: primaryModel}
	fallbacks := make([]schemas.RouteTarget, 0, len(o.Fallbacks))
	for _, f := range o.Fallbacks {
		fallbacks = append(fallbacks, schemas.RouteTarget{Provider: provider, Model: f.Model})
	}
	chain := &schemas.RoutingPolicy{
		Strategy:  schemas.RoutingFallbackChain,
		Targets:   []schemas.RouteTarget{{Provider: provider, Model: primaryModel, Weight: 1}},
		Fallbacks: fallbacks,
	}
	via := ViaFaultProxy
	if pluginPresent {
		via = ViaPlugin
	}
	mk := func(name string, pol *schemas.RoutingPolicy, fault bool) (Arm, error) {
		a := Arm{Name: name, Policy: pol, Headers: map[string]string{HeaderArm: name}}
		if !fault {
			return a, nil
		}
		f := outage
		a.Fault, a.Via = &f, via
		if via == ViaPlugin {
			raw, err := json.Marshal(pluginPlan{Mode: "error", Status: status, Provider: provider, Model: primaryModel, FromCall: 1})
			if err != nil {
				return Arm{}, err
			}
			a.Headers[HeaderFaultPlan] = string(raw)
			if planID != "" {
				a.Headers[HeaderFaultPlanID] = planID
			}
		}
		return a, nil
	}
	arms := make([]Arm, 0, 3)
	for _, spec := range []struct {
		name  string
		pol   *schemas.RoutingPolicy
		fault bool
	}{{ArmClean, nil, false}, {ArmFailover, chain, true}, {ArmNoFailover, nil, true}} {
		a, err := mk(spec.name, spec.pol, spec.fault)
		if err != nil {
			return nil, err
		}
		arms = append(arms, a)
	}
	return arms, nil
}

// pluginPlan is the inline plan the heimdall plugin reads: an error outage
// scoped to the primary provider and model, from the first call.
// TODO(C01): align field names with plugins/heimdall Plan once it lands.
type pluginPlan struct {
	Mode     string `json:"mode"`
	Status   int    `json:"status"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	FromCall int    `json:"from_call,omitempty"`
}

func bare(model string) string {
	if _, m, ok := strings.Cut(model, "/"); ok {
		return m
	}
	return model
}
