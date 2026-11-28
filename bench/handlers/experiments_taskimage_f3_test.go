package handlers

import (
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestComparisonPlanUsesTaskImageRule (PAID-8): the comparison page posts
// the experiment to /experiments/plan; codex on a benchmark whose tasks bring
// their own image must reach the planner as the wizard sends it (built on
// each task's image), not as the plain agent the planner refuses. A user's
// own override is kept, and an agent that cannot be rebuilt is left as is
// for the planner to refuse with its reason.
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
		"arms":{"A":{"label":"codex a"},"B":{"label":"codex b","set":{"image":"mine:1"}},"C":{"label":"svc","agent":"agents/svc.yaml"}},
		"conditions":[{"id":"clean","tasks":["t01","t02"]}]}`
	resp := call(t, func(r *router.Router) { h.RegisterRoutes(r) }, nil, "POST", APIPrefix+"/experiments/plan", body, "Content-Type", "application/json")
	if resp.StatusCode() != fasthttp.StatusOK || l.planned == nil {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	a, b, c := l.planned.Arms["A"], l.planned.Arms["B"], l.planned.Arms["C"]
	if a.Set["build.base_arg"] != taskImageBaseArg {
		t.Errorf("arm A set %+v, want the task-image build", a.Set)
	}
	if b.Set["build.base_arg"] != taskImageBaseArg || b.Set["image"] != "mine:1" {
		t.Errorf("arm B set %+v, want the task-image build and the user's image", b.Set)
	}
	if len(c.Set) != 0 {
		t.Errorf("arm C set %+v, want nothing: the planner refuses it", c.Set)
	}
}
