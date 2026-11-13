package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestHarnessCatalog decodes the harness catalog configs strictly, plans
// each through the command adapter and pins how it reaches Bifrost: the
// route, where the run key travels, the provider, the parser and the notes
// file. TestShippedAgentConfigs covers the generic shape of every config.
func TestHarnessCatalog(t *testing.T) {
	tests := []struct {
		id, provider, parser, notes, image string
		baseEnv, baseRoute                 string // env var that carries the gateway URL, and the route it must name
		keyEnv                             string // env var that carries the run key
	}{
		{"opencode", "anthropic", ParserOpenCode, "AGENTS.md", "bench/agent-opencode:1", "", "/openai/v1", "BIFROST_API_KEY"},
		{"gemini-cli", "gemini", "", "GEMINI.md", "bench/agent-gemini-cli:1", "GOOGLE_GEMINI_BASE_URL", "/genai", "GEMINI_API_KEY"},
		{"claude-code-gpt", "openai", ParserClaudeCode, "CLAUDE.md", "bench/agent-claude-code:1", "ANTHROPIC_BASE_URL", "/anthropic", "ANTHROPIC_AUTH_TOKEN"},
		{"custom-python", "anthropic", "", "AGENTS.md", "bench/agent-custom-python:1", "BENCH_BASE_URL", "/openai/v1", "BENCH_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			agent := loadAgent(t, filepath.Join("..", "agents", tt.id+".yaml"))
			if agent.ID != tt.id || agent.Adapter != AdapterCommand || agent.Image != tt.image ||
				agent.Gateway.Provider != tt.provider || agent.Parser != tt.parser || agent.Notes.ProjectFile != tt.notes {
				t.Errorf("config = %+v", agent)
			}
			if agent.Build == nil || agent.Build.Tag != agent.Image {
				t.Fatalf("build = %+v", agent.Build)
			}
			if _, err := os.Stat(filepath.Join("..", agent.Build.Dockerfile)); err != nil {
				t.Errorf("dockerfile: %v", err)
			}
			rc, cfg := fixture(t, agent)
			plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Env[tt.keyEnv] != testKey {
				t.Errorf("env %s = %q, want the run key", tt.keyEnv, plan.Env[tt.keyEnv])
			}
			for name, v := range plan.Env {
				if strings.Contains(v, testKey) && name != tt.keyEnv {
					t.Errorf("run key also in env %s", name)
				}
			}
			for p, content := range plan.Files {
				if strings.Contains(content, testKey) {
					t.Errorf("run key written into file %s", p)
				}
			}
			if tt.baseEnv != "" && plan.Env[tt.baseEnv] != testGateway+tt.baseRoute {
				t.Errorf("env %s = %q, want %q", tt.baseEnv, plan.Env[tt.baseEnv], testGateway+tt.baseRoute)
			}
			for _, a := range plan.Argv {
				if strings.Contains(a, "{{") && a != testInstruction {
					t.Errorf("unrendered argv %q", a)
				}
			}
		})
	}
}

// TestOpenCodeConfigPlan pins the OpenCode provider file: valid JSON after
// rendering, Bifrost's /openai/v1 route, the session headers, and one model
// id read from env so an arm switches provider with env.BENCH_MODEL.
func TestOpenCodeConfigPlan(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "opencode.yaml"))
	agent.Model = "gpt-5.1"
	agent.Env["BENCH_MODEL"] = "openai/{{model}}"
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"opencode", "run", "--format", "json", "--pure", "--title", "t01", testInstruction}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q", plan.Argv)
	}
	if plan.Env["BENCH_MODEL"] != "openai/gpt-5.1" || plan.Env["OPENCODE_CONFIG"] != "/tmp/bench/opencode.json" {
		t.Errorf("env = %v", plan.Env)
	}
	var oc struct {
		Model      string `json:"model"`
		Small      string `json:"small_model"`
		Permission string `json:"permission"`
		Provider   map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string            `json:"baseURL"`
				APIKey  string            `json:"apiKey"`
				Headers map[string]string `json:"headers"`
			} `json:"options"`
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(plan.Files["/tmp/bench/opencode.json"]), &oc); err != nil {
		t.Fatalf("opencode.json: %v", err)
	}
	b := oc.Provider["bifrost"]
	if oc.Model != "bifrost/{env:BENCH_MODEL}" || oc.Small != oc.Model || oc.Permission != "allow" ||
		b.NPM != "@ai-sdk/openai-compatible" || b.Options.BaseURL != testGateway+"/openai/v1" ||
		b.Options.APIKey != "{env:BIFROST_API_KEY}" || b.Models["{env:BENCH_MODEL}"] == nil {
		t.Errorf("opencode.json = %+v", oc)
	}
	if b.Options.Headers["x-bf-session-id"] != "r_p20fj3s8wa" || b.Options.Headers["x-bf-session-affinity"] != "off" {
		t.Errorf("headers = %v", b.Options.Headers)
	}
}

// TestGeminiCLIConfigPlan pins Gemini CLI's headless launch and its settings
// file, which must select API-key auth so the CLI never tries Google login.
func TestGeminiCLIConfigPlan(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "gemini-cli.yaml"))
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"gemini", "--model", "gemini-2.5-pro", "--yolo", "--skip-trust", "--output-format",
		"stream-json", "--prompt", testInstruction}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q", plan.Argv)
	}
	if plan.Env["GEMINI_CLI_CUSTOM_HEADERS"] != "x-bf-session-id: r_p20fj3s8wa, x-bf-session-affinity: off" ||
		plan.Env["GEMINI_DEFAULT_AUTH_TYPE"] != "gemini-api-key" || plan.Env["GEMINI_MODEL"] != "gemini-2.5-pro" {
		t.Errorf("env = %v", plan.Env)
	}
	var settings struct {
		Security struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
		Privacy struct {
			UsageStatisticsEnabled *bool `json:"usageStatisticsEnabled"`
		} `json:"privacy"`
	}
	if err := json.Unmarshal([]byte(plan.Files["/home/agent/.gemini/settings.json"]), &settings); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if settings.Security.Auth.SelectedType != "gemini-api-key" || settings.Model.Name != "gemini-2.5-pro" ||
		settings.Privacy.UsageStatisticsEnabled == nil || *settings.Privacy.UsageStatisticsEnabled {
		t.Errorf("settings = %+v", settings)
	}
}

// TestClaudeCodeGPTDiffersOnlyInModel checks claude-code-gpt is claude-code
// with every model field carrying Bifrost's provider prefix, so a comparison
// between the two isolates the model provider.
func TestClaudeCodeGPTDiffersOnlyInModel(t *testing.T) {
	base := loadAgent(t, filepath.Join("..", "agents", "claude-code.yaml"))
	gpt := loadAgent(t, filepath.Join("..", "agents", "claude-code-gpt.yaml"))
	if gpt.Gateway.Provider != "openai" || gpt.Image != base.Image || gpt.Parser != base.Parser ||
		gpt.Notes != base.Notes || gpt.Budgets != base.Budgets || strings.Contains(gpt.Model, "/") {
		t.Errorf("gpt config = %+v", gpt)
	}
	if len(gpt.Argv) != len(base.Argv) {
		t.Fatalf("argv %q vs %q", gpt.Argv, base.Argv)
	}
	for i := range base.Argv {
		want := strings.ReplaceAll(base.Argv[i], "{{model}}", "openai/{{model}}")
		if gpt.Argv[i] != want {
			t.Errorf("argv[%d] = %q, want %q", i, gpt.Argv[i], want)
		}
	}
	for k, v := range base.Env {
		if want := strings.ReplaceAll(v, "{{model}}", "openai/{{model}}"); gpt.Env[k] != want {
			t.Errorf("env %s = %q, want %q", k, gpt.Env[k], want)
		}
	}
	for k, v := range gpt.Env {
		if _, ok := base.Env[k]; !ok && v != "openai/{{model}}" {
			t.Errorf("extra env %s = %q is not a model pin", k, v)
		}
	}
}

// TestCustomAgentTemplateIsStdlibOnly keeps the custom agent template
// copyable: one Python file that imports nothing outside the standard
// library and reads its gateway settings from the env the YAML sets.
func TestCustomAgentTemplateIsStdlibOnly(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "agents", "images", "custom-python", "agent.py"))
	if err != nil {
		t.Fatal(err)
	}
	stdlib := map[string]bool{"json": true, "os": true, "subprocess": true, "sys": true, "urllib": true}
	imports := regexp.MustCompile(`(?m)^(?:import|from)\s+([a-zA-Z_]+)`).FindAllStringSubmatch(string(src), -1)
	if len(imports) == 0 {
		t.Fatal("no imports found")
	}
	for _, m := range imports {
		if !stdlib[m[1]] {
			t.Errorf("agent.py imports %s, outside the allowed standard library modules", m[1])
		}
	}
	agent := loadAgent(t, filepath.Join("..", "agents", "custom-python.yaml"))
	for name := range agent.Env {
		if !strings.Contains(string(src), `"`+name+`"`) {
			t.Errorf("env %s is set by the YAML but never read by agent.py", name)
		}
	}
	if agent.Parser != "" || agent.Argv[0] != "bench-agent" {
		t.Errorf("config = %+v", agent)
	}
}
