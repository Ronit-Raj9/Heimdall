package handlers

import (
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestComparisonPlanUsesTaskImageRule (PAID-8): the comparison page posts
// the experiment to /experiments/plan; codex on a benchmark whose tasks bring
// their own image must reach the planner as the wizard sends it (built on
// each task's image), not as the plain agent the planner refuses. A user's
// own override is kept. An agent that cannot be rebuilt is refused with the
// evaluation's sentence (TestComparisonRefusesUnbuildableArm).
func TestComparisonPlanUsesTaskImageRule(t *testing.T) {
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite()}}
	agents := taskImageAgents{
		{Path: "agents/codex.yaml", Config: codexLike()},
		{Path: "agents/svc.yaml", Config: &schemas.AgentConfig{ID: "svc", Adapter: "remote_http"}},
	}
	l := &fakeLauncher{}
	pass := func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	h := NewExperimentsHandler(schemas.ModeLive, nil, nil, l, nil, pass).WithTaskImageAgents(suites, agents, nil)
	body := `{"name":"cmp","suite":"hd-harbor-tb2@1.0.0","base":"agents/codex.yaml",
		"arms":{"A":{"label":"codex a"},"B":{"label":"codex b","set":{"image":"mine:1"}}},
		"conditions":[{"id":"clean","tasks":["t01","t02"]}]}`
	resp := call(t, func(r *router.Router) { h.RegisterRoutes(r) }, nil, "POST", APIPrefix+"/experiments/plan", body, "Content-Type", "application/json")
	if resp.StatusCode() != fasthttp.StatusOK || l.planned == nil {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	a, b := l.planned.Arms["A"], l.planned.Arms["B"]
	if a.Set["build.base_arg"] != taskImageBaseArg {
		t.Errorf("arm A set %+v, want the task-image build", a.Set)
	}
	if b.Set["build.base_arg"] != taskImageBaseArg || b.Set["image"] != "mine:1" {
		t.Errorf("arm B set %+v, want the task-image build and the user's image", b.Set)
	}
}

// N1 (round 6): the New comparison form sends two agents as a matrix with no
// arms (matrixBuilderSchema.ts toMatrixExperiment). The derive ran before the
// planner expanded the matrix, saw no arms and left codex plain, so the
// planner refused it ("Agent codex runs in its own container ..."). Both
// arms must now carry the task-image build, at plan and at create
// (engine TestPlanMatrixAcceptsTaskImageArms: the planner accepts them).
func TestComparisonMatrixTwoAgentsTaskImage(t *testing.T) {
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite()}}
	bfcl := codexLike()
	bfcl.ID, bfcl.Origin, bfcl.Argv = "codex-bfcl", "codex", []string{"codex", "exec"}
	bfcl.Image, bfcl.Build = "bench/agent-codex-bfcl:1", &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex-bfcl:1"}
	agents := taskImageAgents{{Path: "agents/codex.yaml", Config: codexLike()}, {Path: "agents/codex-bfcl.yaml", Config: bfcl}}
	body := `{"id":"","name":"cmp","suite":"hd-harbor-tb2@1.0.0","base":"agents/codex.yaml","arms":{},
		"matrix":{"agents":["agents/codex.yaml","agents/codex-bfcl.yaml"],"models":[{"model":"gpt-6-luna","provider":"openai"}]},
		"primary":{"condition":"clean","metric":"success"},"guards":[{"rule":"no_new_integrity_violation"}],
		"conditions":[{"id":"clean","tasks":{"all":true},"trials":1}],"budget":{"total_usd":1},
		"verdict":{"policy":"paired_signflip","alpha":0.05,"assumed_sd":0.3},"seed":20261128,"concurrency":2,"manifest_mode":"agent_exposure"}`
	for _, path := range []string{"/experiments/plan", "/experiments"} {
		l := &fakeLauncher{}
		pass := func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
		h := NewExperimentsHandler(schemas.ModeLive, nil, nil, l, nil, pass).WithTaskImageAgents(suites, agents, nil)
		resp := call(t, func(r *router.Router) { h.RegisterRoutes(r) }, nil, "POST", APIPrefix+path, body, "Content-Type", "application/json")
		if resp.StatusCode() >= 300 || l.planned == nil {
			t.Fatalf("%s: status %d: %s", path, resp.StatusCode(), resp.Body())
		}
		if len(l.planned.Arms) != 2 || l.planned.Matrix == nil {
			t.Fatalf("%s: arms %+v, want the matrix's two arms beside the matrix", path, l.planned.Arms)
		}
		for id, arm := range l.planned.Arms {
			if arm.Set["build.base_arg"] == nil || arm.Set["build.dockerfile"] != "agents/images/codex-task/Dockerfile" || arm.Set["model"] != "gpt-6-luna" {
				t.Errorf("%s: arm %s (%s) set %+v, want the codex-task build and the model", path, id, arm.Agent, arm.Set)
			}
		}
	}
}

// An agent that cannot be built on the task images is refused at the plan
// with the evaluation's sentence (lane H1), not left for the planner.
func TestComparisonRefusesUnbuildableArm(t *testing.T) {
	suites := &fakeSuites{ids: []string{"hd-harbor-tb2"}, suites: map[string]*schemas.Suite{"hd-harbor-tb2": taskImageSuite()}}
	agents := taskImageAgents{{Path: "agents/codex.yaml", Config: codexLike()}, {Path: "agents/svc.yaml", Config: &schemas.AgentConfig{ID: "svc", Adapter: "remote_http"}}}
	body := `{"name":"cmp","suite":"hd-harbor-tb2@1.0.0","base":"agents/codex.yaml","arms":{},
		"matrix":{"agents":["agents/codex.yaml","agents/svc.yaml"]},"conditions":[{"id":"clean","tasks":{"all":true},"trials":1}]}`
	l := &fakeLauncher{}
	pass := func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	h := NewExperimentsHandler(schemas.ModeLive, nil, nil, l, nil, pass).WithTaskImageAgents(suites, agents, nil)
	for _, path := range []string{"/experiments/plan", "/experiments"} {
		resp := call(t, func(r *router.Router) { h.RegisterRoutes(r) }, nil, "POST", APIPrefix+path, body, "Content-Type", "application/json")
		got := string(resp.Body())
		if l.planned != nil || !strings.Contains(got, "agent svc") || !strings.Contains(got, "create the agent from a template") || strings.Contains(got, "runs in its own container") {
			t.Errorf("%s: status %d body %s, want the task-image sentence", path, resp.StatusCode(), got)
		}
	}
}
