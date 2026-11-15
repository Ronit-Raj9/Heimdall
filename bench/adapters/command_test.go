package adapters

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCommandFaultCapability(t *testing.T) {
	a := newCommandAdapter(CommandConfig{})
	rule := schemas.AdapterFaultRule{Kind: "tool", Name: "search", Mode: "error"}
	if err := a.ValidateFault(&schemas.AgentConfig{}, rule); err == nil {
		t.Fatal("undeclared fault hook accepted")
	}
	cfg := &schemas.AgentConfig{Capabilities: &schemas.AdapterCapabilities{FaultProtocolVersion: schemas.AdapterFaultProtocol, FaultHooks: []schemas.AdapterFaultHook{{Kind: "tool", Name: "search", Modes: []string{"error"}}}}}
	if err := a.ValidateFault(cfg, rule); err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateFault(cfg, schemas.AdapterFaultRule{Kind: "tool", Name: "search", Mode: "timeout", TimeoutMS: 1}); err == nil {
		t.Fatal("unsupported mode accepted")
	}
}

const (
	testKey         = "sk-bf-v1-secretvalue0123456789"
	testGateway     = "http://bifrost:8080"
	testNetwork     = "bench-eval-net"
	testInstruction = "# Fix rounding\n\nAmounts must round half up. Literal {{braces}} stay.\n"
	testPatch       = "diff --git a/x.py b/x.py\n"
	testExploit     = "#!/bin/sh\necho cheat\n"
	testUserNotes   = "Code wins over notes.\n"
)

// fixture builds a task folder, a bench root with a notes file, and a run
// context for an agent config.
func fixture(t *testing.T, agent *schemas.AgentConfig) (*schemas.RunContext, CommandConfig) {
	t.Helper()
	taskDir := t.TempDir()
	benchRoot := t.TempDir()
	write := func(dir, rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(taskDir, "instruction.md", testInstruction)
	write(taskDir, "solution/patch.diff", testPatch)
	write(taskDir, "controls/exploit.sh", testExploit)
	write(benchRoot, "fixtures/notes/codewins.md", testUserNotes)

	rc := &schemas.RunContext{
		Experiment: &schemas.Experiment{ID: "e_codewins"},
		Run:        &schemas.Run{RunID: "r_p20fj3s8wa", SlotID: "s_1", PairID: "p_1", ExpID: "e_codewins"},
		Slot:       &schemas.Slot{SlotID: "s_1", PairID: "p_1"},
		Task: &schemas.Task{
			ID: "t01", Dir: taskDir, Instruction: "instruction.md",
			Controls: schemas.TaskControls{Reference: "solution/patch.diff", Exploit: "controls/exploit.sh"},
			Timeouts: schemas.TaskTimeouts{AgentS: 600},
		},
		Agent:  agent,
		Key:    &schemas.RunKey{ID: "vk_1", Value: testKey, Model: agent.Model},
		Handle: &schemas.EnvironmentHandle{Network: "handle-net"},
	}
	return rc, CommandConfig{GatewayAgentURL: testGateway + "/", EvalNetwork: testNetwork, BenchRoot: benchRoot}
}

func commandAgent() *schemas.AgentConfig {
	return &schemas.AgentConfig{
		ID:      "test-agent",
		Adapter: AdapterCommand,
		Image:   "bench/agent-test:1",
		Argv: []string{"agent", "--model", "{{model}}", "{{instruction}}", "{{instruction_file}}",
			"{{workspace}}", "{{gateway.url}}", "{{run.id}}", "{{run.session}}", "{{exp.id}}", "{{task.id}}"},
		Env: map[string]string{
			"BASE_URL": "{{gateway.url}}/anthropic",
			"TOKEN":    "{{run.key}}",
			"EMPTY":    "",
			"HEADERS":  "x-bf-session-id: {{run.session}}\nx-bf-session-affinity: off",
		},
		Files:   map[string]string{"/home/agent/.config/tool.json": `{"key":"{{run.key}}","model":"{{model}}"}`},
		Model:   "claude-sonnet-4-5",
		Budgets: schemas.Budgets{WallS: 900, CPUs: 2, MemGB: 4},
	}
}

func TestCommandPlanRendersEveryVariable(t *testing.T) {
	rc, cfg := fixture(t, commandAgent())
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{"agent", "--model", "claude-sonnet-4-5", testInstruction, InstructionFile,
		Workspace, testGateway, "r_p20fj3s8wa", "r_p20fj3s8wa", "e_codewins", "t01"}
	if strings.Join(plan.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("argv:\n got %q\nwant %q", plan.Argv, wantArgv)
	}
	wantEnv := map[string]string{
		"BASE_URL": testGateway + "/anthropic",
		"TOKEN":    testKey,
		"EMPTY":    "",
		"HEADERS":  "x-bf-session-id: r_p20fj3s8wa\nx-bf-session-affinity: off",
	}
	for k, v := range wantEnv {
		if got, ok := plan.Env[k]; !ok || got != v {
			t.Errorf("env %s = %q (present %v), want %q", k, got, ok, v)
		}
	}
	if got := plan.Files["/home/agent/.config/tool.json"]; got != `{"key":"`+testKey+`","model":"claude-sonnet-4-5"}` {
		t.Errorf("rendered file = %q", got)
	}
	if got := plan.Files[InstructionFile]; got != testInstruction {
		t.Errorf("instruction file = %q, want the literal instruction", got)
	}
	if plan.Image != "bench/agent-test:1" || plan.Workdir != Workspace || plan.User != "1000" ||
		plan.Network != testNetwork || plan.CPUs != 2 || plan.MemGB != 4 || plan.Pids != defaultPids {
		t.Errorf("plan basics wrong: %+v", plan)
	}
}

func TestCommandPlanErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(rc *schemas.RunContext, cfg *CommandConfig)
		want   string
	}{
		{"unknown variable in argv", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Argv = []string{"agent", "{{run.secret}}"}
		}, "run.secret"},
		{"unknown variable in env", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Env = map[string]string{"X": "{{modle}}"}
		}, "modle"},
		{"unknown variable in files", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Files = map[string]string{"/tmp/x": "{{nope}}"}
		}, "nope"},
		{"key in argv", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Argv = []string{"agent", "--api-key={{run.key}}"}
		}, "expose the run key"},
		{"no key", func(rc *schemas.RunContext, _ *CommandConfig) { rc.Key = nil }, "no run key"},
		{"no gateway url", func(_ *schemas.RunContext, cfg *CommandConfig) { cfg.GatewayAgentURL = "" }, "gateway agent url"},
		{"files collide with instruction", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Files = map[string]string{InstructionFile: "x"}
		}, "already define"},
		{"user path inside workspace", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Notes = schemas.AgentNotes{UserFile: "fixtures/notes/codewins.md", UserPath: "/workspace/CLAUDE.md"}
		}, "inside the workspace"},
		{"user path relative", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Notes = schemas.AgentNotes{UserFile: "fixtures/notes/codewins.md", UserPath: "CLAUDE.md"}
		}, "absolute"},
		{"user file missing", func(rc *schemas.RunContext, _ *CommandConfig) {
			rc.Agent.Notes = schemas.AgentNotes{UserFile: "fixtures/notes/missing.md", UserPath: "/home/agent/.claude/CLAUDE.md"}
		}, "notes.user_file"},
		{"no network", func(rc *schemas.RunContext, cfg *CommandConfig) {
			cfg.EvalNetwork = ""
			rc.Handle = nil
		}, "no evaluation network"},
		{"no image", func(rc *schemas.RunContext, _ *CommandConfig) { rc.Agent.Image = "" }, "no image"},
		{"no instruction", func(rc *schemas.RunContext, _ *CommandConfig) { rc.Task.Instruction = "missing.md" }, "missing.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, cfg := fixture(t, commandAgent())
			tt.mutate(rc, &cfg)
			_, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestCommandPlanKeyOnlyInEnvAndFiles checks every field of the plan except
// env and files for the key.
func TestCommandPlanKeyOnlyInEnvAndFiles(t *testing.T) {
	rc, cfg := fixture(t, commandAgent())
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	stripped := *plan
	stripped.Env, stripped.Files = nil, nil
	b, err := json.Marshal(stripped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), testKey) {
		t.Fatalf("key outside env and files: %s", b)
	}
	if plan.Env["TOKEN"] != testKey {
		t.Fatalf("key missing from env")
	}
	for p := range plan.Files {
		if strings.Contains(p, testKey) {
			t.Fatalf("key in a file path: %s", p)
		}
	}
}

func TestCommandPlanUserFile(t *testing.T) {
	agent := commandAgent()
	agent.Notes = schemas.AgentNotes{ProjectFile: "CLAUDE.md", UserFile: "fixtures/notes/codewins.md", UserPath: "/home/agent/.claude/CLAUDE.md"}
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Files["/home/agent/.claude/CLAUDE.md"]; got != testUserNotes {
		t.Fatalf("user notes = %q, want %q", got, testUserNotes)
	}

	t.Run("absolute user file", func(t *testing.T) {
		agent.Notes.UserFile = filepath.Join(cfg.BenchRoot, "fixtures/notes/codewins.md")
		plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Files["/home/agent/.claude/CLAUDE.md"] != testUserNotes {
			t.Fatal("absolute user file not delivered")
		}
	})
	t.Run("no user file, nothing installed", func(t *testing.T) {
		agent.Notes.UserFile = ""
		plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := plan.Files["/home/agent/.claude/CLAUDE.md"]; ok {
			t.Fatal("user path installed without a user file")
		}
	})
}

func TestCommandPlanWallTime(t *testing.T) {
	tests := []struct {
		name        string
		agent, task int
		want        int
		wantErr     bool
	}{
		{"task stricter", 900, 600, 600, false},
		{"agent stricter", 300, 600, 300, false},
		{"equal", 600, 600, 600, false},
		{"agent unset", 0, 600, 600, false},
		{"task unset", 900, 0, 900, false},
		{"both unset", 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := commandAgent()
			agent.Budgets.WallS = tt.agent
			rc, cfg := fixture(t, agent)
			rc.Task.Timeouts.AgentS = tt.task
			plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.WallS != tt.want {
				t.Fatalf("wall = %d, want %d", plan.WallS, tt.want)
			}
		})
	}
}

func TestCommandPlanNetworkAndPids(t *testing.T) {
	agent := commandAgent()
	agent.Budgets.PidsLimit = 128
	rc, cfg := fixture(t, agent)
	cfg.EvalNetwork = ""
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Network != "handle-net" {
		t.Errorf("network = %q, want the handle's network when none is configured", plan.Network)
	}
	if plan.Pids != 128 {
		t.Errorf("pids = %d, want 128", plan.Pids)
	}
}

var updatePlans = flag.Bool("update", false, "rewrite the golden plans under testdata/plans")

// TestPlanGolden pins the whole plan of each shipped CLI config, and of a
// generic command agent, with and without skills and MCP servers. The plain
// golden of each is the plan as it was before skills and MCP existed, so a
// run with neither must keep producing it byte for byte. Host folders are
// written as $TASK and $BENCH. Run with -update to rewrite them.
func TestPlanGolden(t *testing.T) {
	agents := map[string]func(t *testing.T) *schemas.AgentConfig{
		"codex": func(t *testing.T) *schemas.AgentConfig {
			return loadAgent(t, filepath.Join("..", "agents", "codex.yaml"))
		},
		"claude-code": func(t *testing.T) *schemas.AgentConfig {
			return loadAgent(t, filepath.Join("..", "agents", "claude-code.yaml"))
		},
		"opencode": func(t *testing.T) *schemas.AgentConfig {
			return loadAgent(t, filepath.Join("..", "agents", "opencode.yaml"))
		},
		"command": func(t *testing.T) *schemas.AgentConfig {
			a := commandAgent()
			a.Argv = append(a.Argv, "--mcp-servers", "{{mcp.servers_json}}")
			return a
		},
	}
	cases := []struct {
		name        string
		skills, mcp bool
	}{
		{name: "plain"},
		{name: "skills", skills: true},
		{name: "mcp", mcp: true},
		{name: "skills-mcp", skills: true, mcp: true},
	}
	for id, load := range agents {
		for _, c := range cases {
			t.Run(id+"-"+c.name, func(t *testing.T) {
				agent := load(t)
				rc, cfg := fixture(t, agent)
				if c.skills {
					writeTree(t, rc.Task.Dir, map[string]string{"skills/pdf/SKILL.md": "p", "skills/xlsx/SKILL.md": "x"})
					writeTree(t, cfg.BenchRoot, map[string]string{"skills/house-style/SKILL.md": "h"})
					rc.Task.Skills = "skills"
					agent.Skills = &schemas.AgentSkills{Extra: []string{"skills/house-style"}}
				}
				if c.mcp {
					rc.Services = mcpServices()
				}
				plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
				if err != nil {
					t.Fatal(err)
				}
				checkGolden(t, filepath.Join("testdata", "plans", id+"-"+c.name+".json"), planJSON(t, plan, rc.Task.Dir, cfg.BenchRoot))
			})
		}
	}
}

// TestPlanUnchangedWithoutScenario checks that every way of having no skills
// and no MCP servers gives the plain plan: skills disabled on a task that has
// some, and services that include no MCP server.
func TestPlanUnchangedWithoutScenario(t *testing.T) {
	for _, id := range []string{"codex", "claude-code", "opencode"} {
		t.Run(id, func(t *testing.T) {
			agent := loadAgent(t, filepath.Join("..", "agents", id+".yaml"))
			rc, cfg := fixture(t, agent)
			plain, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			writeTree(t, rc.Task.Dir, map[string]string{"skills/pdf/SKILL.md": "p"})
			rc.Task.Skills = "skills"
			agent.Skills = &schemas.AgentSkills{Enabled: boolPtr(false)}
			rc.Services = mcpServices()[2:3]
			other, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
			if err != nil {
				t.Fatal(err)
			}
			a := planJSON(t, plain, rc.Task.Dir, cfg.BenchRoot)
			b := planJSON(t, other, rc.Task.Dir, cfg.BenchRoot)
			if a != b {
				t.Fatalf("plan changed:\n%s\n%s", a, b)
			}
		})
	}
}

// planJSON is a plan as indented JSON with the test's host folders replaced.
func planJSON(t *testing.T, plan *schemas.RunPlan, taskDir, benchRoot string) string {
	t.Helper()
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b) + "\n"
	for dir, name := range map[string]string{taskDir: "$TASK", benchRoot: "$BENCH"} {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			s = strings.ReplaceAll(s, real, name)
		}
		s = strings.ReplaceAll(s, dir, name)
	}
	return s
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updatePlans {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Fatalf("plan differs from %s:\n got:\n%s\nwant:\n%s", path, got, want)
	}
}
