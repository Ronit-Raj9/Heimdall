package adapters

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// H02: every agent that can carry the run's gateway headers is trusted with
// heimdall plugin faults; the rest keep the fault proxy plan.
func TestCarriesGatewayHeaders(t *testing.T) {
	codex := loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
	noProvider := loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
	p := codexConfigPath(noProvider)
	noProvider.Files = map[string]string{p: strings.Replace(noProvider.Files[p], "model_provider = \"bifrost\"\n", "", 1)}
	inline := loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
	inline.Files = map[string]string{p: "model_provider = \"bf\"\n[model_providers.bf]\nhttp_headers = { a = \"b\" }\n"}
	declared := &schemas.AgentConfig{Adapter: "command", AdapterCaps: &schemas.AdapterCapabilities{SupportsGatewayHeaders: true}}
	remoteGW := &schemas.AgentConfig{Adapter: AdapterRemoteHTTP, Remote: &schemas.RemoteAgent{ModelTraffic: RemoteTrafficGateway}, AdapterCaps: declared.AdapterCaps}
	remoteExt := &schemas.AgentConfig{Adapter: AdapterRemoteHTTP, Remote: &schemas.RemoteAgent{ModelTraffic: RemoteTrafficExternal}, AdapterCaps: declared.AdapterCaps}
	openaiEP := &schemas.AgentConfig{Adapter: AdapterOpenAIAgent, AdapterCaps: declared.AdapterCaps}
	cases := []struct {
		name string
		cfg  *schemas.AgentConfig
		want bool
	}{
		{"nil", nil, false},
		{"claude code", &schemas.AgentConfig{Parser: ParserClaudeCode}, true},
		{"codex shipped", codex, true},
		{"codex default provider", noProvider, false},
		{"codex inline http_headers", inline, false},
		{"mcploop", &schemas.AgentConfig{Adapter: AdapterScripted, Scripted: &schemas.ScriptedSpec{Kind: ScriptedMCPLoop}}, true},
		{"scripted oracle", &schemas.AgentConfig{Adapter: AdapterScripted, Scripted: &schemas.ScriptedSpec{Kind: "oracle"}}, false},
		{"generic command", &schemas.AgentConfig{Adapter: "command"}, false},
		{"declared command", declared, true},
		{"remote gateway declared", remoteGW, true},
		{"remote external", remoteExt, false},
		{"openai endpoint", openaiEP, false},
	}
	for _, c := range cases {
		if got := CarriesGatewayHeaders(c.cfg); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	for _, id := range []string{"fw-plain-openai", "fw-langgraph", "fw-crewai", "fw-llamaindex", "fw-openai-agents", "fw-pydantic-ai", "fw-ai-sdk"} {
		if !CarriesGatewayHeaders(loadAgent(t, filepath.Join("..", "agents", id+".yaml"))) {
			t.Errorf("%s does not declare supports_gateway_headers", id)
		}
	}
}

// The plugin fault plan and run tags land in the Codex provider's
// http_headers table beside the session headers the config sets.
func TestCodexPlanCarriesGatewayHeaders(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
	rc, cfg := fixture(t, agent)
	rc.FaultHeaders = map[string]string{"x-heimdall-fault-plan": "eyJtIjoxfQ", "x-heimdall-run-id": "r_1", "x-bf-session-id": "ignored"}
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	toml := plan.Files["/home/agent/.codex/config.toml"]
	lines := strings.Split(toml, "\n")
	start, end := tomlSection(lines, "[model_providers.bifrost.http_headers]")
	if start < 0 {
		t.Fatalf("no http_headers table:\n%s", toml)
	}
	section := strings.Join(lines[start:end], "\n")
	for _, want := range []string{"x-heimdall-fault-plan = \"eyJtIjoxfQ\"", "x-heimdall-run-id = \"r_1\"", "x-bf-session-id = \"r_p20fj3s8wa\""} {
		if !strings.Contains(section, want) {
			t.Errorf("http_headers lacks %q:\n%s", want, section)
		}
	}
	if strings.Contains(toml, "ignored") || strings.Count(toml, "[model_providers.bifrost.http_headers]") != 1 {
		t.Errorf("config header overridden or table repeated:\n%s", toml)
	}
	if plan.Env[EnvGatewayHeaders] == "" {
		t.Error("BENCH_GATEWAY_HEADERS missing")
	}

	// A config without the table gets one.
	plan2 := &schemas.RunPlan{Files: map[string]string{codexConfigPath(agent): "model_provider = \"bifrost\"\n[model_providers.bifrost]\nname = \"B\""}}
	agent2 := *agent
	agent2.Files = plan2.Files
	addCodexHeaders(plan2, &agent2, map[string]string{"x-heimdall-arm": "a"})
	if got := plan2.Files[codexConfigPath(agent)]; !strings.HasSuffix(got, "\n\n[model_providers.bifrost.http_headers]\nx-heimdall-arm = \"a\"\n") {
		t.Errorf("new table: %q", got)
	}
}

// remote_http with model_traffic gateway passes the headers to the launcher.
func TestRemoteHTTPGatewayHeadersEnv(t *testing.T) {
	rc, cfg := remoteFixture(t, remoteAgent(RemoteTrafficGateway))
	cfg.GatewayPublicURL = testGateway
	rc.FaultHeaders = map[string]string{"x-heimdall-fault-plan": "p"}
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env[EnvGatewayHeaders] != `{"x-heimdall-fault-plan":"p"}` {
		t.Fatalf("env %v", plan.Env)
	}
	rc2, cfg2 := remoteFixture(t, remoteAgent(RemoteTrafficExternal))
	rc2.FaultHeaders = rc.FaultHeaders
	plan2, err := newRemoteHTTPAdapter(cfg2).Plan(context.Background(), rc2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan2.Env[EnvGatewayHeaders]; ok {
		t.Fatal("external traffic got gateway headers")
	}
}
