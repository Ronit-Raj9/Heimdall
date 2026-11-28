package handlers

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// k1SWEVerifiedSuites holds hd-swe-verified as the swebench importer writes it:
// docker workspace, verifier command in a clean room, bugfix tasks in their
// own image, no probes, no tool service.
func k1SWEVerifiedSuites() *fakeSuites {
	tasks := []*schemas.Task{}
	ids := []string{"astropy__astropy-12907", "django__django-11099", "sympy__sympy-20590"}
	for _, id := range ids {
		tasks = append(tasks, &schemas.Task{ID: id, Kind: schemas.TaskBugfix, RepoDir: "repo",
			Environment: &schemas.TaskEnvironment{Image: "swebench/sweb.eval.x86_64." + id + ":latest", WorkspacePath: "/testbed"}})
	}
	return &fakeSuites{ids: []string{"hd-swe-verified"}, suites: map[string]*schemas.Suite{
		"hd-swe-verified": {ID: "hd-swe-verified", Version: "2.0.0", Environment: "docker_workspace", Grader: "verifier_command",
			Workdir: "/testbed", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierCleanRoom}, TaskIDs: ids, Tasks: tasks},
	}}
}

// The wizard's body with "Broken tool" and "Misleading instructions" ticked
// on SWE-bench Verified: the estimate no longer skips them and the launch
// plans a fault condition for each.
func TestSWEVerifiedTakesBrokenToolAndMisleadingFromWizardBody(t *testing.T) {
	old := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) { return true, nil, "" }
	defer func() { evalCompat = old }()
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: k1SWEVerifiedSuites(), OrgID: "org1"})
	body := `{"arms":[{"label":"A","agent":"scripted","model":"gpt-6-luna","provider":"openai"}],"benchmarks":["hd-swe-verified"],"tasks_per_benchmark":3,"trials":1,"faults":["broken-tool","misleading-instruction"],"budget_usd":1}`

	est := evalCall(r, "POST", APIPrefix+"/evaluations/estimate", body)
	var e EvalEstimate
	if err := json.Unmarshal(est.Response.Body(), &e); err != nil || len(e.PerBenchmark) != 1 {
		t.Fatalf("estimate %s %v", est.Response.Body(), err)
	}
	b := e.PerBenchmark[0]
	if b.Skipped != "" || len(b.FaultsSkip) != 0 {
		t.Fatalf("estimate still skips: %+v", b)
	}
	if !slices.Contains(b.Conditions, schemas.GenericProbeTestCrash) || !slices.Contains(b.Conditions, schemas.GenericProbeMisleadingNotes) || b.Runs != 3*3 {
		t.Fatalf("estimate conditions %v runs %d", b.Conditions, b.Runs)
	}

	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if len(ev.Children) != 1 || len(ev.Children[0].FaultsSkipped) != 0 {
		t.Fatalf("launch children %+v", ev.Children)
	}
	if fo := ev.Children[0].FaultOf; fo[schemas.GenericProbeTestCrash] != "broken-tool" || fo[schemas.GenericProbeMisleadingNotes] != "misleading-instruction" {
		t.Fatalf("fault_of %v", fo)
	}
	exp := l.planned[ev.Children[0].ExperimentID]
	var conds []string
	for _, c := range exp.Conditions {
		conds = append(conds, c.ID)
	}
	if !slices.Equal(conds, []string{"clean", schemas.GenericProbeTestCrash, schemas.GenericProbeMisleadingNotes}) {
		t.Fatalf("planned conditions %v", conds)
	}
}
