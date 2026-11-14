package adapters

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuiltinNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Builtin(CommandConfig{}) {
		if seen[a.GetName()] || a.GetVersion() == "" {
			t.Errorf("adapter %q duplicated or unversioned", a.GetName())
		}
		seen[a.GetName()] = true
	}
	if !seen[AdapterCommand] || !seen[AdapterScripted] {
		t.Errorf("adapters = %v", seen)
	}
	names := map[string]bool{}
	for _, p := range BuiltinParsers() {
		if names[p.GetName()] || p.GetVersion() == "" {
			t.Errorf("parser %q duplicated or unversioned", p.GetName())
		}
		names[p.GetName()] = true
	}
	for _, want := range []string{ParserClaudeCode, ParserCodex} {
		if !names[want] {
			t.Errorf("parser %q not registered: %v", want, names)
		}
	}
}

// loadAgent decodes an agent YAML strictly, as the bench loader does.
func loadAgent(t *testing.T, path string) *schemas.AgentConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var cfg schemas.AgentConfig
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return &cfg
}

// TestShippedAgentConfigs decodes every agents/*.yaml strictly and plans a
// run with it through the registered adapter.
func TestShippedAgentConfigs(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "agents", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Configs that must ship, with the gateway provider each routes through.
	// Other configs in the folder are decoded and planned too.
	shipped := map[string]string{
		"claude-code": "anthropic", "codex": "openai", "mini-swe-agent": "openai",
		"oracle": "anthropic", "noop": "anthropic", "exploit": "anthropic", "refuse": "anthropic", "toy": "anthropic",
	}
	found := map[string]bool{}
	for _, p := range paths {
		found[strings.TrimSuffix(filepath.Base(p), ".yaml")] = true
	}
	for id := range shipped {
		if !found[id] {
			t.Errorf("agents/%s.yaml missing", id)
		}
	}
	parsers := map[string]bool{}
	for _, p := range BuiltinParsers() {
		parsers[p.GetName()] = true
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			agent := loadAgent(t, path)
			if agent.ID != strings.TrimSuffix(filepath.Base(path), ".yaml") {
				t.Errorf("id %q does not match the file name", agent.ID)
			}
			if agent.Parser != "" && !parsers[agent.Parser] {
				t.Errorf("unknown parser %q", agent.Parser)
			}
			want, pinned := shipped[agent.ID]
			if agent.Gateway.Provider == "" || (pinned && agent.Gateway.Provider != want) ||
				agent.Gateway.MaxRetries != 0 || agent.Model == "" {
				t.Errorf("gateway or model wrong: %+v %q", agent.Gateway, agent.Model)
			}
			if agent.Build != nil {
				if _, err := os.Stat(filepath.Join("..", agent.Build.Dockerfile)); err != nil || agent.Build.Tag != agent.Image {
					t.Errorf("build spec %+v: %v", agent.Build, err)
				}
			}
			rc, cfg := fixture(t, agent)
			var adapter schemas.AgentAdapter
			for _, a := range Builtin(cfg) {
				if a.GetName() == agent.Adapter {
					adapter = a
				}
			}
			if adapter == nil {
				t.Fatalf("no adapter %q", agent.Adapter)
			}
			if _, err := adapter.Plan(context.Background(), rc); err != nil {
				t.Fatalf("plan: %v", err)
			}
		})
	}
}

// TestClaudeCodeConfigPlan pins the exact Claude Code launch.
func TestClaudeCodeConfigPlan(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "claude-code.yaml"))
	agent.Model = "claude-sonnet-5"
	agent.Notes.UserFile = "fixtures/notes/codewins.md"
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"claude", "-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose",
		"--disallowedTools", "WebSearch,WebFetch",
		"--model", "claude-sonnet-5", testInstruction}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q", plan.Argv)
	}
	wantEnv := map[string]string{
		"ANTHROPIC_BASE_URL":                       testGateway + "/anthropic",
		"ANTHROPIC_AUTH_TOKEN":                     testKey,
		"ANTHROPIC_API_KEY":                        "",
		"ANTHROPIC_MODEL":                          "claude-sonnet-5",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           "claude-sonnet-5",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             "claude-sonnet-5",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            "claude-sonnet-5",
		"ANTHROPIC_CUSTOM_HEADERS":                 "x-bf-session-id: r_p20fj3s8wa\nx-bf-session-affinity: off",
		"CLAUDE_CONFIG_DIR":                        "/home/agent/.claude",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY":          "1",
		"DISABLE_AUTOUPDATER":                      "1",
		"DISABLE_TELEMETRY":                        "1",
		"CLAUDE_CODE_DISABLE_TELEMETRY":            "1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
		"DISABLE_ERROR_REPORTING":                  "1",
	}
	if len(plan.Env) != len(wantEnv) {
		t.Errorf("env has %d entries, want %d: %v", len(plan.Env), len(wantEnv), plan.Env)
	}
	for k, v := range wantEnv {
		if got, ok := plan.Env[k]; !ok || got != v {
			t.Errorf("env %s = %q (present %v), want %q", k, got, ok, v)
		}
	}
	if plan.Files["/home/agent/.claude/CLAUDE.md"] != testUserNotes || plan.Files[InstructionFile] != testInstruction {
		t.Errorf("files = %v", plan.Files)
	}
	if plan.WallS != 600 || plan.CPUs != 2 || plan.MemGB != 4 || plan.Network != testNetwork {
		t.Errorf("budgets = %+v", plan)
	}
}

// TestCodexConfigPlan pins the exact Codex launch: the run key only in env
// and in no file, the Bifrost provider and session headers in config.toml.
func TestCodexConfigPlan(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
	agent.Model = "gpt-5.6-sol"
	agent.Notes.UserFile = "fixtures/notes/codewins.md"
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"codex", "exec", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox",
		"--ephemeral", "--strict-config", "--model", "gpt-5.6-sol", "--", testInstruction}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q", plan.Argv)
	}
	wantEnv := map[string]string{
		"CODEX_HOME":      "/home/agent/.codex",
		"BIFROST_API_KEY": testKey,
		"OPENAI_API_KEY":  "",
	}
	if len(plan.Env) != len(wantEnv) {
		t.Errorf("env has %d entries, want %d: %v", len(plan.Env), len(wantEnv), plan.Env)
	}
	for k, v := range wantEnv {
		if got, ok := plan.Env[k]; !ok || got != v {
			t.Errorf("env %s = %q (present %v), want %q", k, got, ok, v)
		}
	}
	toml := plan.Files["/home/agent/.codex/config.toml"]
	for _, want := range []string{
		"model = \"gpt-5.6-sol\"\n",
		"model_provider = \"bifrost\"\n",
		"allow_login_shell = false\n",
		"web_search = \"disabled\"\n",
		"[model_providers.bifrost]\n",
		"base_url = \"" + testGateway + "/openai/v1\"\n",
		"wire_api = \"responses\"\n",
		"env_key = \"BIFROST_API_KEY\"\n",
		"requires_openai_auth = false\n",
		"exclude = [\"BIFROST_API_KEY\"]\n",
		"x-bf-session-id = \"r_p20fj3s8wa\"\n",
		"x-bf-session-affinity = \"off\"\n",
		"persistence = \"none\"\n",
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("config.toml lacks %q:\n%s", want, toml)
		}
	}
	if strings.Contains(toml, testKey) {
		t.Error("config.toml contains the run key")
	}
	if plan.Files["/home/agent/.codex/AGENTS.md"] != testUserNotes || plan.Files[InstructionFile] != testInstruction {
		t.Errorf("files = %v", plan.Files)
	}
	if agent.Parser != ParserCodex || agent.Notes.ProjectFile != "AGENTS.md" || agent.Gateway.Provider != "openai" {
		t.Errorf("parser %q, notes %+v, gateway %+v", agent.Parser, agent.Notes, agent.Gateway)
	}
	if plan.WallS != 600 || plan.CPUs != 2 || plan.MemGB != 4 || plan.Network != testNetwork {
		t.Errorf("budgets = %+v", plan)
	}
}

// TestMiniSWEAgentConfigPlan pins mini-swe-agent on Bifrost's OpenAI route.
func TestMiniSWEAgentConfigPlan(t *testing.T) {
	agent := loadAgent(t, filepath.Join("..", "agents", "mini-swe-agent.yaml"))
	agent.Model = "gpt-5.6-sol"
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"mini", "-y", "--exit-immediately", "-l", "0", "-m", "openai/gpt-5.6-sol",
		"-o", "/tmp/bench/mini.traj.json", "-t", testInstruction}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Errorf("argv = %q", plan.Argv)
	}
	for k, v := range map[string]string{
		"OPENAI_API_BASE": testGateway + "/openai/v1",
		"OPENAI_BASE_URL": testGateway + "/openai/v1",
		"OPENAI_API_KEY":  testKey,
	} {
		if plan.Env[k] != v {
			t.Errorf("env %s = %q, want %q", k, plan.Env[k], v)
		}
	}
	if agent.Gateway.Provider != "openai" || agent.Notes.ProjectFile != "AGENTS.md" || agent.Parser != "" {
		t.Errorf("gateway %+v, notes %+v, parser %q", agent.Gateway, agent.Notes, agent.Parser)
	}
}

func TestAdaptersImplementAdditiveContracts(t *testing.T) {
	for _, adapter := range Builtin(CommandConfig{GatewayAgentURL: "http://bifrost:8080", EvalNetwork: "bench"}) {
		t.Run(adapter.GetName(), func(t *testing.T) {
			if _, ok := adapter.(schemas.AdapterMetadata); !ok {
				t.Error("missing AdapterMetadata")
			}
			if _, ok := adapter.(schemas.AdapterValidator); !ok {
				t.Error("missing AdapterValidator")
			}
			if _, ok := adapter.(schemas.AdapterRunner); !ok {
				t.Error("missing AdapterRunner")
			}
			if _, ok := adapter.(schemas.AdapterCleanup); !ok {
				t.Error("missing AdapterCleanup")
			}
		})
	}
}
