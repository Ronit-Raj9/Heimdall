package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B2 Bifrost features under test (docs/bifrost-features.md). An arm's
// gateway.bifrost turns on, for that arm's runs only, a Bifrost feature that
// v2.5.1 applies per virtual key or per request: MCP through Bifrost's MCP
// gateway (classic or Code Mode clients, created per run and granted to the
// run's key only), auto prompt caching and the semantic cache (headers the
// agent sends). The planner refuses what the gateway or the run cannot do
// and freezes a hash of every arm's settings in the manifest; the pipeline
// creates and removes the run's MCP clients.

var (
	cacheTTLRe = regexp.MustCompile(`^[1-9][0-9]*(s|m|h|d)$`)
	bearerRe   = regexp.MustCompile(`Bearer [A-Za-z0-9._~+/=-]+`)
)

// bifrostPromptCacheProbe is implemented by gateways that can say whether a
// provider has a prompt_cache block (gateways/bifrost/bifrostcache.go).
// Bifrost honours x-bf-prompt-cache-auto-inject only on such a provider.
type bifrostPromptCacheProbe interface {
	PromptCacheConfigured(ctx context.Context, provider string) (bool, error)
}

// bifrostComplexityProbe is implemented by gateways that report the state of
// Bifrost's complexity analyzer (gateways/bifrost/bifrostcache.go).
type bifrostComplexityProbe interface {
	ComplexityAnalyzerState(ctx context.Context) (string, error)
}

// checkComplexityRouting refuses an arm whose W9 routing rule reads
// complexity_tier while the gateway's complexity analyzer produces no tier:
// on v2.5.1 such a rule never matches, so the arm would silently run the
// agent's own model and test nothing. The classifier itself is gateway-wide;
// only the rule is the arm's (scoped to the run's key).
func (p *Planner) checkComplexityRouting(ctx context.Context, cfg *schemas.AgentConfig) error {
	pol := cfg.Gateway.Routing
	if pol == nil || !strings.Contains(pol.When, "complexity_tier") || p.deferGateway {
		// A hosted plan has no gateway: the runner's replan, which has one,
		// checks the analyzer before any slot runs (audit 8-B-04).
		return nil
	}
	probe, ok := schemas.GatewayAs[bifrostComplexityProbe](p.reg.Gateway)
	if !ok {
		return errors.New("routing.when reads complexity_tier, but the gateway cannot report its complexity analyzer")
	}
	state, err := probe.ComplexityAnalyzerState(ctx)
	switch {
	case err != nil:
		return fmt.Errorf("routing.when reads complexity_tier: read the complexity analyzer: %w", err)
	case state == "disabled" || state == "absent":
		return fmt.Errorf("routing.when reads complexity_tier, but the gateway's complexity analyzer is %s, so no request gets a tier and the rule never matches; configure a classifier (gateway-wide) first", state)
	}
	return nil
}

// checkBifrostFeatures validates every arm's gateway.bifrost (and the
// compatibility agent's) against the suite and the gateway, and returns arm
// id -> sha256 of the canonical settings; nil when no arm declares any, so
// other plans keep their hash.
func (p *Planner) checkBifrostFeatures(ctx context.Context, m *schemas.Manifest, suite *schemas.Suite) (map[string]string, error) {
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
		if err := p.checkComplexityRouting(ctx, &cfg); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", id, err))
		}
		f := cfg.Gateway.Bifrost
		if f == nil {
			continue
		}
		if err := p.validateBifrost(ctx, &cfg, suite); err != nil {
			errs = append(errs, fmt.Errorf("arm %s: %w", id, err))
			continue
		}
		h, err := utils.HashJSON(f)
		if err != nil {
			return nil, fmt.Errorf("hash gateway.bifrost of arm %s: %w", id, err)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[id] = h
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("gateway.bifrost: %w", err)
	}
	return out, nil
}

// validateBifrost reports every reason an agent config's gateway.bifrost
// cannot run as declared.
func (p *Planner) validateBifrost(ctx context.Context, cfg *schemas.AgentConfig, suite *schemas.Suite) error {
	f := cfg.Gateway.Bifrost
	var errs []error
	if f.MCP == nil && f.PromptCache == "" && f.SemanticCache == nil {
		errs = append(errs, errors.New("gateway.bifrost sets nothing: name mcp, prompt_cache or semantic_cache, or remove it"))
	}
	if f.MCP != nil {
		errs = append(errs, validateBifrostMCP(cfg, suite)...)
	}
	if f.PromptCache != "" || f.SemanticCache != nil {
		if reason := headerlessAgent(cfg); reason != "" {
			errs = append(errs, fmt.Errorf("prompt_cache and semantic_cache are request headers the agent must send, and %s", reason))
		}
	}
	switch f.PromptCache {
	case "":
	case schemas.PromptCacheAutoInject, schemas.PromptCacheOff:
		if err := p.checkPromptCacheProvider(ctx, cfg.Gateway.Provider); err != nil {
			errs = append(errs, err)
		}
	default:
		errs = append(errs, fmt.Errorf("prompt_cache %q is not auto_inject or off", f.PromptCache))
	}
	if sc := f.SemanticCache; sc != nil {
		switch sc.Mode {
		case schemas.SemanticCacheDirect:
			if sc.Threshold != 0 {
				errs = append(errs, errors.New("semantic_cache.threshold applies to mode semantic only"))
			}
		case schemas.SemanticCacheSemantic:
			if sc.Threshold < 0 || sc.Threshold > 1 {
				errs = append(errs, fmt.Errorf("semantic_cache.threshold %g is outside (0, 1]", sc.Threshold))
			}
		default:
			errs = append(errs, fmt.Errorf("semantic_cache.mode %q is not direct or semantic", sc.Mode))
		}
		switch sc.Scope {
		case schemas.SemanticCacheScopeTask, schemas.SemanticCacheScopeArm, schemas.SemanticCacheScopeExperiment:
		default:
			errs = append(errs, fmt.Errorf("semantic_cache.scope %q is not task, arm or experiment", sc.Scope))
		}
		if sc.TTL != "" && !cacheTTLRe.MatchString(sc.TTL) {
			errs = append(errs, fmt.Errorf("semantic_cache.ttl %q is not a duration like 30s, 5m, 1h or 1d", sc.TTL))
		}
	}
	return errors.Join(errs...)
}

// validateBifrostMCP lists what makes MCP through Bifrost impossible for the
// config: an agent that cannot reach the gateway, a team, a remote agent, or
// a suite with no MCP server to serve.
func validateBifrostMCP(cfg *schemas.AgentConfig, suite *schemas.Suite) []error {
	var errs []error
	switch {
	case cfg.Adapter == adapters.AdapterScripted && cfg.Scripted != nil && cfg.Scripted.Kind != adapters.ScriptedToy && cfg.Scripted.Kind != adapters.ScriptedMCPLoop:
		errs = append(errs, fmt.Errorf("mcp: the scripted control %s runs without the gateway network, so it cannot reach Bifrost's MCP endpoint (only toy and mcploop can)", cfg.Scripted.Kind))
	case cfg.Remote != nil:
		errs = append(errs, errors.New("mcp: a remote agent reaches the run's tools through its tunnel, not through the gateway's MCP endpoint; not built"))
	}
	if len(cfg.Team) > 0 {
		errs = append(errs, errors.New("mcp: team runs give every member its own key; serving MCP through Bifrost to a team is not built"))
	}
	if suite != nil && !suiteHasMCP(suite) {
		errs = append(errs, fmt.Errorf("mcp: no task of suite %s has a visible MCP service to serve through Bifrost", suite.ID))
	}
	return errs
}

// suiteHasMCP reports whether some task of the suite has a visible, proxied
// service of role mcp.
func suiteHasMCP(suite *schemas.Suite) bool {
	for _, t := range suite.Tasks {
		for _, s := range utils.TaskServices(suite, t) {
			if s.Role == schemas.ServiceRoleMCP && !s.Hidden && utils.ServiceProxied(s) {
				return true
			}
		}
	}
	return false
}

// headerlessAgent says why an agent cannot send extra request headers, or "".
func headerlessAgent(cfg *schemas.AgentConfig) string {
	if cfg.Adapter == adapters.AdapterScripted && cfg.Scripted != nil && cfg.Scripted.Kind != adapters.ScriptedMCPLoop {
		return fmt.Sprintf("the scripted control %s sends none (only mcploop does)", cfg.Scripted.Kind)
	}
	return ""
}

// checkPromptCacheProvider refuses prompt_cache when the provider has no
// prompt_cache block: Bifrost ignores the header there ("it cannot
// manufacture opt-in"), so the arm would silently test nothing.
func (p *Planner) checkPromptCacheProvider(ctx context.Context, provider string) error {
	if p.deferGateway { // hosted plan: the runner's replan checks its own gateway (audit 8-B-04)
		return nil
	}
	probe, ok := schemas.GatewayAs[bifrostPromptCacheProbe](p.reg.Gateway)
	if !ok {
		return errors.New("prompt_cache: the gateway cannot report its providers' prompt_cache config")
	}
	on, err := probe.PromptCacheConfigured(ctx, provider)
	switch {
	case err != nil:
		return fmt.Errorf("prompt_cache: read provider %s: %w", provider, err)
	case !on:
		return fmt.Errorf("prompt_cache: provider %s has no prompt_cache block in Bifrost, so x-bf-prompt-cache-auto-inject is ignored; configure {\"auto_inject\": false} on the provider to let arms opt in per request", provider)
	}
	return nil
}

// bifrostMCP is the arm's MCP-through-Bifrost setting, or nil.
func bifrostMCP(cfg *schemas.AgentConfig) *schemas.BifrostMCP {
	if cfg == nil || cfg.Gateway.Bifrost == nil {
		return nil
	}
	return cfg.Gateway.Bifrost.MCP
}

// serveBifrostMCP creates the run's Bifrost MCP clients and points the
// agent's MCP servers at the gateway, after the services started and the key
// was minted. A failure fails the prepare stage: the agent never starts on
// half-served tools.
func (p *Pipeline) serveBifrostMCP(ctx context.Context, st *runState) error {
	f := bifrostMCP(st.rc.Agent)
	if f == nil || len(st.rc.Services) == 0 {
		return nil
	}
	be, ok := st.env.(schemas.BifrostMCPEnvironment)
	if !ok {
		return wrapPart("environment", st.env, errors.New("gateway.bifrost.mcp: this environment cannot serve MCP through the gateway"))
	}
	ev, err := be.ServeMCP(ctx, st.rc, f.CodeMode)
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("serve mcp through bifrost: %w", err))
	}
	st.ev.BifrostMCP = ev
	return nil
}

// removeBifrostMCP deletes the run's Bifrost MCP clients at cleanup, before
// the tool proxy goes away.
func (p *Pipeline) removeBifrostMCP(ctx context.Context, st *runState) error {
	if bifrostMCP(st.rc.Agent) == nil || st.env == nil {
		return nil
	}
	be, ok := st.env.(schemas.BifrostMCPEnvironment)
	if !ok {
		return nil
	}
	if err := be.RemoveMCP(ctx, st.rc); err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("remove bifrost mcp clients: %w", err))
	}
	return nil
}

// collectBifrostMCP reads the tool executions Bifrost logged for the run's
// key into the run's evidence, after the tool proxy ledger was collected.
func (p *Pipeline) collectBifrostMCP(ctx context.Context, st *runState) {
	if st.ev.BifrostMCP == nil {
		return
	}
	if be, ok := st.env.(schemas.BifrostMCPEnvironment); ok {
		be.CollectMCP(ctx, st.rc, st.ev.BifrostMCP, st.ev.ToolCalls)
	}
}

// redactBearer blanks every bearer token in a plan copy's env values and
// files: MCP through Bifrost puts the run key into BENCH_MCP_SERVERS and the
// agent's MCP config as an Authorization header, and plan.json must not
// keep it. out's maps are replaced, never edited in place.
func redactBearer(out *schemas.RunPlan) {
	env := make(map[string]string, len(out.Env))
	for k, v := range out.Env {
		env[k] = bearerRe.ReplaceAllString(v, "Bearer [redacted]")
	}
	out.Env = env
	if out.Files != nil {
		files := make(map[string]string, len(out.Files))
		for k, v := range out.Files {
			files[k] = bearerRe.ReplaceAllString(v, "Bearer [redacted]")
		}
		out.Files = files
	}
}

// declaresSemanticCache reports whether some arm of m sets
// gateway.bifrost.semantic_cache.
func declaresSemanticCache(m *schemas.Manifest) bool {
	for _, a := range m.Arms {
		if f := a.Config.Gateway.Bifrost; f != nil && f.SemanticCache != nil {
			return true
		}
	}
	return false
}

// acceptSemanticCache turns the semantic cache plugin's hygiene violations
// into expected findings for an experiment whose arm tests the cache: on
// v2.5.1 the plugin skips every request that carries no x-bf-cache-key, so the
// other arms' requests are not cached, unless the plugin has a
// default_cache_key, which caches them all and stays a violation. Every
// other violation is kept.
func acceptSemanticCache(h *schemas.GatewayHygiene) {
	if h == nil || !h.SemanticCache || h.SemanticCacheDefaultKey {
		return
	}
	kept := h.Violations[:0:0]
	for _, v := range h.Violations {
		if strings.HasPrefix(v, "semantic cache plugin ") {
			h.BenchSemanticCache = append(h.BenchSemanticCache, v+" (accepted: an arm under test opts in with its own cache key; requests without one bypass the cache)")
			continue
		}
		kept = append(kept, v)
	}
	h.Violations = kept
}

// complexityRule reports whether a routing policy is a complexity router
// arm: strategy rule with a condition that reads complexity_tier. Its first
// target is the model matched requests go to (claude-haiku-4-5 for SIMPLE
// requests, say), so the W9 rule that the first target is the agent's own
// model does not apply; requests the rule does not match keep the agent's
// model, which stays the primary for the fidelity gate. A gateway probe that
// names no model is narrowed to the rule's first target (probes/gateway.go),
// so outage conditions on such an arm take down the routed model, not the
// agent's: run them with model: "*" or a named model (docs/bifrost-features.md).
func complexityRule(pol *schemas.RoutingPolicy) bool {
	return pol != nil && pol.Strategy == schemas.RoutingRule && strings.Contains(pol.When, "complexity_tier")
}

// staleMCPRemover is implemented by gateways that can sweep the MCP keys and
// clients of runs that are no longer active (gateways/bifrost/bifrostmcp.go);
// the janitor calls it after revoking stale keys.
type staleMCPRemover interface {
	RemoveStaleMCP(ctx context.Context, active map[string]bool) (int, error)
}
