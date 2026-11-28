package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// taskImageSuite is a Harbor-style benchmark: snapshot verifier, every task
// brings its own image.
func taskImageSuite() *schemas.Suite {
	var tasks []*schemas.Task
	var ids []string
	for _, id := range []string{"t01", "t02", "t03"} {
		tasks = append(tasks, &schemas.Task{ID: id, Environment: &schemas.TaskEnvironment{Image: "harbor/" + id + ":1"}})
		ids = append(ids, id)
	}
	return &schemas.Suite{ID: "hd-harbor-tb2", Version: "1.0.0", TaskIDs: ids, Tasks: tasks,
		Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}}
}

func codexLike() *schemas.AgentConfig {
	return &schemas.AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"}}
}

func TestDeriveTaskImageAgent(t *testing.T) {
	st := taskImageSuite()
	v := deriveTaskImageAgent(codexLike(), st, nil, nil, "")
	if v.Skip != "" || v.Set["build.base_arg"] != taskImageBaseArg || v.Note == "" {
		t.Fatalf("codex variant %+v", v)
	}
	// The library's codex-task build wins over the generic one.
	lib := &schemas.AgentConfig{ID: "codex-task", Adapter: "command", Image: "bench/agent-codex-task:1",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex-task/Dockerfile", Tag: "bench/agent-codex-task:1", BaseArg: "TASK_BASE"}}
	v = deriveTaskImageAgent(codexLike(), st, []string{"t02"}, map[string]*schemas.AgentConfig{"agents/codex-task.yaml": lib}, "")
	if v.Set["build.base_arg"] != "TASK_BASE" || v.Set["build.dockerfile"] != lib.Build.Dockerfile || v.Set["image"] != lib.Image {
		t.Fatalf("library variant %+v", v)
	}
	// Already task-image, scripted, or a suite without own task images: nothing to do.
	ready := codexLike()
	ready.Build.BaseArg = "BASE_IMAGE"
	plain := taskImageSuite()
	plain.Verifier.Mode = ""
	for name, c := range map[string]struct {
		cfg *schemas.AgentConfig
		st  *schemas.Suite
	}{"ready": {ready, st}, "scripted": {&schemas.AgentConfig{ID: "s", Adapter: "scripted"}, st}, "plain": {codexLike(), plain}, "nil": {nil, st}} {
		if v := deriveTaskImageAgent(c.cfg, c.st, nil, nil, ""); v.Set != nil || v.Skip != "" {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	// Agents that cannot be rebuilt on a task image skip the benchmark.
	for _, cfg := range []*schemas.AgentConfig{
		{ID: "svc", Adapter: "remote_http"},
		{ID: "pre", Adapter: "command", Image: "acme/agent:1"},
	} {
		if v := deriveTaskImageAgent(cfg, st, nil, nil, ""); v.Skip != taskImageSkipReason {
			t.Fatalf("%s: %+v", cfg.ID, v)
		}
	}
	// A Dockerfile that does not build FROM the argument is refused honestly
	// (an agent without a shipped -task variant).
	acme := func() *schemas.AgentConfig {
		c := codexLike()
		c.ID = "acme"
		return c
	}
	root := t.TempDir()
	df := filepath.Join(root, "agents/images/codex/Dockerfile")
	_ = os.MkdirAll(filepath.Dir(df), 0o755)
	_ = os.WriteFile(df, []byte("FROM bench/coding-core-base:1\n"), 0o644)
	if v := deriveTaskImageAgent(acme(), st, nil, nil, root); !strings.HasPrefix(v.Skip, taskImageSkipReason) {
		t.Fatalf("fixed FROM: %+v", v)
	}
	_ = os.WriteFile(df, []byte("ARG BASE_IMAGE=bench/coding-core-base:1\nFROM ${BASE_IMAGE}\n"), 0o644)
	if v := deriveTaskImageAgent(acme(), st, nil, nil, root); v.Skip != "" || v.Set == nil {
		t.Fatalf("ARG FROM: %+v", v)
	}
}

type taskImageAgents []AgentConfigFile

func (a taskImageAgents) AgentConfigs(context.Context) ([]AgentConfigFile, error) { return a, nil }

func TestEvaluationTaskImageVariant(t *testing.T) {
	old := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) { return true, nil, "" }
	defer func() { evalCompat = old }()
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite()}}
	agents := taskImageAgents{
		{Path: "agents/codex.yaml", Config: codexLike()},
		{Path: "agents/svc.yaml", Config: &schemas.AgentConfig{ID: "svc", Adapter: "remote_http"}},
	}
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: suites, Agents: agents})
	writeFiles(t, dir, map[string]string{"agents/codex.yaml": "id: codex\n", "agents/svc.yaml": "id: svc\n"})

	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"mode":"score","arms":[{"label":"A","agent":"codex","model":"gpt-5.6-terra"}],"benchmarks":["hd-harbor-tb2"],"tasks_per_benchmark":2,"trials":1}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	c := ev.Children[0]
	if c.Skipped != "" || c.ExperimentID == "" {
		t.Fatalf("child %+v", c)
	}
	if ev.Spec.Arms[0].Agent != "codex" {
		t.Fatalf("arm agent changed: %+v", ev.Spec.Arms)
	}
	exp := l.planned[c.ExperimentID]
	if len(exp.Arms) != 1 {
		t.Fatalf("score mode plans one arm, got %+v", exp.Arms)
	}
	for _, id := range []string{"A"} {
		if exp.Arms[id].Set["build.base_arg"] != taskImageBaseArg {
			t.Fatalf("arm %s set %+v", id, exp.Arms[id].Set)
		}
	}
	if exp.Base != "agents/codex.yaml" || !strings.HasPrefix(exp.Arms["A"].Label, "codex on ") {
		t.Fatalf("experiment base %s label %s", exp.Base, exp.Arms["A"].Label)
	}
	ps, _ := projectfs.Open(dir)
	data, _, _ := ps.Read(t.Context(), projectfs.AreaExperiments, exp.Name+".yaml")
	if !strings.Contains(string(data), "# Task images: arm A: agent codex is built on each task's own image") {
		t.Fatalf("variant not recorded:\n%s", data)
	}

	ctx = evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"mode":"score","arms":[{"label":"A","agent":"svc","model":"gpt-5.6-terra"}],"benchmarks":["hd-harbor-tb2"],"trials":1}`)
	if ev := decodeEval(t, ctx); !strings.Contains(ev.Children[0].Skipped, "runs outside that workspace") { // J5: the output rule skips it first
		t.Fatalf("remote child %+v", ev.Children[0])
	}
}

func TestEstimateTaskImageVariant(t *testing.T) {
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite()}}
	agents := map[string]*schemas.AgentConfig{"agents/codex.yaml": codexLike(), "agents/svc.yaml": {ID: "svc", Adapter: "remote_http"}}
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"hd-harbor-tb2"}, TasksPerBenchmark: 2, Trials: 1}
	est, err := EstimateEvaluationWith(context.Background(), suites, src, req, agents, "")
	if err != nil {
		t.Fatal(err)
	}
	be := est.PerBenchmark[0]
	if be.Skipped != "" || be.Runs != 2 || src.lastExp.Arms["A"].Set["build.base_arg"] != taskImageBaseArg || len(src.lastExp.Arms) != 1 {
		t.Fatalf("estimate %+v exp %+v", be, src.lastExp)
	}
	req = &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "svc", Model: "gpt-x"}}, Benchmarks: []string{"hd-harbor-tb2"}}
	est, err = EstimateEvaluationWith(context.Background(), suites, src, req, agents, "")
	if err != nil || !strings.Contains(est.PerBenchmark[0].Skipped, "runs outside that workspace") || est.Runs != 0 { // J5
		t.Fatalf("remote estimate %v %+v", err, est)
	}
}

// The compatibility check never blocks a container agent on a task-image
// benchmark: the evaluation builds the variant.
func TestCompatTaskImageContainerAgentOK(t *testing.T) {
	st := taskImageSuite()
	st.Track = "terminal"
	if ok, missing, reason := compat.Check(codexLike(), st); !ok {
		t.Fatalf("codex blocked: %v %s", missing, reason)
	}
}
