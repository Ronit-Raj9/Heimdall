package bifrost

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Management routes the hygiene check reads (Bifrost v2.5.1,
// transports/bifrost-http/handlers). Model routers are newer than v2.5.1; a
// 404 there means the gateway has none.
const (
	pathMCPClients         = "/api/mcp/clients"
	pathPlugins            = "/api/plugins"
	pathPluginsLoaded      = "/api/plugins/loaded"
	pathRoutingRules       = "/api/routing/rules"
	pathRoutingRulesLegacy = "/api/governance/routing-rules"
	pathModelRouters       = "/api/routing/model-routers"

	pluginSemanticCache = "semantic_cache"
)

// routingRule is the part of a routing rule that decides whether it can send
// a request to another target (framework/configstore/tables/routingrules.go).
type routingRule struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Enabled   *bool             `json:"enabled"` // nil means enabled, the database default
	Scope     string            `json:"scope"`
	ScopeID   *string           `json:"scope_id"` // W9: the virtual key a bench run rule is scoped to
	Targets   []routingTarget   `json:"targets"`
	Fallbacks []json.RawMessage `json:"fallbacks"`
}

// routingTarget is one weighted target of a routing rule. A nil provider or
// model keeps the request's own.
type routingTarget struct {
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	KeyID    *string `json:"key_id"`
	Weight   float64 `json:"weight"`
}

// modelRouter is the part of a model router the check reads.
type modelRouter struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Routing struct {
		Enabled bool `json:"enabled"`
	} `json:"routing"`
}

// hygieneFindings collects the hygiene record while preflight runs.
type hygieneFindings struct {
	h *schemas.GatewayHygiene
}

// violate records one violation.
func (f *hygieneFindings) violate(format string, args ...any) {
	f.h.Violations = append(f.h.Violations, fmt.Sprintf(format, args...))
}

// retarget records a config that can send a model to another target, and the
// violation it is.
func (f *hygieneFindings) retarget(what string) {
	f.h.MultiTarget = append(f.h.MultiTarget, what)
	f.violate("%s can send a run's model to another target", what)
}

// checkHygiene reads the live gateway features that could change what a run's
// model requests carry or where they go (DESIGN.md 13.5), after checkConfig
// and checkProviders filled their part: MCP tools auto-injected while MCP
// clients exist, the semantic cache plugin, and routing rules or model
// routers that can send a request to another provider, model or key. A
// feature that cannot be read is a violation too, because hygiene that was
// not shown is not assumed. loaded is GET /api/plugins/loaded, or nil with
// loadedErr set when that read failed.
func (g *Gateway) checkHygiene(ctx context.Context, m *schemas.GatewayManifest, f *hygieneFindings, loaded []string, loadedErr error) {
	f.h.MCPAutoToolInjectOff = m.Settings[SettingAutoToolOff] == "true"
	g.checkMCP(ctx, f)
	g.checkPlugins(ctx, f, loaded, loadedErr)
	g.checkRoutingRules(ctx, f)
	g.checkModelRouters(ctx, f)
}

// checkMCP counts the configured MCP clients. With auto-injection on, every
// chat request would carry the clients' tools, so the arms would not see the
// tools their agent configs declare.
func (g *Gateway) checkMCP(ctx context.Context, f *hygieneFindings) {
	var page struct {
		TotalCount int `json:"total_count"`
	}
	if err := g.call(ctx, http.MethodGet, pathMCPClients, nil, &page); err != nil {
		f.violate("MCP clients could not be read: %v", err)
		return
	}
	f.h.MCPClients = page.TotalCount
	if page.TotalCount > 0 && !f.h.MCPAutoToolInjectOff {
		f.violate("MCP tool auto-inject is on (client_config.mcp_disable_auto_tool_inject is not true) while %d MCP client(s) exist: every request would carry the gateway's tools",
			page.TotalCount)
	}
}

// checkPlugins flags the semantic cache when it is loaded, or enabled in the
// plugin config so that it loads on the next restart. A cached answer is not
// a model call: it has no cost and no fresh output.
func (g *Gateway) checkPlugins(ctx context.Context, f *hygieneFindings, loaded []string, loadedErr error) {
	if loadedErr != nil {
		f.violate("loaded plugins could not be read: %v", loadedErr)
	} else if slices.Contains(loaded, pluginSemanticCache) {
		f.h.SemanticCache = true
		f.violate("semantic cache plugin %s is loaded", pluginSemanticCache)
	}
	var rows struct {
		Plugins []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"plugins"`
	}
	if err := g.call(ctx, http.MethodGet, pathPlugins, nil, &rows); err != nil {
		f.violate("plugin config could not be read: %v", err)
		return
	}
	for _, p := range rows.Plugins {
		if p.Name == pluginSemanticCache && p.Enabled && !f.h.SemanticCache {
			f.h.SemanticCache = true
			f.violate("semantic cache plugin %s is enabled in the plugin config", pluginSemanticCache)
		}
	}
}

// checkRoutingRules flags every enabled routing rule that can move a request:
// more than one target, fallbacks, or a target that pins a provider, model or
// key. The check cannot evaluate a rule's CEL expression, so a rule that could
// match a bench run counts whatever its scope.
func (g *Gateway) checkRoutingRules(ctx context.Context, f *hygieneFindings) {
	var page struct {
		Rules []routingRule `json:"rules"`
	}
	err := g.call(ctx, http.MethodGet, pathRoutingRules, nil, &page)
	if isStatus(err, http.StatusNotFound) {
		err = g.call(ctx, http.MethodGet, pathRoutingRulesLegacy, nil, &page)
	}
	switch {
	case isStatus(err, http.StatusNotFound):
		return // a gateway without routing rules
	case err != nil:
		f.violate("routing rules could not be read: %v", err)
		return
	}
	for _, r := range page.Rules {
		if r.Enabled != nil && !*r.Enabled {
			continue
		}
		if benchRule(r) { // W9: a run's own VK-scoped policy rule (routing.go) is expected, recorded, never a violation
			f.h.BenchRouting = append(f.h.BenchRouting, fmt.Sprintf("routing rule %s (id %s, scope virtual_key %s)", r.Name, r.ID, derefOr(r.ScopeID, "?")))
			continue
		}
		f.h.RoutingRules++
		if what, moves := ruleMoves(r); moves {
			f.retarget(what)
		}
	}
}

// checkModelRouters flags every model router with routing enabled. Bifrost
// v2.5.1 has no model routers and answers 404.
func (g *Gateway) checkModelRouters(ctx context.Context, f *hygieneFindings) {
	var page struct {
		Routers []modelRouter `json:"routers"`
	}
	err := g.call(ctx, http.MethodGet, pathModelRouters, nil, &page)
	switch {
	case isStatus(err, http.StatusNotFound):
		return
	case err != nil:
		f.violate("model routers could not be read: %v", err)
		return
	}
	for _, r := range page.Routers {
		if r.Routing.Enabled {
			f.retarget(fmt.Sprintf("model router %s (id %s)", r.Name, r.ID))
		}
	}
}

// checkProviderHygiene flags a bench provider that injects tools into every
// request (injected_tools, which mcp_disable_auto_tool_inject does not gate)
// and a bench key slot with model aliases, which send another wire model than
// the one the run asked for.
func checkProviderHygiene(f *hygieneFindings, provider string, cfg providerConfig, slots []Slot, keys []providerKey) {
	if n := len(cfg.InjectedTools); n > 0 {
		f.violate("provider %s injects %d tool(s) into every request (injected_tools)", provider, n)
	}
	for _, k := range keys {
		if len(k.Aliases) == 0 || !slices.ContainsFunc(slots, func(s Slot) bool { return s.Name == k.Name }) {
			continue
		}
		models := make([]string, 0, len(k.Aliases))
		for model := range k.Aliases {
			models = append(models, model)
		}
		slices.Sort(models)
		f.retarget(fmt.Sprintf("provider %s key slot %s with model aliases for %s", provider, k.Name, strings.Join(models, ", ")))
	}
}

// ruleMoves describes an enabled rule that can send a request to another
// target, and reports whether it can.
func ruleMoves(r routingRule) (string, bool) {
	pins := 0
	weights := make([]string, 0, len(r.Targets))
	for _, t := range r.Targets {
		if t.Provider != nil || t.Model != nil || t.KeyID != nil {
			pins++
		}
		weights = append(weights, strconv.FormatFloat(t.Weight, 'g', -1, 64))
	}
	if len(r.Targets) <= 1 && len(r.Fallbacks) == 0 && pins == 0 {
		return "", false
	}
	scope := r.Scope
	if scope == "" {
		scope = "global"
	}
	return fmt.Sprintf("routing rule %s (id %s, scope %s, %d target(s) weighted %s, %d pinned, %d fallback(s))",
		r.Name, r.ID, scope, len(r.Targets), strings.Join(weights, "/"), pins, len(r.Fallbacks)), true
}
