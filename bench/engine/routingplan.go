package engine

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Routing policy under test (W9, docs/routing-experiments.md): the planner
// refuses a policy the gateway cannot apply to one run's key, and freezes a
// hash of every arm's policy in the manifest so the report can describe the
// arms by their policy difference.

// routeWeightTolerance is Bifrost's tolerance for target weights summing to 1
// (transports/bifrost-http/handlers/routing.go validateRoutingTargets).
const routeWeightTolerance = 0.001

// routeDurationRe is the reset duration syntax Bifrost's governance accepts
// for budgets and rate limits (handlers/governance.go: 30s, 5m, 1h, 1d, 1w,
// 1M, 1Q).
var routeDurationRe = regexp.MustCompile(`^[1-9][0-9]*(s|m|h|d|w|M|Q|Y)$`)

// checkRouting validates the routing policy of every arm (and of the
// compatibility agent) and returns arm id -> sha256 of the canonical policy,
// nil when no arm declares one, so a plan without routing keeps its hash.
func checkRouting(m *schemas.Manifest) (map[string]string, error) {
	arms := make(map[string]schemas.ArmManifest, len(m.Arms)+1)
	for id, a := range m.Arms {
		arms[id] = a
	}
	if m.Compatibility != nil {
		arms[CompatArm] = *m.Compatibility
	}
	ids := make([]string, 0, len(arms))
	for id := range arms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out map[string]string
	var errs []error
	for _, id := range ids {
		cfg := arms[id].Config
		pol := cfg.Gateway.Routing
		if pol == nil {
			continue
		}
		if err := ValidateRouting(&cfg, m.Gateway); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", id, err))
			continue
		}
		h, err := utils.HashJSON(pol)
		if err != nil {
			return nil, fmt.Errorf("hash routing policy of arm %s: %w", id, err)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[id] = h
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("routing policy: %w", err)
	}
	return out, nil
}

// ValidateRouting reports every reason the agent config's routing policy
// cannot run as declared. gm, the gateway preflight, may be nil; then a
// policy that names retries is refused, because they cannot be checked.
//
// Rules: the policy's targets and fallbacks are on the agent's gateway
// provider (a run key holds one key slot); the first target is the model the
// agent asks for, so a fault that names no model and the fidelity gate know
// which model is primary; weights follow Bifrost's rule (each above 0,
// summing to 1); retries, which Bifrost sets per provider and not per key or
// rule, must equal the gateway's; limits use Bifrost's duration syntax.
func ValidateRouting(cfg *schemas.AgentConfig, gm *schemas.GatewayManifest) error {
	pol := cfg.Gateway.Routing
	if pol == nil {
		return nil
	}
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	provider := cfg.Gateway.Provider
	if provider == "" {
		bad("gateway.provider is required with gateway.routing")
	}
	errs = append(errs, routeShape(pol)...)
	for _, t := range append(append([]schemas.RouteTarget{}, pol.Targets...), pol.Fallbacks...) {
		if t.Model == "" {
			bad("every target and fallback needs a model")
		}
		if t.Provider != "" && provider != "" && t.Provider != provider {
			bad("%s/%s is not on the gateway provider %s: a run key holds one key slot", t.Provider, t.Model, provider)
		}
	}
	if p, ok := pol.Primary(); ok && !sameRouteModel(cfg.Model, p.Model) && !complexityRule(pol) { // B2: a complexity_tier rule routes matched requests to another model by design (bifrostfeatures.go)
		bad("the first target %s must be the model the agent asks for (model: %s)", p.Model, orDash(cfg.Model))
	}
	if pol.Retries != nil {
		errs = append(errs, routeRetries(*pol.Retries, provider, gm)...)
	}
	errs = append(errs, routeLimits(pol.Limits)...)
	if len(pol.Fallbacks) > 0 && len(cfg.Gateway.Fallbacks) > 0 {
		bad("gateway.fallbacks and gateway.routing.fallbacks cannot both be set")
	}
	return errors.Join(errs...)
}

// routeShape checks the strategy's target and fallback counts and weights.
func routeShape(pol *schemas.RoutingPolicy) []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	n, f := len(pol.Targets), len(pol.Fallbacks)
	switch pol.Strategy {
	case schemas.RoutingFixed:
		if n != 1 || f != 0 {
			bad("strategy fixed takes one target and no fallbacks, got %d and %d", n, f)
		}
	case schemas.RoutingWeighted:
		if n < 2 {
			bad("strategy weighted takes at least two targets, got %d", n)
		}
	case schemas.RoutingFallbackChain:
		if n != 1 || f < 1 {
			bad("strategy fallback_chain takes one target and at least one fallback, got %d and %d", n, f)
		}
	case schemas.RoutingRule:
		if n < 1 || strings.TrimSpace(pol.When) == "" {
			bad("strategy rule takes at least one target and a when condition")
		}
	default:
		bad("strategy must be fixed, weighted, fallback_chain or rule, got %q", pol.Strategy)
	}
	if pol.When != "" && pol.Strategy != schemas.RoutingRule {
		bad("when is only read by strategy rule")
	}
	if n > 1 || pol.Strategy == schemas.RoutingWeighted {
		total := 0.0
		for _, t := range pol.Targets {
			if !(t.Weight > 0) {
				bad("target %s needs a weight above 0", t.Model)
			}
			total += t.Weight
		}
		if math.Abs(total-1) > routeWeightTolerance {
			bad("target weights sum to %s; Bifrost needs 1", strconv.FormatFloat(total, 'g', -1, 64))
		}
	}
	errs = append(errs, routeDuplicates("target", pol.Targets)...)
	return append(errs, routeDuplicates("fallback", pol.Fallbacks)...)
}

// routeDuplicates refuses a target listed twice (Bifrost refuses duplicate
// rule targets; a repeated fallback would only retry the same target).
func routeDuplicates(what string, ts []schemas.RouteTarget) []error {
	seen := map[string]bool{}
	var errs []error
	for _, t := range ts {
		k := strings.ToLower(t.Provider + "|" + t.Model)
		if seen[k] {
			errs = append(errs, fmt.Errorf("%s %s is listed twice", what, t.Model))
		}
		seen[k] = true
	}
	return errs
}

// routeRetries checks a declared retry count against the gateway's provider
// max_retries recorded at preflight.
func routeRetries(retries int, provider string, gm *schemas.GatewayManifest) []error {
	if gm == nil {
		return []error{errors.New("retries cannot be checked without a gateway preflight")}
	}
	got, ok := gm.Settings[provider+".max_retries"]
	if !ok {
		got, ok = gm.Settings["max_retries"]
	}
	if !ok {
		return []error{fmt.Errorf("the gateway preflight recorded no max_retries for %s", provider)}
	}
	if got != strconv.Itoa(retries) {
		return []error{fmt.Errorf("retries %d differs from the gateway's provider max_retries %s: Bifrost sets retries per provider, so one run cannot change them without changing every concurrent run", retries, got)}
	}
	return nil
}

// routeLimits checks the governance limits' values and durations.
func routeLimits(l *schemas.RouteLimits) []error {
	if l == nil {
		return nil
	}
	var errs []error
	check := func(name, d string) {
		if d != "" && !routeDurationRe.MatchString(d) {
			errs = append(errs, fmt.Errorf("limits.%s %q is not a Bifrost duration (30s, 5m, 1h, 1d, 1w, 1M)", name, d))
		}
	}
	check("budget_reset", l.BudgetReset)
	check("request_reset", l.RequestReset)
	check("token_reset", l.TokenReset)
	if l.BudgetUSD < 0 || l.RequestMax < 0 || l.TokenMax < 0 {
		errs = append(errs, errors.New("limits cannot be negative"))
	}
	return errs
}

// sameRouteModel compares model ids ignoring one leading provider segment.
func sameRouteModel(a, b string) bool {
	trim := func(m string) string {
		if _, rest, ok := strings.Cut(m, "/"); ok {
			return rest
		}
		return m
	}
	return a != "" && (a == b || trim(a) == trim(b))
}

// orDash names an empty value.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
