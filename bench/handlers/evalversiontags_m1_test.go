package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// m1Agent creates agent bot from the Codex CLI template through the
// builder's create route and saves a second version through the form.
func m1Agent(t *testing.T) (*evalLauncher, func(body string) *schemas.Experiment, string) {
	t.Helper()
	t.Setenv(suite.LibraryEnv, "")
	old := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) { return true, nil, "" }
	t.Cleanup(func() { evalCompat = old })
	br, ps := builderRouter(t)
	root := ps.Root()
	if code, body := abCall(br, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "bot"}); code != 201 {
		t.Fatalf("create %d %s", code, body)
	}
	if code, body := abCall(br, "PUT", APIPrefix+"/agents/bot/config", AgentConfigPut{Fields: &AgentConfigFields{Model: "gpt-5.4-mini", Provider: "openai", WallS: 1234, CPUs: 2, MemGB: 4}}); code != 200 {
		t.Fatalf("form save %d %s", code, body)
	}
	if vs, err := agentversions.List(root, "bot"); err != nil || len(vs) != 2 {
		t.Fatalf("versions %+v %v", vs, err)
	}
	doc, err := os.ReadFile(filepath.Join(root, "agents", "bot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(doc, "agents/bot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	suites := evalSuites()
	suites.ids = append(suites.ids, "hd-harbor-bfcl")
	ti := taskImageSuite()
	ti.ID, ti.BaseImage = "hd-harbor-bfcl", schemas.ImageSpec{Tag: "bench/tb-base:1"}
	suites.suites["hd-harbor-bfcl"] = ti
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: suites, Projects: ps, Agents: taskImageAgents{{Path: "agents/bot.yaml", Config: cfg}}})
	post := func(body string) *schemas.Experiment {
		t.Helper()
		ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
		if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
			t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		c := decodeEval(t, ctx).Children[0]
		if c.Skipped != "" || c.ExperimentID == "" {
			t.Fatalf("child %+v", c)
		}
		return l.planned[c.ExperimentID]
	}
	return l, post, root
}

// m1Images resolves every arm's image as the planner does (the pinned
// snapshot or the current file, the arm's overrides, then the image), the
// per-task image for task when it is set.
func m1Images(t *testing.T, exp *schemas.Experiment, root string, st *schemas.Suite, task string) map[string]engine.AgentImage {
	t.Helper()
	out := map[string]engine.AgentImage{}
	for label, arm := range exp.Arms {
		path := arm.Agent
		if path == "" {
			path = exp.Base
		}
		rel := path
		if base, id, n, ok := schemas.ParseAgentVersionRef(path); ok {
			_ = base
			rel = schemas.AgentVersionSnapshotPath(id, n)
		}
		doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("arm %s: %v", label, err)
		}
		cfg, err := engine.ResolveAgentConfig(doc, arm.Set)
		if err != nil {
			t.Fatalf("arm %s: %v", label, err)
		}
		var img engine.AgentImage
		if task != "" {
			img, err = engine.ResolveTaskAgentImage(&cfg, st, st.TaskByID(task), "harbor/"+task+":1", root, "")
		} else {
			img, err = engine.ResolveAgentImageIn(&cfg, st, root, "")
		}
		if err != nil {
			t.Fatalf("arm %s: %v", label, err)
		}
		if _, err := os.Stat(img.Dockerfile); err != nil {
			t.Fatalf("arm %s: build file %q not found: %v", label, img.Dockerfile, err)
		}
		out[label] = img
	}
	return out
}

const m1Versions = `{"mode":"versions","arms":[{"label":"A","agent":"bot","version":1,"model":"gpt-5.4-mini","provider":"openai"},{"label":"B","agent":"bot","version":2,"model":"gpt-5.4-mini","provider":"openai"}],"benchmarks":["%s"],"tasks_per_benchmark":1,"trials":1}`

// The wizard's versions request for a template agent with two saved
// versions: both arms plan a buildable image, one shared tag when only
// settings differ; on a task-image benchmark both get the per-task variant.
func TestVersionsEvaluationArmsBuildLikeScore(t *testing.T) {
	_, post, root := m1Agent(t)
	exp := post(strings.Replace(m1Versions, "%s", "hd-swe-verified", 1))
	if exp.Arms["A"].Agent != "agents/bot.yaml@v1" || exp.Arms["B"].Agent != "agents/bot.yaml@v2" {
		t.Fatalf("arms %+v", exp.Arms)
	}
	st := evalSuites().suites["hd-swe-verified"]
	imgs := m1Images(t, exp, root, st, "")
	if imgs["A"].Tag != "bench/agent-bot:1" || imgs["B"].Tag != imgs["A"].Tag || imgs["A"].PerSuite {
		t.Fatalf("settings-only versions must share one plain image: %+v", imgs)
	}

	exp = post(strings.Replace(m1Versions, "%s", "hd-harbor-bfcl", 1))
	ti := taskImageSuite()
	ti.ID, ti.BaseImage = "hd-harbor-bfcl", schemas.ImageSpec{Tag: "bench/tb-base:1"}
	imgs = m1Images(t, exp, root, ti, "t01")
	for _, label := range []string{"A", "B"} {
		img := imgs[label]
		if !img.PerTask || img.BuildArgs["BASE_IMAGE"] != "harbor/t01:1" || !strings.HasSuffix(filepath.ToSlash(img.Dockerfile), "agents/images/codex-task/Dockerfile") {
			t.Fatalf("arm %s not the per-task variant: %+v", label, img)
		}
	}
	if imgs["A"].Tag != imgs["B"].Tag {
		t.Fatalf("settings-only versions must share one per-task image: %+v", imgs)
	}
}

// Versions whose build inputs differ get different tags; the request body
// round-trips unchanged otherwise.
func TestVersionsEvaluationDifferentBuildDifferentTag(t *testing.T) {
	_, post, root := m1Agent(t)
	df := filepath.Join(root, "agents", "images", "bot")
	entries, err := os.ReadDir(df)
	if err != nil || len(entries) == 0 {
		t.Fatalf("template images folder %s: %v", df, err)
	}
	if err := os.WriteFile(filepath.Join(df, "Dockerfile.slim"), []byte("FROM busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, _ := os.ReadFile(filepath.Join(root, "agents", "bot.yaml"))
	edited := strings.Replace(string(doc), "agents/images/bot/Dockerfile\n", "agents/images/bot/Dockerfile.slim\n", 1)
	if edited == string(doc) {
		t.Fatalf("dockerfile line not found:\n%s", doc)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "bot.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	v, _, err := agentversions.Snapshot(root, "bot", []byte(edited), "Ann", "slim image")
	if err != nil || v.Version != 3 {
		t.Fatalf("v3 %+v %v", v, err)
	}
	body := strings.Replace(strings.Replace(m1Versions, "%s", "hd-swe-verified", 1), `"version":2`, `"version":3`, 1)
	exp := post(body)
	imgs := m1Images(t, exp, root, evalSuites().suites["hd-swe-verified"], "")
	if imgs["A"].Tag != "bench/agent-bot:1" || imgs["B"].Tag != "bench/agent-bot:1-v3" || !strings.HasSuffix(imgs["B"].Dockerfile, "Dockerfile.slim") {
		raw, _ := json.Marshal(exp.Arms)
		t.Fatalf("different builds share a tag: %+v arms %s", imgs, raw)
	}
}
