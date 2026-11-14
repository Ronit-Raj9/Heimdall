package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestParseInterspersed(t *testing.T) {
	tests := []struct {
		args    []string
		pos     []string
		tmpl    string
		wantErr bool
	}{
		{[]string{"bot", "-template", "command"}, []string{"bot"}, "command", false},
		{[]string{"-template", "command", "bot"}, []string{"bot"}, "command", false},
		{[]string{"a", "-template", "http-service", "b"}, []string{"a", "b"}, "http-service", false},
		{[]string{"--", "-x"}, []string{"-x"}, "python", false},
		{[]string{"bot", "-nope"}, nil, "", true},
	}
	for _, tt := range tests {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(&bytes.Buffer{})
		tmpl := fs.String("template", "python", "")
		pos, err := parseInterspersed(fs, tt.args)
		if (err != nil) != tt.wantErr {
			t.Errorf("%v: err %v", tt.args, err)
			continue
		}
		if !tt.wantErr && (!reflect.DeepEqual(pos, tt.pos) || *tmpl != tt.tmpl) {
			t.Errorf("%v: pos %v template %s", tt.args, pos, *tmpl)
		}
	}
}

func TestAgentCommandUsage(t *testing.T) {
	if code := runAgent(nil); code != exitUsage {
		t.Errorf("no subcommand: exit %d", code)
	}
	if code := runAgent([]string{"teleport"}); code != exitUsage {
		t.Errorf("unknown subcommand: exit %d", code)
	}
	if code := runAgentInit([]string{}, &bytes.Buffer{}); code != exitUsage {
		t.Errorf("init without id: exit %d", code)
	}
	if code := runAgentBuild([]string{}, &bytes.Buffer{}); code != exitUsage {
		t.Errorf("build without file: exit %d", code)
	}
	if code := runAgentCheck([]string{"agents/x.yaml"}, &bytes.Buffer{}); code != exitUsage {
		t.Errorf("check without -suite: exit %d", code)
	}
}

// TestAgentInitTemplates writes every template, refuses to overwrite it,
// then decodes the YAML the way the planner does, resolves its per-suite
// image and plans it through the real command adapter.
func TestAgentInitTemplates(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "deploy", "bench.yaml")
	suite := &schemas.Suite{ID: "coding-core", BaseImage: schemas.ImageSpec{Tag: "bench/coding-core-base:1"}}
	for _, tmpl := range templates.Names {
		t.Run(tmpl, func(t *testing.T) {
			id := "acme-" + strings.ReplaceAll(tmpl, "-", "")
			var out bytes.Buffer
			if code := runAgentInit([]string{id, "-template", tmpl, "-config", cfg}, &out); code != exitOK {
				t.Fatalf("init: exit %d %s", code, out.String())
			}
			if code := runAgentInit([]string{id, "-template", tmpl, "-config", cfg}, &out); code != exitInfra {
				t.Fatalf("second init overwrote: exit %d", code)
			}
			if code := runAgentInit([]string{id, "-template", tmpl, "-config", cfg, "-force"}, &out); code != exitOK {
				t.Fatalf("init -force: exit %d", code)
			}
			path := filepath.Join(root, "agents", id+".yaml")
			agent, err := loadAgentConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if agent.Image != "bench/agent-"+id+":1" || agent.Build == nil || agent.Build.BaseArg != "BASE_IMAGE" {
				t.Fatalf("agent = %+v", agent)
			}
			img, err := engine.ResolveAgentImage(agent, suite, root)
			if err != nil {
				t.Fatal(err)
			}
			if img.Tag != agent.Image+"-coding-core" || img.BuildArgs["BASE_IMAGE"] != "bench/coding-core-base:1" ||
				img.Context != filepath.Join(root, "agents", "images", id) {
				t.Fatalf("image = %+v", img)
			}
			if _, err := os.Stat(img.Dockerfile); err != nil {
				t.Fatalf("Dockerfile: %v", err)
			}
			agent.Image = img.Tag
			plan := planAgent(t, agent)
			if plan.Image != img.Tag {
				t.Errorf("plan image %s", plan.Image)
			}
			keyEnv := 0
			for k, v := range plan.Env {
				if v == "sk-bf-test-key" {
					keyEnv++
				}
				if strings.Contains(v, "{{") {
					t.Errorf("env %s not rendered: %s", k, v)
				}
			}
			for _, a := range plan.Argv {
				if strings.Contains(a, "sk-bf-test-key") {
					t.Error("run key in argv")
				}
			}
			if keyEnv != 1 || !hasValue(plan.Env, "r_test") {
				t.Errorf("env %v: want the run key once and the session id", plan.Env)
			}
			if tmpl == "http-service" && !strings.Contains(plan.Files["/tmp/bench/launcher.py"], "AGENT_SERVER_CMD") {
				t.Error("launcher not delivered through files")
			}
		})
	}
}

func TestAgentSuiteNeedsSuiteForBaseArg(t *testing.T) {
	agent := &schemas.AgentConfig{ID: "x", Build: &schemas.ImageSpec{BaseArg: "BASE_IMAGE"}}
	if _, err := agentSuite(context.Background(), nil, agent, ""); err == nil || !strings.Contains(err.Error(), "-suite") {
		t.Fatalf("err = %v", err)
	}
	if s, err := agentSuite(context.Background(), nil, &schemas.AgentConfig{ID: "y"}, ""); err != nil || s != nil {
		t.Fatalf("no base_arg, no suite: %v %v", s, err)
	}
}

func TestProjectRoot(t *testing.T) {
	got, err := projectRoot(filepath.Join("/p", "deploy", "bench.yaml"))
	if err != nil || got != filepath.Clean("/p") {
		t.Fatalf("root %q %v", got, err)
	}
}

// planAgent renders cfg through the real command adapter.
func planAgent(t *testing.T, cfg *schemas.AgentConfig) *schemas.RunPlan {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("fix the bug"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd schemas.AgentAdapter
	for _, a := range adapters.Builtin(adapters.CommandConfig{GatewayAgentURL: "http://bifrost:8080", EvalNetwork: "evalnet", BenchRoot: dir}) {
		if a.GetName() == adapters.AdapterCommand {
			cmd = a
		}
	}
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_test", ExpID: "e_test"},
		Agent: cfg,
		Task:  &schemas.Task{ID: "t01", Dir: dir, Instruction: "instruction.md"},
		Key:   &schemas.RunKey{Value: "sk-bf-test-key"},
	}
	plan, err := cmd.Plan(context.Background(), rc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func hasValue(m map[string]string, v string) bool {
	for _, x := range m {
		if x == v {
			return true
		}
	}
	return false
}
