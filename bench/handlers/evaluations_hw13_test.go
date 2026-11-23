package handlers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// The package's older evaluation tests read the children from the create
// answer, so they run with the synchronous start; the tests below turn the
// background start (the server default, N182) back on.
func TestMain(m *testing.M) {
	evalCreateInBackground = false
	os.Exit(m.Run())
}

func inBackground(t *testing.T) {
	t.Helper()
	evalCreateInBackground = true
	t.Cleanup(func() { evalCreateInBackground = false })
}

// gatedLauncher is evalLauncher whose Plan waits for the gate of the
// benchmark it plans (benchmarks without a gate plan at once).
type gatedLauncher struct {
	evalLauncher
	gates map[string]chan struct{}
}

func (l *gatedLauncher) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	suite, _, _ := strings.Cut(exp.Suite, "@")
	if g, ok := l.gates[suite]; ok {
		<-g
	}
	return l.evalLauncher.Plan(ctx, exp)
}

// waitStored polls the stored evaluation until ok says so or 10 s pass.
func waitStored(t *testing.T, dir, id string, ok func(*schemas.Evaluation) bool) *schemas.Evaluation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ev, err := evalstore.Open(dir).Load(id)
		if err == nil && ok(ev) {
			return ev
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored evaluation %s never reached the state: %+v (%v)", id, ev, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const oneArm = `"arms":[{"label":"A","agent":"scripted","model":"gpt-5.4-mini","provider":"openai"}]`

func TestCreateEvaluationAnswersBeforeChildrenStart(t *testing.T) {
	inBackground(t)
	gate := make(chan struct{})
	l := &gatedLauncher{gates: map[string]chan struct{}{"longmemeval": gate}}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	done := make(chan *fasthttp.RequestCtx, 1)
	go func() {
		done <- evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["longmemeval"],"tasks_per_benchmark":4}`)
	}()
	var ctx *fasthttp.RequestCtx
	select {
	case ctx = <-done:
	case <-time.After(10 * time.Second):
		close(gate)
		t.Fatal("POST /evaluations waited for the children to start")
	}
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		close(gate)
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if ev.Status != schemas.EvaluationQueued || len(ev.Children) != 0 || ev.Launch == nil || ev.Launch.Of != 1 || ev.Launch.Started != 0 {
		close(gate)
		t.Fatalf("answer %+v launch %+v", ev, ev.Launch)
	}
	// While the child is starting, a read keeps it queued.
	if got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID, "")); got.Status != schemas.EvaluationQueued {
		close(gate)
		t.Fatalf("status while starting %q", got.Status)
	}
	close(gate)
	final := waitStored(t, dir, ev.ID, func(e *schemas.Evaluation) bool { return e.Status != schemas.EvaluationQueued })
	if final.Status != schemas.EvaluationRunning || len(final.Children) != 1 || final.Children[0].ExperimentID == "" {
		t.Fatalf("after the start %+v", final)
	}
	if final.Launch == nil || final.Launch.Started != 1 || final.Launch.Current != "" {
		t.Fatalf("launch %+v", final.Launch)
	}
}

func TestLaunchProgressSaved(t *testing.T) {
	inBackground(t)
	gate := make(chan struct{})
	l := &gatedLauncher{gates: map[string]chan struct{}{"longmemeval": gate}}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	ev := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["hd-harbor-tb2","longmemeval"],"tasks_per_benchmark":4}`))
	mid := waitStored(t, dir, ev.ID, func(e *schemas.Evaluation) bool {
		return len(e.Children) == 1 || (e.Launch != nil && e.Launch.Started == 1)
	})
	close(gate)
	if mid.Launch == nil || mid.Launch.Started != 1 || mid.Launch.Of != 2 || mid.Launch.Current != "longmemeval" {
		t.Fatalf("progress %+v", mid.Launch)
	}
	if len(mid.Children) != 1 || mid.Children[0].Benchmark != "hd-harbor-tb2" || mid.Children[0].ExperimentID == "" {
		t.Fatalf("children so far %+v", mid.Children)
	}
	waitStored(t, dir, ev.ID, func(e *schemas.Evaluation) bool { return e.Status != schemas.EvaluationQueued && len(e.Children) == 2 })
}

func TestQueuedEvaluationAfterRestartFails(t *testing.T) {
	r, dir := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites(), OrgID: "org1"})
	old := &schemas.Evaluation{ID: "ev_20261122_200000_abcdef", Org: "org1", Status: schemas.EvaluationQueued,
		Children: []schemas.EvaluationChild{}, CreatedAt: time.Now().Add(-10 * time.Minute),
		Spec:   schemas.EvaluationSpec{Benchmarks: []string{"longmemeval"}},
		Launch: &schemas.EvaluationLaunch{Of: 1, Current: "longmemeval"}}
	young := &schemas.Evaluation{ID: "ev_20261122_200000_fedcba", Org: "org1", Status: schemas.EvaluationQueued,
		Children: []schemas.EvaluationChild{}, CreatedAt: time.Now().Add(-time.Minute),
		Spec:   schemas.EvaluationSpec{Benchmarks: []string{"longmemeval"}},
		Launch: &schemas.EvaluationLaunch{Of: 1}}
	for _, e := range []*schemas.Evaluation{old, young} {
		if err := evalstore.Open(dir).Save(e); err != nil {
			t.Fatal(err)
		}
	}
	got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+old.ID, ""))
	if got.Status != schemas.EvaluationFailed || got.Problem != "The server restarted while this evaluation was starting. Start it again." {
		t.Fatalf("lost start: %q %q", got.Status, got.Problem)
	}
	if saved, _ := evalstore.Open(dir).Load(old.ID); saved.Status != schemas.EvaluationFailed {
		t.Fatalf("not saved: %+v", saved)
	}
	if got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+young.ID, "")); got.Status != schemas.EvaluationQueued || got.Problem != "" {
		t.Fatalf("young start: %q %q", got.Status, got.Problem)
	}
}

func TestCreateEvaluationRefusesIncompatibleSetup(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		if s.ID == "longmemeval" {
			return false, nil, "Agent x cannot run benchmark longmemeval: it grades a file the agent writes, and the agent only answers in text."
		}
		return true, nil, ""
	}
	defer func() { evalCompat = old }()
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"mode":"opponent","arms":[{"label":"A","agent":"scripted","model":"m1"},{"label":"B","agent":"other","model":"m2"}],"benchmarks":["hd-harbor-tb2","longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	for _, want := range []string{
		"Setup A (scripted) cannot run longmemeval: it grades a file the agent writes, and the agent only answers in text. Remove the benchmark or pick another baseline.",
		"Setup B (other) cannot run longmemeval: it grades a file the agent writes, and the agent only answers in text. Remove the benchmark or pick another baseline.",
	} {
		if !strings.Contains(string(ctx.Response.Body()), want) {
			t.Fatalf("body %s, want %q", ctx.Response.Body(), want)
		}
	}
	if strings.Contains(string(ctx.Response.Body()), "hd-harbor-tb2") {
		t.Fatalf("a compatible benchmark was listed: %s", ctx.Response.Body())
	}
	if evs, _ := evalstore.Open(dir).List(); len(evs) != 0 {
		t.Fatalf("saved %d evaluations", len(evs))
	}
	if len(l.planned) != 0 {
		t.Fatalf("planned %v", l.planned)
	}
}

func TestCreateCallsPlatformRefusal(t *testing.T) {
	old := platformRefusal
	var saw string
	platformRefusal = func(_ context.Context, _ *evaluationsHandler, spec *schemas.EvaluationSpec) (int, any, bool) {
		saw = strings.Join(spec.Benchmarks, ",")
		return fasthttp.StatusConflict, map[string]string{"why": "no image registry"}, true
	}
	defer func() { platformRefusal = old }()
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", `{`+oneArm+`,"benchmarks":["longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(ctx.Response.Body()), "no image registry") {
		t.Fatalf("got %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if saw != "longmemeval" {
		t.Fatalf("hook saw %q, want the normalized spec", saw)
	}
	if evs, _ := evalstore.Open(dir).List(); len(evs) != 0 || len(l.planned) != 0 {
		t.Fatalf("saved %d evaluations, planned %v", len(evs), l.planned)
	}
}

func TestEvaluationFailedRunsCount(t *testing.T) {
	run := func(slot string, counted bool, o schemas.Outcome) RunView {
		return RunView{Slot: schemas.Slot{SlotID: slot, Counted: counted}, Run: schemas.Run{Outcome: o, Attempt: 1}, Sealed: true}
	}
	runs := []RunView{
		run("s1", true, schemas.OutcomePass),
		run("s2", true, schemas.Outcome("fail")),
		run("s3", false, schemas.OutcomeInfraError),
		run("s4", false, schemas.Outcome("fail")),                                            // not counted, not infra: left out
		{Slot: schemas.Slot{SlotID: "s5", Counted: true}, Run: schemas.Run{Outcome: "fail"}}, // not sealed yet
	}
	if n := failedRunCount(runs); n != 2 {
		t.Fatalf("failed runs %d, want 2", n)
	}
	if n := failedRunCount(runs[:1]); n != 0 {
		t.Fatalf("all passed: %d", n)
	}
}

func TestCreateEvaluationBadArmLabelPlainText(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites(), OrgID: "org1"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"arms":[{"label":"current","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if body := string(ctx.Response.Body()); strings.Contains(body, "quickrun") || !strings.Contains(body, "current") {
		t.Fatalf("body %s", body)
	}
}
