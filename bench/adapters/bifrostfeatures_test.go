package adapters

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func gatewayServedRC() *schemas.RunContext {
	return &schemas.RunContext{
		Key: &schemas.RunKey{Value: "sk-bf-test"},
		Services: []schemas.ServiceHandle{{
			Spec: schemas.ServiceSpec{ID: "desk", Role: schemas.ServiceRoleMCP},
			URL:  "http://bifrost:8080/mcp/desk-r-x", GatewayAuth: true,
		}},
	}
}

func TestMCPServersJSONGatewayAuth(t *testing.T) {
	js, err := mcpServersJSON(gatewayServedRC())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"desk":{"url":"http://bifrost:8080/mcp/desk-r-x","headers":{"Authorization":"Bearer sk-bf-test"}}}`
	if js != want {
		t.Fatalf("got %s\nwant %s", js, want)
	}
	rc := gatewayServedRC()
	rc.Services[0].GatewayAuth = false
	rc.Services[0].URL = "http://toolproxy:9200/s/desk/mcp"
	if js, _ := mcpServersJSON(rc); strings.Contains(js, "headers") {
		t.Fatalf("direct server got headers: %s", js)
	}
}

func TestMergeMCPAuthNativeFormats(t *testing.T) {
	servers := map[string]string{"desk": "http://bifrost:8080/mcp/desk-r-x", "docs": "http://toolproxy:9200/s/docs/mcp"}
	auth := map[string]map[string]string{"desk": {"Authorization": "Bearer sk-bf-test"}}
	codex, err := mergeMCPAuth(MCPFormatCodex, "model = \"m\"\n", servers, auth)
	if err != nil {
		t.Fatal(err)
	}
	want := "model = \"m\"\n\n[mcp_servers.desk]\nurl = \"http://bifrost:8080/mcp/desk-r-x\"\nhttp_headers = { \"Authorization\" = \"Bearer sk-bf-test\" }\n\n[mcp_servers.docs]\nurl = \"http://toolproxy:9200/s/docs/mcp\"\n"
	if codex != want {
		t.Fatalf("codex:\n%s\nwant:\n%s", codex, want)
	}
	claude, err := mergeMCPAuth(MCPFormatClaude, "", servers, auth)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(claude, `"headers": {`) || strings.Count(claude, `"headers"`) != 1 || !strings.Contains(claude, `"Authorization": "Bearer sk-bf-test"`) {
		t.Fatalf("claude json:\n%s", claude)
	}
	plain, err := mergeMCPAuth(MCPFormatOpenCode, "", servers, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := mergeMCP(MCPFormatOpenCode, "", servers)
	if plain != same {
		t.Fatal("without auth mergeMCPAuth must equal mergeMCP")
	}
}

func TestBifrostHeaders(t *testing.T) {
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_1", ExpID: "e_x"},
		Slot:  &schemas.Slot{Arm: "B", Task: "t01"},
		Agent: &schemas.AgentConfig{Parser: ParserClaudeCode},
	}
	if BifrostHeaders(rc) != nil {
		t.Fatal("no gateway.bifrost must mean no headers")
	}
	rc.Agent.Gateway.Bifrost = &schemas.BifrostFeatures{PromptCache: schemas.PromptCacheAutoInject,
		SemanticCache: &schemas.BifrostSemanticCache{Mode: "direct", Scope: "task", TTL: "1h"}}
	h := BifrostHeaders(rc)
	want := map[string]string{"x-bf-prompt-cache-auto-inject": "true", "x-bf-cache-key": "hm-e_x-B-t01", "x-bf-cache-type": "direct", "x-bf-cache-ttl": "1h"}
	for k, v := range want {
		if h[k] != v {
			t.Fatalf("headers = %v, want %v", h, want)
		}
	}
	rc.Agent.Gateway.Bifrost.SemanticCache.Scope = "arm"
	if got := BifrostHeaders(rc)["x-bf-cache-key"]; got != "hm-e_x-B" {
		t.Fatalf("arm scope key = %q", got)
	}
	rc.Agent.Gateway.Bifrost.SemanticCache.Scope = "experiment"
	if got := BifrostHeaders(rc)["x-bf-cache-key"]; got != "hm-e_x" {
		t.Fatalf("experiment scope key = %q", got)
	}
	plan := &schemas.RunPlan{}
	addBifrostEnv(plan, rc)
	if !strings.Contains(plan.Env[EnvGatewayHeaders], `"x-bf-prompt-cache-auto-inject":"true"`) {
		t.Fatalf("BENCH_GATEWAY_HEADERS = %q", plan.Env[EnvGatewayHeaders])
	}
	if !strings.Contains(plan.Env["ANTHROPIC_CUSTOM_HEADERS"], "x-bf-prompt-cache-auto-inject: true") {
		t.Fatalf("ANTHROPIC_CUSTOM_HEADERS = %q", plan.Env["ANTHROPIC_CUSTOM_HEADERS"])
	}
}

func TestBifrostHeadersAppendToClaudeCustomHeaders(t *testing.T) {
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_x"}, Slot: &schemas.Slot{Arm: "B", Task: "t01"},
		Agent: &schemas.AgentConfig{Parser: ParserClaudeCode, Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{PromptCache: schemas.PromptCacheAutoInject}}}}
	plan := &schemas.RunPlan{Env: map[string]string{"ANTHROPIC_CUSTOM_HEADERS": "x-bf-session-id: r_1\nx-bf-session-affinity: off"}}
	addBifrostEnv(plan, rc)
	if got, want := plan.Env["ANTHROPIC_CUSTOM_HEADERS"], "x-bf-session-id: r_1\nx-bf-session-affinity: off\nx-bf-prompt-cache-auto-inject: true"; got != want {
		t.Fatalf("ANTHROPIC_CUSTOM_HEADERS = %q, want %q", got, want)
	}
}
