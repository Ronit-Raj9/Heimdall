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
	parsers := BuiltinParsers()
	if len(parsers) != 1 || parsers[0].GetName() != ParserClaudeCode || parsers[0].GetVersion() == "" {
		t.Errorf("parsers = %v", parsers)
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
	if len(paths) != 7 {
		t.Fatalf("found %d agent configs, want 7: %v", len(paths), paths)
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
			if agent.Gateway.Provider != "anthropic" || agent.Gateway.MaxRetries != 0 || agent.Model == "" {
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
	wantArgv := []string{"claude", "-p", "--dangerously-skip-permissions", "--output-format", "stream-json",
		"--verbose", "--model", "claude-sonnet-5", testInstruction}
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
