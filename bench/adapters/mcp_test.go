package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// mcpServices is a run's started services: a proxied MCP server, an MCP
// server reached directly on its own path, a data service and a hidden MCP
// server the agent must not be told about.
func mcpServices() []schemas.ServiceHandle {
	return []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "tickets", Role: schemas.ServiceRoleMCP}, Host: "tickets", URL: "http://toolproxy:9200/s/tickets"},
		{Spec: schemas.ServiceSpec{ID: "kb", Role: schemas.ServiceRoleMCP, MCP: &schemas.MCPEndpoint{Path: "/rpc"}}, Host: "kb", URL: "http://kb:8000"},
		{Spec: schemas.ServiceSpec{ID: "db", Role: schemas.ServiceRoleData}, Host: "db", URL: "http://db:5432"},
		{Spec: schemas.ServiceSpec{ID: "secret", Role: schemas.ServiceRoleMCP, Hidden: true}, Host: "secret", URL: "http://secret:9000"},
	}
}

const wantServersJSON = `{"kb":{"url":"http://kb:8000/rpc"},"tickets":{"url":"http://toolproxy:9200/s/tickets/mcp"}}`

func TestMCPVars(t *testing.T) {
	vars := map[string]string{}
	if err := mcpVars(vars, &schemas.RunContext{}); err != nil {
		t.Fatal(err)
	}
	if vars["mcp.servers_json"] != "{}" || len(vars) != 1 {
		t.Fatalf("vars without services = %v, want only an empty servers_json", vars)
	}
	vars = map[string]string{}
	if err := mcpVars(vars, &schemas.RunContext{Services: mcpServices()}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"mcp.servers_json": wantServersJSON,
		"mcp.tickets.url":  "http://toolproxy:9200/s/tickets/mcp",
		"mcp.kb.url":       "http://kb:8000/rpc",
	}
	if len(vars) != len(want) {
		t.Fatalf("vars = %v, want %v", vars, want)
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}

func TestMCPFormatAndPath(t *testing.T) {
	tests := []struct {
		name         string
		cfg          schemas.AgentConfig
		format, path string
		errHas       string
	}{
		{name: "codex", cfg: schemas.AgentConfig{Parser: ParserCodex}, format: MCPFormatCodex, path: "/home/agent/.codex/config.toml"},
		{name: "codex home", cfg: schemas.AgentConfig{Parser: ParserCodex, Env: map[string]string{"CODEX_HOME": "/c"}}, format: MCPFormatCodex, path: "/c/config.toml"},
		{name: "claude code", cfg: schemas.AgentConfig{Parser: ParserClaudeCode}, format: MCPFormatClaude, path: "/home/agent/.claude/.claude.json"},
		{name: "opencode config env", cfg: schemas.AgentConfig{Parser: ParserOpenCode, Env: map[string]string{"OPENCODE_CONFIG": "/tmp/bench/opencode.json"}}, format: MCPFormatOpenCode, path: "/tmp/bench/opencode.json"},
		{name: "opencode global", cfg: schemas.AgentConfig{Parser: ParserOpenCode}, format: MCPFormatOpenCode, path: "/home/agent/.config/opencode/opencode.json"},
		{name: "unknown cli", cfg: schemas.AgentConfig{}, format: MCPFormatEnv},
		{name: "generic with path", cfg: schemas.AgentConfig{MCP: &schemas.AgentMCP{Format: MCPFormatGeneric, ConfigPath: "/home/agent/mcp.json"}}, format: MCPFormatGeneric, path: "/home/agent/mcp.json"},
		{name: "explicit format and path", cfg: schemas.AgentConfig{Parser: ParserCodex, MCP: &schemas.AgentMCP{Format: MCPFormatClaude, ConfigPath: "/x/y.json"}}, format: MCPFormatClaude, path: "/x/y.json"},
		{name: "generic without path", cfg: schemas.AgentConfig{MCP: &schemas.AgentMCP{Format: MCPFormatGeneric}}, format: MCPFormatGeneric, errHas: "needs mcp.config_path"},
		{name: "unknown format", cfg: schemas.AgentConfig{MCP: &schemas.AgentMCP{Format: "yaml"}}, errHas: "not one of"},
		{name: "relative path", cfg: schemas.AgentConfig{MCP: &schemas.AgentMCP{Format: MCPFormatGeneric, ConfigPath: "mcp.json"}}, format: MCPFormatGeneric, errHas: "absolute"},
		{name: "workspace path", cfg: schemas.AgentConfig{MCP: &schemas.AgentMCP{Format: MCPFormatGeneric, ConfigPath: "/workspace/.mcp.json"}}, format: MCPFormatGeneric, errHas: "inside the workspace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, err := mcpFormat(&tt.cfg)
			if err == nil && format != MCPFormatEnv {
				var p string
				p, err = mcpConfigPath(&tt.cfg, format)
				if err == nil && p != tt.path {
					t.Errorf("path = %q, want %q", p, tt.path)
				}
			}
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.errHas)
				}
				return
			}
			if err != nil || format != tt.format {
				t.Fatalf("format = %q, %v; want %q", format, err, tt.format)
			}
		})
	}
}

func TestMergeCodexTOML(t *testing.T) {
	servers := map[string]string{"tickets": "http://toolproxy:9200/s/tickets/mcp", "my.kb": "http://kb:8000/rpc"}
	existing := "model = \"m\"\n\n[features]\nplugins = false"
	got, err := mergeMCP(MCPFormatCodex, existing, servers)
	if err != nil {
		t.Fatal(err)
	}
	want := existing + "\n\n[mcp_servers.\"my.kb\"]\nurl = \"http://kb:8000/rpc\"\n\n[mcp_servers.tickets]\nurl = \"http://toolproxy:9200/s/tickets/mcp\"\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasPrefix(got, existing) {
		t.Fatal("existing settings were not kept as written")
	}

	fresh, err := mergeMCP(MCPFormatCodex, "", map[string]string{"t": "http://u"})
	if err != nil || fresh != "[mcp_servers.t]\nurl = \"http://u\"\n" {
		t.Fatalf("fresh = %q, %v", fresh, err)
	}
	if _, err := mergeMCP(MCPFormatCodex, "[mcp_servers.tickets]\nurl = \"x\"\n", servers); err == nil || !strings.Contains(err.Error(), "already defines") {
		t.Fatalf("err = %v, want a duplicate error", err)
	}
}

func TestMergeJSON(t *testing.T) {
	servers := map[string]string{"tickets": "http://toolproxy:9200/s/tickets/mcp"}
	t.Run("claude new file", func(t *testing.T) {
		got, err := mergeMCP(MCPFormatClaude, "", servers)
		if err != nil {
			t.Fatal(err)
		}
		want := "{\n  \"mcpServers\": {\n    \"tickets\": {\n      \"type\": \"http\",\n      \"url\": \"http://toolproxy:9200/s/tickets/mcp\"\n    }\n  }\n}\n"
		if got != want {
			t.Fatalf("got:\n%s\nwant:\n%s", got, want)
		}
	})
	t.Run("opencode keeps every other field", func(t *testing.T) {
		existing := `{"model": "bifrost/{env:BENCH_MODEL}", "limit": 12345678901234567890, "html": "<a&b>", "mcp": {"other": {"type": "local", "command": ["x"]}}}`
		got, err := mergeMCP(MCPFormatOpenCode, existing, servers)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{`"model": "bifrost/{env:BENCH_MODEL}"`, `"limit": 12345678901234567890`, `"html": "<a&b>"`, `"other": {`} {
			if !strings.Contains(got, s) {
				t.Errorf("merged config lost %s:\n%s", s, got)
			}
		}
		var obj struct {
			MCP map[string]map[string]any `json:"mcp"`
		}
		if err := json.Unmarshal([]byte(got), &obj); err != nil {
			t.Fatal(err)
		}
		if e := obj.MCP["tickets"]; e["type"] != "remote" || e["url"] != servers["tickets"] || e["enabled"] != true {
			t.Fatalf("opencode entry = %v", e)
		}
	})
	t.Run("generic shape", func(t *testing.T) {
		got, err := mergeMCP(MCPFormatGeneric, "", servers)
		if err != nil || !strings.Contains(got, `"mcpServers"`) || !strings.Contains(got, `"type": "http"`) {
			t.Fatalf("got %s, %v", got, err)
		}
	})
	errs := []struct{ name, existing, errHas string }{
		{"duplicate id", `{"mcpServers": {"tickets": {}}}`, "already defines mcpServers.tickets"},
		{"section not an object", `{"mcpServers": []}`, "not an object"},
		{"not JSON", `model = "x"`, "not a JSON object"},
	}
	for _, e := range errs {
		t.Run(e.name, func(t *testing.T) {
			if _, err := mergeMCP(MCPFormatClaude, e.existing, servers); err == nil || !strings.Contains(err.Error(), e.errHas) {
				t.Fatalf("err = %v, want it to mention %q", err, e.errHas)
			}
		})
	}
}

func TestPlanMCP(t *testing.T) {
	t.Run("command agent gets env and template variables", func(t *testing.T) {
		agent := commandAgent()
		agent.Argv = append(agent.Argv, "--mcp", "{{mcp.servers_json}}")
		agent.Env["TICKETS"] = "{{mcp.tickets.url}}"
		agent.Files["/home/agent/mcp.json"] = `{"servers": {{mcp.servers_json}}}`
		rc, cfg := fixture(t, agent)
		rc.Services = mcpServices()
		plan, err := newCommandAdapter(cfg).Plan(t.Context(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Env[utils.EnvMCPServers] != wantServersJSON {
			t.Errorf("%s = %q", utils.EnvMCPServers, plan.Env[utils.EnvMCPServers])
		}
		if plan.Argv[len(plan.Argv)-1] != wantServersJSON || plan.Env["TICKETS"] != "http://toolproxy:9200/s/tickets/mcp" {
			t.Errorf("argv %q env %q", plan.Argv, plan.Env["TICKETS"])
		}
		if plan.Files["/home/agent/mcp.json"] != `{"servers": `+wantServersJSON+`}` {
			t.Errorf("file = %q", plan.Files["/home/agent/mcp.json"])
		}
		if strings.Contains(plan.Env[utils.EnvMCPServers], "secret") {
			t.Error("a hidden service reached the agent")
		}
	})
	t.Run("unknown server variable fails", func(t *testing.T) {
		agent := commandAgent()
		agent.Env["X"] = "{{mcp.missing.url}}"
		rc, cfg := fixture(t, agent)
		rc.Services = mcpServices()
		if _, err := newCommandAdapter(cfg).Plan(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "mcp.missing.url") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("config conflict fails", func(t *testing.T) {
		agent := commandAgent()
		agent.Parser = ParserClaudeCode
		agent.Files["/home/agent/.claude/.claude.json"] = `{"mcpServers": {"tickets": {"type": "stdio"}}}`
		rc, cfg := fixture(t, agent)
		rc.Services = mcpServices()
		if _, err := newCommandAdapter(cfg).Plan(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "already defines") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("only non-mcp services change nothing", func(t *testing.T) {
		agent := commandAgent()
		agent.Parser = ParserCodex
		rc, cfg := fixture(t, agent)
		base, err := newCommandAdapter(cfg).Plan(t.Context(), rc)
		if err != nil {
			t.Fatal(err)
		}
		rc.Services = mcpServices()[2:]
		withData, err := newCommandAdapter(cfg).Plan(t.Context(), rc)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(base)
		b, _ := json.Marshal(withData)
		if string(a) != string(b) {
			t.Fatalf("plan changed with no MCP server:\n%s\n%s", a, b)
		}
	})
}
