package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/schemas"
)

// promptCacheGateway is the engine's fake gateway that also reports a
// provider's prompt_cache block.
type promptCacheGateway struct {
	fakeGateway
	on bool
}

func (g *promptCacheGateway) PromptCacheConfigured(context.Context, string) (bool, error) {
	return g.on, nil
}

func mcpSuite(mcp bool) *schemas.Suite {
	role := schemas.ServiceRoleTool
	if mcp {
		role = schemas.ServiceRoleMCP
	}
	return &schemas.Suite{ID: "acme-support", Tasks: []*schemas.Task{{ID: "t1", Services: []schemas.ServiceSpec{{ID: "desk", Role: role}}}}}
}

func bifrostAgent(kind string, f *schemas.BifrostFeatures) *schemas.AgentConfig {
	cfg := &schemas.AgentConfig{ID: "a", Adapter: adapters.AdapterCommand, Gateway: schemas.GatewayPolicy{Provider: "anthropic", Bifrost: f}}
	if kind != "" {
		cfg.Adapter = adapters.AdapterScripted
		cfg.Scripted = &schemas.ScriptedSpec{Kind: kind}
	}
	return cfg
}

func TestValidateBifrost(t *testing.T) {
	on := &Planner{reg: &schemas.Registry{Gateway: &promptCacheGateway{on: true}}}
	off := &Planner{reg: &schemas.Registry{Gateway: &promptCacheGateway{on: false}}}
	tests := []struct {
		name  string
		p     *Planner
		cfg   *schemas.AgentConfig
		suite *schemas.Suite
		want  string // "" = valid
	}{
		{"classic mcp", on, bifrostAgent("", &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}}), mcpSuite(true), ""},
		{"code mode on the mcp loop", on, bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{CodeMode: true}}), mcpSuite(true), ""},
		{"empty block", on, bifrostAgent("", &schemas.BifrostFeatures{}), mcpSuite(true), "sets nothing"},
		{"oracle cannot reach the gateway", on, bifrostAgent(adapters.ScriptedOracle, &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}}), mcpSuite(true), "runs without the gateway network"},
		{"suite without mcp", on, bifrostAgent("", &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}}), mcpSuite(false), "no task of suite acme-support has a visible MCP service"},
		{"team", on, func() *schemas.AgentConfig {
			c := bifrostAgent("", &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}})
			c.Team = []schemas.TeamMember{{ID: "lead"}}
			return c
		}(), mcpSuite(true), "team runs"},
		{"prompt cache", on, bifrostAgent("", &schemas.BifrostFeatures{PromptCache: schemas.PromptCacheAutoInject}), nil, ""},
		{"prompt cache without a provider block", off, bifrostAgent("", &schemas.BifrostFeatures{PromptCache: schemas.PromptCacheAutoInject}), nil, "has no prompt_cache block"},
		{"prompt cache bad value", on, bifrostAgent("", &schemas.BifrostFeatures{PromptCache: "always"}), nil, "is not auto_inject or off"},
		{"prompt cache on toy", on, bifrostAgent(adapters.ScriptedToy, &schemas.BifrostFeatures{PromptCache: schemas.PromptCacheOff}), nil, "sends none"},
		{"semantic direct", on, bifrostAgent("", &schemas.BifrostFeatures{SemanticCache: &schemas.BifrostSemanticCache{Mode: "direct", Scope: "task", TTL: "1h"}}), nil, ""},
		{"semantic bad scope and ttl", on, bifrostAgent("", &schemas.BifrostFeatures{SemanticCache: &schemas.BifrostSemanticCache{Mode: "semantic", Scope: "suite", TTL: "soon", Threshold: 0.9}}), nil, "scope \"suite\""},
		{"direct with threshold", on, bifrostAgent("", &schemas.BifrostFeatures{SemanticCache: &schemas.BifrostSemanticCache{Mode: "direct", Scope: "arm", Threshold: 0.8}}), nil, "threshold applies to mode semantic only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.validateBifrost(context.Background(), tt.cfg, tt.suite)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("unexpected refusal: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCheckBifrostFeaturesHashesOnlyDeclaringArms(t *testing.T) {
	p := &Planner{reg: &schemas.Registry{Gateway: &promptCacheGateway{on: true}}}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Config: *bifrostAgent("", nil)},
		"B": {Config: *bifrostAgent("", &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{CodeMode: true}})},
	}}
	got, err := p.checkBifrostFeatures(context.Background(), m, mcpSuite(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["B"] == "" {
		t.Fatalf("hashes = %v, want arm B only", got)
	}
	none, err := p.checkBifrostFeatures(context.Background(), &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Config: *bifrostAgent("", nil)}}}, mcpSuite(true))
	if err != nil || none != nil {
		t.Fatalf("a plan without gateway.bifrost got %v, %v; want nil so its hash is unchanged", none, err)
	}
}

func TestRedactPlanBlanksBearerTokens(t *testing.T) {
	plan := &schemas.RunPlan{
		Env:   map[string]string{"BENCH_MCP_SERVERS": `{"desk":{"url":"http://bifrost:8080/mcp/desk-r-1","headers":{"Authorization":"Bearer sk-bf-secret"}}}`, "BENCH_RUN_KEY": "sk-bf-secret"},
		Files: map[string]string{"/home/agent/.codex/config.toml": "url = \"x\"\nhttp_headers = { \"Authorization\" = \"Bearer sk-bf-secret\" }\n"},
	}
	out := redactPlan(plan)
	for k, v := range out.Env {
		if strings.Contains(v, "sk-bf-secret") {
			t.Fatalf("env %s keeps the key: %s", k, v)
		}
	}
	for k, v := range out.Files {
		if strings.Contains(v, "sk-bf-secret") {
			t.Fatalf("file %s keeps the key: %s", k, v)
		}
	}
	if !strings.Contains(plan.Env["BENCH_MCP_SERVERS"], "sk-bf-secret") {
		t.Fatal("redactPlan edited the plan the agent runs with")
	}
}

// complexityGateway reports a complexity analyzer state.
type complexityGateway struct {
	fakeGateway
	state string
}

func (g *complexityGateway) ComplexityAnalyzerState(context.Context) (string, error) {
	return g.state, nil
}

func TestCheckComplexityRouting(t *testing.T) {
	rule := &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Provider: "anthropic", Routing: &schemas.RoutingPolicy{
		Strategy: schemas.RoutingRule, When: `complexity_tier == "COMPLEX"`, Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-5"}}}}}
	for state, refused := range map[string]bool{"disabled": true, "absent": true, "ready": false, "llm_ready": false} {
		p := &Planner{reg: &schemas.Registry{Gateway: &complexityGateway{state: state}}}
		err := p.checkComplexityRouting(context.Background(), rule)
		if (err != nil) != refused {
			t.Errorf("state %s: err = %v, want refused %t", state, err, refused)
		}
	}
	plain := &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Routing: &schemas.RoutingPolicy{Strategy: schemas.RoutingRule, When: `request_type == "chat"`}}}
	if err := (&Planner{reg: &schemas.Registry{}}).checkComplexityRouting(context.Background(), plain); err != nil {
		t.Fatalf("a rule that does not read complexity_tier: %v", err)
	}
}

func TestAcceptSemanticCache(t *testing.T) {
	h := &schemas.GatewayHygiene{SemanticCache: true, Violations: []string{
		"semantic cache plugin semantic_cache is loaded", "routing rule x can send a run's model to another target"}}
	acceptSemanticCache(h)
	if len(h.Violations) != 1 || !strings.HasPrefix(h.Violations[0], "routing rule") || len(h.BenchSemanticCache) != 1 {
		t.Fatalf("hygiene = %+v", h)
	}
	withDefault := &schemas.GatewayHygiene{SemanticCache: true, SemanticCacheDefaultKey: true, Violations: []string{"semantic cache plugin semantic_cache is loaded"}}
	acceptSemanticCache(withDefault)
	if len(withDefault.Violations) != 1 {
		t.Fatal("a default_cache_key caches every arm's requests and must stay a violation")
	}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {}, "B": {Config: *bifrostAgent("", &schemas.BifrostFeatures{SemanticCache: &schemas.BifrostSemanticCache{Mode: "direct", Scope: "task"}})}}}
	if !declaresSemanticCache(m) || declaresSemanticCache(&schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {}}}) {
		t.Fatal("declaresSemanticCache")
	}
}
