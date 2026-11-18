package bifrost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Routing policy under test (W9, docs/routing-experiments.md).
//
// A run whose agent config declares gateway.routing gets a virtual key that
// allows every model of the policy on the run's one key slot, with the
// policy's governance limits, and, for every strategy but fixed, one Bifrost
// routing rule scoped to that key (scope virtual_key, scope_id the key id).
// Bifrost v2.5.1 evaluates a virtual_key-scoped rule only for requests that
// key authenticates (plugins/routing/rules/engine.go buildScopeChain), so the
// rule never reaches another run or another client. The bench never creates
// a global rule. The rule is deleted before the key at cleanup, and the
// janitor deletes rules of runs that are no longer active.
const (
	routeRulePrefix    = "heimdall-route-"
	routeRuleScope     = "virtual_key"
	routeDefaultWhen   = "true"
	routeBudgetReset   = "1d"
	routeRateReset     = "1h"
	routeWeightEpsilon = 0.001 // Bifrost's tolerance for target weights summing to 1 (handlers/routing.go)
)

// routeRulePattern matches the names of the routing rules MintKey creates and
// captures the run id.
var routeRulePattern = regexp.MustCompile(`^` + routeRulePrefix + `(r_[a-z0-9]+)$`)

// RouteRuleName is the name of the routing rule the bench creates for runID.
// The run id is the name of the run's virtual key, so the rule and the key
// can be matched without the bench's memory.
func RouteRuleName(runID string) string { return routeRulePrefix + runID }

// routeRuleBody is POST /api/routing/rules as Bifrost v2.5.1 reads it
// (transports/bifrost-http/handlers/routing.go CreateRoutingRuleRequest).
// Fallbacks use the legacy "provider/model" string form that
// configstore/tables.RoutingFallback accepts.
type routeRuleBody struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Enabled       bool              `json:"enabled"`
	CelExpression string            `json:"cel_expression"`
	Targets       []routeRuleTarget `json:"targets"`
	Fallbacks     []string          `json:"fallbacks,omitempty"`
	Scope         string            `json:"scope"`
	ScopeID       string            `json:"scope_id"`
	Priority      int               `json:"priority"`
}

// routeRuleTarget is one weighted target of routeRuleBody. Provider and model
// are always set, so no target keeps the request's own and governance load
// balancing is skipped (plugins/governance/main.go LoadBalanceProvider: a
// request whose provider is already set is not balanced and gains no
// fallbacks of its own).
type routeRuleTarget struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Weight   float64 `json:"weight"`
}

// routedKeyBody is the virtual key body for a routed run: one provider config
// on the run's provider and key slot that allows every model of the policy
// (governance refuses any routed attempt whose model the key does not allow),
// plus the policy's budget and rate limit (handlers/governance.go
// CreateVirtualKeyRequest). It is pure.
func routedKeyBody(runID, provider, keyID string, pol *schemas.RoutingPolicy) (map[string]any, error) {
	models, err := policyModels(provider, pol)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"name": runID,
		"provider_configs": []map[string]any{{
			"provider":       provider,
			"weight":         1,
			"allowed_models": models,
			"key_ids":        []string{keyID},
		}},
		"is_active": true,
	}
	if l := pol.Limits; l != nil {
		if l.BudgetUSD > 0 {
			body["budgets"] = []map[string]any{{"max_limit": l.BudgetUSD, "reset_duration": orDefault(l.BudgetReset, routeBudgetReset)}}
		}
		rate := map[string]any{}
		if l.RequestMax > 0 {
			rate["request_max_limit"] = l.RequestMax
			rate["request_reset_duration"] = orDefault(l.RequestReset, routeRateReset)
		}
		if l.TokenMax > 0 {
			rate["token_max_limit"] = l.TokenMax
			rate["token_reset_duration"] = orDefault(l.TokenReset, routeRateReset)
		}
		if len(rate) > 0 {
			body["rate_limit"] = rate
		}
	}
	return body, nil
}

// routeRule translates a policy into the run's routing rule, or nil for the
// fixed strategy, which needs none. Every target and fallback must be on the
// run's provider: the key has one slot. It is pure.
func routeRule(runID, vkID, provider string, pol *schemas.RoutingPolicy) (*routeRuleBody, error) {
	if pol == nil || pol.Strategy == schemas.RoutingFixed {
		return nil, nil
	}
	if _, err := policyModels(provider, pol); err != nil {
		return nil, err
	}
	rule := &routeRuleBody{
		Name:          RouteRuleName(runID),
		Description:   "Heimdall routing policy under test for run " + runID + " (" + pol.Strategy + "); deleted at run cleanup",
		Enabled:       true,
		CelExpression: routeDefaultWhen,
		Scope:         routeRuleScope,
		ScopeID:       vkID,
	}
	if pol.Strategy == schemas.RoutingRule && strings.TrimSpace(pol.When) != "" {
		rule.CelExpression = strings.TrimSpace(pol.When)
	}
	total := 0.0
	for _, t := range pol.Targets {
		w := t.Weight
		if len(pol.Targets) == 1 && w == 0 {
			w = 1
		}
		total += w
		rule.Targets = append(rule.Targets, routeRuleTarget{Provider: orDefault(t.Provider, provider), Model: t.Model, Weight: w})
	}
	if math.Abs(total-1) > routeWeightEpsilon {
		return nil, fmt.Errorf("routing policy %s: target weights sum to %g, Bifrost needs 1", pol.Strategy, total)
	}
	for _, f := range pol.Fallbacks {
		rule.Fallbacks = append(rule.Fallbacks, orDefault(f.Provider, provider)+"/"+f.Model)
	}
	return rule, nil
}

// policyModels returns the models a routed key must allow, targets then
// fallbacks, and refuses a policy the key cannot carry: no target, a target
// without a model, or a provider other than the run's.
func policyModels(provider string, pol *schemas.RoutingPolicy) ([]string, error) {
	if pol == nil {
		return nil, errors.New("routing policy is nil")
	}
	if len(pol.Targets) == 0 {
		return nil, fmt.Errorf("routing policy %s has no target", pol.Strategy)
	}
	for _, t := range append(append([]schemas.RouteTarget{}, pol.Targets...), pol.Fallbacks...) {
		if t.Model == "" {
			return nil, fmt.Errorf("routing policy %s: a target has no model", pol.Strategy)
		}
		if t.Provider != "" && t.Provider != provider {
			return nil, fmt.Errorf("routing policy %s: target %s/%s is not on the run's provider %s (a run key holds one key slot)", pol.Strategy, t.Provider, t.Model, provider)
		}
	}
	return pol.Models(), nil
}

// applyRouteRule creates the routing rule a routed run needs on its freshly
// minted key and records its id on the key, so RevokeKey deletes it. A nil
// policy and the fixed strategy need no rule.
func (g *Gateway) applyRouteRule(ctx context.Context, key *schemas.RunKey, provider string, pol *schemas.RoutingPolicy) error {
	rule, err := routeRule(key.Name, key.ID, provider, pol)
	if err != nil || rule == nil {
		return err
	}
	id, err := g.createRouteRule(ctx, rule)
	if err != nil {
		return err
	}
	key.RoutingRuleID = id
	g.logger.Info("routing rule created", "run_id", key.Name, "virtual_key_id", key.ID, "routing_rule_id", id,
		"strategy", pol.Strategy, "targets", len(rule.Targets), "fallbacks", len(rule.Fallbacks))
	return nil
}

// createRouteRule creates the run's routing rule and returns its id.
func (g *Gateway) createRouteRule(ctx context.Context, rule *routeRuleBody) (string, error) {
	var out struct {
		Rule struct {
			ID string `json:"id"`
		} `json:"rule"`
	}
	if err := g.call(ctx, http.MethodPost, pathRoutingRules, rule, &out); err != nil {
		return "", fmt.Errorf("create routing rule %s: %w", rule.Name, err)
	}
	if out.Rule.ID == "" {
		return "", fmt.Errorf("create routing rule %s: bifrost returned no id", rule.Name)
	}
	return out.Rule.ID, nil
}

// deleteRouteRule deletes a routing rule; one already gone counts as deleted.
func (g *Gateway) deleteRouteRule(ctx context.Context, id string) error {
	err := g.call(ctx, http.MethodDelete, pathRoutingRules+"/"+url.PathEscape(id), nil, nil)
	if err == nil || isStatus(err, http.StatusNotFound) {
		return nil
	}
	return fmt.Errorf("delete routing rule %s: %w", id, err)
}

// revokeStaleRules deletes every bench routing rule whose run is not in
// active, and returns how many it deleted. A gateway without the routing
// rules API has none.
func (g *Gateway) revokeStaleRules(ctx context.Context, active map[string]bool) (int, error) {
	var page struct {
		Rules []routingRule `json:"rules"`
	}
	err := g.call(ctx, http.MethodGet, pathRoutingRules, nil, &page)
	if isStatus(err, http.StatusNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list routing rules: %w", err)
	}
	n := 0
	var errs []error
	for _, r := range page.Rules {
		m := routeRulePattern.FindStringSubmatch(r.Name)
		if m == nil || r.Scope != routeRuleScope || active[m[1]] {
			continue
		}
		if err := g.deleteRouteRule(ctx, r.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
		g.logger.Info("stale routing rule deleted", "routing_rule_id", r.ID, "name", r.Name)
	}
	return n, errors.Join(errs...)
}

// benchRule reports whether an enabled rule is one the bench created for a
// single run: its name is RouteRuleName of a run id and it is scoped to one
// virtual key, so Bifrost applies it to that key's requests only.
func benchRule(r routingRule) bool {
	return routeRulePattern.MatchString(r.Name) && r.Scope == routeRuleScope
}

// orDefault returns s, or def when s is empty.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// derefOr returns *p, or def when p is nil or empty.
func derefOr(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}
