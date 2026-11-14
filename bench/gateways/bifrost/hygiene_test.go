package bifrost

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestHygiene(t *testing.T) {
	rule := func(name string, enabled any, targets []map[string]any, fallbacks []any) map[string]any {
		r := map[string]any{"id": "id-" + name, "name": name, "scope": "global", "cel_expression": "true", "targets": targets}
		if enabled != nil {
			r["enabled"] = enabled
		}
		if fallbacks != nil {
			r["fallbacks"] = fallbacks
		}
		return r
	}
	half := func(provider string) map[string]any { return map[string]any{"provider": provider, "weight": 0.5} }
	tests := []struct {
		name       string
		mutate     func(f *fakeBifrost)
		violations []string // substrings, in order
		check      func(t *testing.T, f *fakeBifrost, g *Gateway)
	}{
		{name: "clean gateway"},
		{name: "MCP clients with auto-inject off pass", mutate: func(f *fakeBifrost) { f.mcpClients = 2 }},
		{name: "MCP clients with auto-inject on fail", mutate: func(f *fakeBifrost) {
			f.mcpClients = 2
			f.config["client_config"].(map[string]any)["mcp_disable_auto_tool_inject"] = false
		}, violations: []string{"MCP tool auto-inject is on (client_config.mcp_disable_auto_tool_inject is not true) while 2 MCP client(s) exist"}},
		{name: "auto-inject on without MCP clients passes", mutate: func(f *fakeBifrost) {
			f.config["client_config"].(map[string]any)["mcp_disable_auto_tool_inject"] = false
		}},
		{name: "semantic cache loaded", mutate: func(f *fakeBifrost) { f.loaded = append(f.loaded, "semantic_cache") },
			violations: []string{"semantic cache plugin semantic_cache is loaded"}},
		{name: "semantic cache enabled in config", mutate: func(f *fakeBifrost) {
			f.pluginRows = []map[string]any{{"name": "semantic_cache", "enabled": true}, {"name": "otel", "enabled": false}}
		}, violations: []string{"semantic cache plugin semantic_cache is enabled in the plugin config"}},
		{name: "semantic cache disabled in config passes", mutate: func(f *fakeBifrost) {
			f.pluginRows = []map[string]any{{"name": "semantic_cache", "enabled": false}}
		}},
		{name: "weighted two-target rule", mutate: func(f *fakeBifrost) {
			f.rules = []map[string]any{rule("split", nil, []map[string]any{half("openai"), half("anthropic")}, nil)}
		}, violations: []string{"routing rule split (id id-split, scope global, 2 target(s) weighted 0.5/0.5, 2 pinned, 0 fallback(s)) can send a run's model to another target"}},
		{name: "single target pinning a model", mutate: func(f *fakeBifrost) {
			f.rules = []map[string]any{rule("pin", true, []map[string]any{{"model": "gpt-4o", "weight": 1}}, nil)}
		}, violations: []string{"routing rule pin (id id-pin, scope global, 1 target(s) weighted 1, 1 pinned, 0 fallback(s))"}},
		{name: "fallbacks move a request", mutate: func(f *fakeBifrost) {
			f.rules = []map[string]any{rule("fb", nil, []map[string]any{{"weight": 1}}, []any{"openai/gpt-4o"})}
		}, violations: []string{"1 fallback(s)"}},
		{name: "a disabled rule and a pass-through rule pass", mutate: func(f *fakeBifrost) {
			f.rules = []map[string]any{
				rule("off", false, []map[string]any{half("openai"), half("anthropic")}, nil),
				rule("same", nil, []map[string]any{{"weight": 1, "ttft_timeout_ms": 500}}, nil),
			}
		}, check: func(t *testing.T, f *fakeBifrost, g *Gateway) {
			m, _ := g.Preflight(context.Background())
			if m.Hygiene.RoutingRules != 1 {
				t.Errorf("enabled rules = %d, want 1", m.Hygiene.RoutingRules)
			}
		}},
		{name: "enabled model router", mutate: func(f *fakeBifrost) {
			f.routers = []map[string]any{{"id": "mr1", "name": "smart", "routing": map[string]any{"enabled": true}},
				{"id": "mr2", "name": "idle", "routing": map[string]any{"enabled": false}}}
		}, violations: []string{"model router smart (id mr1) can send a run's model to another target"}},
		{name: "injected tools on a bench provider", mutate: func(f *fakeBifrost) {
			f.provider["injected_tools"] = []any{map[string]any{"name": "web_search"}}
		}, violations: []string{"provider anthropic injects 1 tool(s) into every request (injected_tools)"}},
		{name: "model aliases on a bench key slot", mutate: func(f *fakeBifrost) {
			f.keys[1]["aliases"] = map[string]any{testModel: map[string]any{"model_id": "other"}}
		}, violations: []string{"provider anthropic key slot fp-slot-2 with model aliases for " + testModel}},
		{name: "an unreadable feature is a violation", mutate: func(f *fakeBifrost) {
			f.down = map[string]bool{"GET /api/mcp/clients": true, "GET /api/routing/rules": true}
		}, violations: []string{"MCP clients could not be read: bifrost GET /api/mcp/clients: status 500",
			"routing rules could not be read: bifrost GET /api/routing/rules: status 500"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeBifrost(t)
			g, err := New(testConfig(f.srv.URL))
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Setup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				tt.mutate(f)
			}
			if tt.check != nil {
				tt.check(t, f, g)
				return
			}
			m, err := g.Preflight(context.Background())
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			h := m.Hygiene
			if h == nil {
				t.Fatal("no hygiene record")
			}
			if len(h.Violations) != len(tt.violations) {
				t.Fatalf("violations = %q, want %q", h.Violations, tt.violations)
			}
			for i, want := range tt.violations {
				if !strings.Contains(h.Violations[i], want) {
					t.Errorf("violation %d = %q, want %q", i, h.Violations[i], want)
				}
				if !slices.ContainsFunc(m.Problems, func(p string) bool { return p == "hygiene: "+h.Violations[i] }) {
					t.Errorf("violation %q is not a problem: %q", h.Violations[i], m.Problems)
				}
			}
			if h.MCPClients != f.mcpClients || h.SemanticCache != slices.ContainsFunc(tt.violations, func(v string) bool { return strings.Contains(v, "semantic") }) {
				t.Errorf("hygiene = %+v", h)
			}
		})
	}
}
