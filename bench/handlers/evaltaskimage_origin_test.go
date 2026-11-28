package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// An agent made in the web builder from the Codex CLI template runs on a
// benchmark whose tasks bring their own image (BFCL, Terminal-Bench) through
// the codex-task variant of the agent it was made from; changed to another
// program it is refused with the plain sentence.
func TestTemplateAgentFollowsOriginTaskVariant(t *testing.T) {
	t.Setenv(suite.LibraryEnv, "")
	r, ps := builderRouter(t)
	root := ps.Root()
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "codex-bfcl"}); code != 201 {
		t.Fatalf("create %d %s", code, body)
	}
	doc, err := os.ReadFile(filepath.Join(root, "agents", "codex-bfcl.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(doc, "agents/codex-bfcl.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Origin != "codex" || cfg.Image != "bench/agent-codex-bfcl:1" {
		t.Fatalf("origin %q image %q", cfg.Origin, cfg.Image)
	}
	st := taskImageSuite()
	st.BaseImage = schemas.ImageSpec{Tag: "bench/tb-base:1"}
	agents := map[string]*schemas.AgentConfig{"agents/codex-bfcl.yaml": cfg}
	v := deriveTaskImageAgent(cfg, st, nil, agents, root)
	if v.Skip != "" || v.Set["build.dockerfile"] != "agents/images/codex-task/Dockerfile" || v.Set["image"] != "bench/agent-codex-task:1" || v.Set["build.base_arg"] != "BASE_IMAGE" {
		t.Fatalf("variant %+v", v)
	}
	// The wizard's check (GET /agents/{id}/compatibility) says runnable.
	harbor := &schemas.Suite{ID: "hd-harbor-bfcl", Track: "terminal", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot},
		Tasks: []*schemas.Task{{ID: "t1", Environment: &schemas.TaskEnvironment{Image: "harbor/bfcl-t1:1"}}}}
	env := OrgEnv{Mode: "local", Cfg: &lib.Config{Root: root}, Services: Services{
		Suites: &fakeSuites{ids: []string{"hd-harbor-bfcl"}, suites: map[string]*schemas.Suite{"hd-harbor-bfcl": harbor}},
		Agents: compatAgents{{Path: "agents/codex-bfcl.yaml", Config: cfg}}}}
	if code, out := doCompat(t, env, "codex-bfcl"); code != 200 || len(out.Benchmarks) != 1 || !out.Benchmarks[0].OK {
		t.Fatalf("compatibility %d %+v", code, out)
	}
	run := applySet(cfg, v.Set)
	img, err := engine.ResolveTaskAgentImage(&run, st, st.TaskByID("t01"), "harbor/t01:1", root, "")
	if err != nil || !img.PerTask || img.BuildArgs["BASE_IMAGE"] != "harbor/t01:1" {
		t.Fatalf("image %+v err %v", img, err)
	}

	// A form save keeps the origin.
	if code, body := abCall(r, "PUT", APIPrefix+"/agents/codex-bfcl/config", AgentConfigPut{Fields: &AgentConfigFields{Model: "gpt-5.4-mini", Provider: "openai", WallS: 600, CPUs: 2, MemGB: 4}}); code != 200 {
		t.Fatalf("form save %d %s", code, body)
	}
	doc, _ = os.ReadFile(filepath.Join(root, "agents", "codex-bfcl.yaml"))
	if c, err := decodeAgent(doc, "agents/codex-bfcl.yaml"); err != nil || c.Origin != "codex" {
		t.Fatalf("after form save: %v %+v", err, c)
	}

	// The command changed to another program: no Codex variant.
	edited := strings.Replace(string(doc), "  - codex\n  - exec\n", "  - aider\n  - --yes\n", 1)
	if edited == string(doc) {
		t.Fatalf("argv not found in:\n%s", doc)
	}
	if code, body := abCall(r, "PUT", APIPrefix+"/agents/codex-bfcl/config", AgentConfigPut{YAML: edited}); code != 200 {
		t.Fatalf("yaml save %d %s", code, body)
	}
	other, err := decodeAgent([]byte(edited), "agents/codex-bfcl.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env.Services.Agents = compatAgents{{Path: "agents/codex-bfcl.yaml", Config: other}}
	if code, out := doCompat(t, env, "codex-bfcl"); code != 200 || len(out.Benchmarks) != 1 || out.Benchmarks[0].OK ||
		!strings.Contains(out.Benchmarks[0].Reason, "cannot be built on top of it") {
		t.Fatalf("compatibility after the command change %d %+v", code, out)
	}
	v = deriveTaskImageAgent(other, st, nil, agents, root)
	if v.Skip != taskImageSkip("codex-bfcl", "its Dockerfile does not start FROM ${BASE_IMAGE}") {
		t.Fatalf("changed command: %+v", v)
	}
	for _, want := range []string{"runs the agent inside each task's own image", "cannot be built on top of it", "Codex CLI", "base_arg: BASE_IMAGE"} {
		if !strings.Contains(v.Skip, want) {
			t.Fatalf("reason %q lacks %q", v.Skip, want)
		}
	}
	if strings.Contains(v.Skip, "register a container agent") {
		t.Fatalf("old wording: %q", v.Skip)
	}
}

// An agent created before the origin field existed is matched to a shipped
// agent conservatively: by the built-in image, or by adapter, launch program
// and parser together.
func TestPreOriginAgentInferred(t *testing.T) {
	t.Setenv(suite.LibraryEnv, "")
	root := orgWithOwnCodex(t)
	st := taskImageSuite()
	old := &schemas.AgentConfig{ID: "my-codex", Adapter: "command", Image: "bench/agent-codex:1", Argv: []string{"codex", "exec"},
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"}}
	if v := deriveTaskImageAgent(old, st, nil, nil, root); v.Skip != "" || v.Set["build.dockerfile"] != "agents/images/codex-task/Dockerfile" {
		t.Fatalf("built-in image: %+v", v)
	}
	copied := &schemas.AgentConfig{ID: "codex-bfcl", Adapter: "command", Image: "bench/agent-codex-bfcl:1", Argv: []string{"codex", "exec"}, Parser: "codex",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex-bfcl:1"}}
	if v := deriveTaskImageAgent(copied, st, nil, nil, root); v.Skip != "" || v.Set["image"] != "bench/agent-codex-task:1" {
		t.Fatalf("template copy: %+v", v)
	}
	// Same program, no image or parser match: not inferred.
	copied.Parser = ""
	if v := deriveTaskImageAgent(copied, st, nil, nil, root); v.Skip != taskImageSkip("codex-bfcl", "its Dockerfile does not start FROM ${BASE_IMAGE}") {
		t.Fatalf("no evidence: %+v", v)
	}
}
