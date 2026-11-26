package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// fakeAgentsGet answers GET /api/agents with regs.
func fakeAgentsGet(regs []schemas.A2ARegistrationInfo, paths *[]string) func(ctx context.Context, path string, out any) error {
	return func(ctx context.Context, path string, out any) error {
		if paths != nil {
			*paths = append(*paths, path)
		}
		data, _ := json.Marshal(map[string]any{"agents": regs, "count": len(regs)})
		return json.Unmarshal(data, out)
	}
}

func gatewayAgentsRouter(env OrgEnv, get func(ctx context.Context, path string, out any) error, noSource bool) fasthttp.RequestHandler {
	r := router.New()
	h := newGatewayAgentsHandler(env)
	h.sourceGet = func(context.Context, OrgEnv) func(ctx context.Context, path string, out any) error {
		if noSource {
			return nil
		}
		return get
	}
	h.routes(r)
	return r.Handler
}

var testRegs = []schemas.A2ARegistrationInfo{
	{Name: "Support Bot", CardURL: "https://agents.example.com/support/.well-known/agent-card.json", Enabled: true, AllowByDefault: true},
	{Name: "old_bot", CardURL: "https://agents.example.com/old/.well-known/agent-card.json", Enabled: false},
	{Name: "local_bot", CardURL: "http://10.0.0.7:9000/.well-known/agent-card.json", Enabled: true},
}

func TestGatewayAgentsListFromSource(t *testing.T) {
	var paths []string
	h := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, fakeAgentsGet(testRegs, &paths), false)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/agents", "")
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var resp struct {
		Agents []struct {
			Name    string `json:"name"`
			CardURL string `json:"card_url"`
			Enabled bool   `json:"enabled"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 3 || resp.Agents[0].Name != "Support Bot" || resp.Agents[0].CardURL != testRegs[0].CardURL || !resp.Agents[0].Enabled || resp.Agents[1].Enabled {
		t.Fatalf("agents = %+v", resp.Agents)
	}
	if len(paths) != 1 || paths[0] != "/api/agents" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestGatewayAgentsNeedSource409(t *testing.T) {
	h := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil, true)
	for _, c := range [][3]string{{"GET", "/gateway/agents", ""}, {"POST", "/gateway/agents/Support%20Bot/import", `{"model":"m","provider":"anthropic"}`}} {
		st, out := secretsCall(h, c[0], APIPrefix+c[1], c[2])
		if st != 409 || !strings.Contains(out, "Connect your Bifrost first") {
			t.Errorf("%s %s: %d %s", c[0], c[1], st, out)
		}
	}
}

func TestImportBuildsA2AYAML(t *testing.T) {
	h := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, fakeAgentsGet(testRegs, nil), false)
	st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/Support%20Bot/import", `{"model":"claude-sonnet-4-5","provider":"anthropic"}`)
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var resp struct {
		AgentID string `json:"agent_id"`
		YAML    string `json:"yaml"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AgentID != "bifrost-support-bot" || !strings.HasPrefix(resp.YAML, "# Imported from Bifrost agent registration Support Bot\n") {
		t.Fatalf("resp = %+v", resp)
	}
	var cfg schemas.AgentConfig
	if err := suite.DecodeYAML([]byte(resp.YAML), "agent.yaml", suite.SchemaAgent, &cfg); err != nil {
		t.Fatalf("yaml does not decode: %v\n%s", err, resp.YAML)
	}
	if cfg.ID != "bifrost-support-bot" || cfg.Adapter != "a2a" || cfg.Image != "bench/coding-core-base:1" || cfg.Model != "claude-sonnet-4-5" ||
		cfg.A2A == nil || cfg.A2A.CardURL != testRegs[0].CardURL || cfg.A2A.TimeoutS != 600 ||
		cfg.Budgets.WallS != 900 || cfg.Budgets.CPUs != 1 || cfg.Budgets.MemGB != 1 || cfg.Gateway.Provider != "anthropic" || cfg.Gateway.MaxRetries != 0 {
		t.Fatalf("config = %+v", cfg)
	}
	st, out = secretsCall(h, "POST", APIPrefix+"/gateway/agents/Support%20Bot/import", `{"model":"m","provider":"anthropic","id":"my-agent"}`)
	if st != 200 || !strings.Contains(out, `"agent_id":"my-agent"`) {
		t.Fatalf("own id: %d %s", st, out)
	}
	for _, body := range []string{`{"model":"m","provider":"anthropic","id":"Bad Id!"}`, `{"provider":"anthropic"}`, `{"model":"m","provider":"anthropic","x":1}`} {
		if st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/Support%20Bot/import", body); st != 400 {
			t.Errorf("%s: %d %s", body, st, out)
		}
	}
	if st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/nobody/import", `{"model":"m","provider":"anthropic"}`); st != 404 {
		t.Errorf("unknown registration: %d %s", st, out)
	}
}

func TestImportRefusesDisabled(t *testing.T) {
	h := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, fakeAgentsGet(testRegs, nil), false)
	st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/old_bot/import", `{"model":"m","provider":"anthropic"}`)
	if st != 400 || strings.Contains(out, "yaml") {
		t.Fatalf("%d %s", st, out)
	}
}

func TestImportHostedRefusesPrivateCardURL(t *testing.T) {
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Hosted: true, OrgID: "o1"}}
	h := gatewayAgentsRouter(env, fakeAgentsGet(testRegs, nil), false)
	st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/local_bot/import", `{"model":"m","provider":"anthropic"}`)
	if st != 400 || !strings.Contains(out, "Heimdall's gateway must reach this agent's card at a public https address") {
		t.Fatalf("%d %s", st, out)
	}
	if st, out := secretsCall(h, "POST", APIPrefix+"/gateway/agents/Support%20Bot/import", `{"model":"m","provider":"anthropic"}`); st != 200 {
		t.Fatalf("public card refused: %d %s", st, out)
	}
	local := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, fakeAgentsGet(testRegs, nil), false)
	if st, out := secretsCall(local, "POST", APIPrefix+"/gateway/agents/local_bot/import", `{"model":"m","provider":"anthropic"}`); st != 200 {
		t.Fatalf("local mode refused a private card: %d %s", st, out)
	}
}
