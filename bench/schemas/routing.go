package schemas

import (
	"cmp"
	"fmt"
	"strings"
)

// W9 routing policy under test: the API views of what an arm's gateway policy
// was and what it did (handlers/routing.go, docs/routing-experiments.md).
// They are read from the frozen manifest and the sealed runs' metrics.json
// and evidence.json; nothing here is computed from data the bench did not
// witness.

// RoutingSummary is GET /experiments/{id}/routing: every arm's gateway
// policy, how the arms' policies differ, and per arm and condition what the
// policy did over the sealed counted runs.
type RoutingSummary struct {
	ExpID       string       `json:"exp_id"`
	Routed      bool         `json:"routed"`      // at least one arm declares gateway.routing
	Arms        []RoutingArm `json:"arms"`        // sorted by arm id
	Differences []string     `json:"differences"` // one line per policy field the arms disagree on
}

// RoutingArm is one arm's policy and its effect per condition.
type RoutingArm struct {
	Arm        string             `json:"arm"`
	Label      string             `json:"label"`
	Model      string             `json:"model"`                 // the model the agent asks for
	Policy     *RoutingPolicy     `json:"policy,omitempty"`      // nil: the bench's fixed single-model key
	PolicyHash string             `json:"policy_hash,omitempty"` // sha256 frozen in the manifest
	Summary    string             `json:"summary"`               // the policy in one line
	Conditions []RoutingCondition `json:"conditions"`            // sorted by condition id
}

// RoutingCondition is what an arm's policy did under one condition, over the
// sealed counted runs. A run whose metric was not observed is left out of
// that metric's count, never counted as 0.
type RoutingCondition struct {
	Condition     string             `json:"condition"`
	Runs          int                `json:"runs"`           // sealed counted runs
	FallbackRuns  int                `json:"fallback_runs"`  // runs in which a fallback row answered a request
	FallbackKnown int                `json:"fallback_known"` // runs whose fallback_used was observed
	Switches      int                `json:"switches"`       // served model switches summed over runs
	SwitchesKnown int                `json:"switches_known"` // runs whose served_model_switches was observed
	Targets       []RoutingTargetUse `json:"targets"`        // targets that answered turns, by share
}

// RoutingTargetUse is one target's share of answered turns and its spend.
type RoutingTargetUse struct {
	Target    string   `json:"target"`               // provider/model; the metric suffix when no policy names it
	TurnShare float64  `json:"turn_share"`           // mean over runs that reported targets
	CostUSD   *float64 `json:"cost_usd,omitempty"`   // summed priced spend; absent when a run's spend on it was unknown
	LatencyMS *float64 `json:"latency_ms,omitempty"` // mean of the runs' mean answered-row latency
}

// RunRouting is GET /runs/{id}/routing: one run's policy, the gateway rows
// that show which target served each request, and the model fidelity result.
type RunRouting struct {
	RunID        string             `json:"run_id"`
	Arm          string             `json:"arm"`
	Condition    string             `json:"condition"`
	Model        string             `json:"model"`
	Policy       *RoutingPolicy     `json:"policy,omitempty"`
	Summary      string             `json:"summary"`
	FallbackUsed *bool              `json:"fallback_used,omitempty"` // absent when not observed
	Switches     *int               `json:"switches,omitempty"`      // absent when not observed
	Targets      []RoutingTargetUse `json:"targets"`
	Rows         []RunRoutingRow    `json:"rows"`
	Fidelity     *GateResult        `json:"fidelity,omitempty"` // model_fidelity, when the experiment ran it
}

// RunRoutingRow is one Bifrost message row of a run: the target that served
// it and how Bifrost routed it.
type RunRoutingRow struct {
	ID              string   `json:"id"`
	ParentID        string   `json:"parent_id,omitempty"` // the primary row of a fallback
	Provider        string   `json:"provider"`
	ModelServed     string   `json:"model_served"`
	FallbackIndex   int      `json:"fallback_index"`
	Status          string   `json:"status"`
	ErrorStatus     int      `json:"error_status,omitempty"`
	LatencyMS       float64  `json:"latency_ms"`
	RoutingRuleName string   `json:"routing_rule_name,omitempty"`
	RoutingEngines  []string `json:"routing_engines,omitempty"`
}

// routingDefaultProvider is the provider a config without gateway.provider
// runs on (gateways/bifrost DefaultProvider).
const routingDefaultProvider = "anthropic"

// RoutingSummaryLine describes an agent config's gateway policy in one line.
func RoutingSummaryLine(cfg *AgentConfig) string {
	provider := cmp.Or(cfg.Gateway.Provider, routingDefaultProvider)
	pol := cfg.Gateway.Routing
	if pol == nil {
		return fmt.Sprintf("fixed %s/%s (no routing policy)", provider, cfg.Model)
	}
	target := func(t RouteTarget) string { return cmp.Or(t.Provider, provider) + "/" + t.Model }
	var parts []string
	for _, t := range pol.Targets {
		s := target(t)
		if len(pol.Targets) > 1 {
			s += fmt.Sprintf(" %g", t.Weight)
		}
		parts = append(parts, s)
	}
	line := pol.Strategy + ": " + strings.Join(parts, ", ")
	if pol.When != "" {
		line += " when " + pol.When
	}
	if len(pol.Fallbacks) > 0 {
		var fb []string
		for _, t := range pol.Fallbacks {
			fb = append(fb, target(t))
		}
		line += "; fallbacks " + strings.Join(fb, " then ")
	}
	if l := pol.Limits; l != nil {
		var lim []string
		if l.BudgetUSD > 0 {
			lim = append(lim, fmt.Sprintf("budget $%g per %s", l.BudgetUSD, cmp.Or(l.BudgetReset, "1d")))
		}
		if l.RequestMax > 0 {
			lim = append(lim, fmt.Sprintf("%d requests per %s", l.RequestMax, cmp.Or(l.RequestReset, "1h")))
		}
		if l.TokenMax > 0 {
			lim = append(lim, fmt.Sprintf("%d tokens per %s", l.TokenMax, cmp.Or(l.TokenReset, "1h")))
		}
		if len(lim) > 0 {
			line += "; limits " + strings.Join(lim, ", ")
		}
	}
	return line
}

// RoutingDifferences lists the policy fields the arms disagree on, one line
// per field with every arm's value, so an experiment's arms are described by
// their policy difference.
func RoutingDifferences(arms []RoutingArm) []string {
	if len(arms) < 2 {
		return []string{}
	}
	fields := []struct {
		name string
		get  func(a RoutingArm) string
	}{
		{"model", func(a RoutingArm) string { return a.Model }},
		{"strategy", func(a RoutingArm) string {
			if a.Policy == nil {
				return "none"
			}
			return a.Policy.Strategy
		}},
		{"targets", func(a RoutingArm) string { return routeList(a, false) }},
		{"fallbacks", func(a RoutingArm) string { return routeList(a, true) }},
		{"limits", func(a RoutingArm) string {
			if a.Policy == nil || a.Policy.Limits == nil {
				return "none"
			}
			l := a.Policy.Limits
			return fmt.Sprintf("budget %g, requests %d, tokens %d", l.BudgetUSD, l.RequestMax, l.TokenMax)
		}},
	}
	out := []string{}
	for _, f := range fields {
		vals := make([]string, 0, len(arms))
		differ := false
		for _, a := range arms {
			v := f.get(a)
			if len(vals) > 0 && v != f.get(arms[0]) {
				differ = true
			}
			vals = append(vals, a.Arm+" "+v)
		}
		if differ {
			out = append(out, f.name+": "+strings.Join(vals, "; "))
		}
	}
	return out
}

// routeList renders an arm's targets or fallbacks.
func routeList(a RoutingArm, fallbacks bool) string {
	if a.Policy == nil {
		if fallbacks {
			return "none"
		}
		return a.Model
	}
	ts := a.Policy.Targets
	if fallbacks {
		ts = a.Policy.Fallbacks
	}
	if len(ts) == 0 {
		return "none"
	}
	var parts []string
	for _, t := range ts {
		s := t.Model
		if t.Weight > 0 && len(ts) > 1 {
			s += fmt.Sprintf(" %g", t.Weight)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
