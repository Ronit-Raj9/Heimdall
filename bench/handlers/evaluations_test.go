package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdictab"
	"github.com/valyala/fasthttp"
)

// evalLauncher is a scripted launcher: one experiment id per planned
// experiment name, nothing ever runs. It also cancels.
type evalLauncher struct {
	mu        sync.Mutex
	planned   map[string]*schemas.Experiment // exp id -> experiment
	created   []string
	started   []string
	cancelled []string
}

func (l *evalLauncher) Plan(_ context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.Contains(exp.Suite, "refused") {
		return nil, fmt.Errorf("suite refused")
	}
	if l.planned == nil {
		l.planned = map[string]*schemas.Experiment{}
	}
	id := "e_" + strings.ReplaceAll(exp.Name, "-", "_")
	l.planned[id] = exp
	m := testManifest()
	m.ExpID, m.Experiment = id, *exp
	return m, nil
}

func (l *evalLauncher) Create(_ context.Context, m *schemas.Manifest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.created = append(l.created, m.ExpID)
	return nil
}

func (l *evalLauncher) Start(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, id)
	return nil
}

func (l *evalLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	ch := make(chan schemas.LiveEvent)
	close(ch)
	return ch, func() {}
}

func (l *evalLauncher) Cancel(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelled = append(l.cancelled, id)
	return nil
}
func (l *evalLauncher) Resume(context.Context, string) error { return nil }
func (l *evalLauncher) Cancelled(string) bool                { return false }

func evalServer(t *testing.T, s Services) (*router.Router, string) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"agents/scripted.yaml":  "id: scripted\nadapter: scripted\n",
		"agents/other.yaml":     "id: other\nadapter: scripted\n",
		"policies/default.yaml": "name: default\n",
	})
	ps, err := projectfs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Projects == nil {
		s.Projects = ps
	}
	r := router.New()
	env := OrgEnv{Cfg: &lib.Config{Root: dir}, Mode: schemas.ModeRecorded, Services: s}
	for _, a := range OrgAreas() {
		a(env, r)
	}
	return r, dir
}

func evalCall(r *router.Router, method, uri, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	return ctx
}

func evalSuites() *fakeSuites {
	tasks := []string{"t01", "t02", "t03", "t04", "t05", "t06", "t07", "t08", "t09", "t10"}
	return &fakeSuites{
		ids: []string{"hd-harbor-tb2", "hd-swe-verified", "longmemeval", "hd-swe-ib-lcb"},
		suites: map[string]*schemas.Suite{
			"hd-harbor-tb2": {ID: "hd-harbor-tb2", Version: "1.0.0", TaskIDs: tasks, ProbeIDs: []string{"stale-notes"},
				Probes: []*schemas.ProbeSpec{{ID: "stale-notes", PS2Family: schemas.FamilyMisleadingInstruction, Layer: schemas.LayerFixture}}},
			"hd-swe-verified": {ID: "hd-swe-verified", Version: "2.0.0", TaskIDs: tasks[:3]},
			"hd-swe-ib-lcb":   {ID: "hd-swe-ib-lcb", Version: "1.0.0"},
			"longmemeval":     {ID: "longmemeval", Version: "1.0.0", TaskIDs: tasks},
		},
	}
}

func decodeEval(t *testing.T, ctx *fasthttp.RequestCtx) *schemas.Evaluation {
	t.Helper()
	var ev schemas.Evaluation
	if err := json.Unmarshal(ctx.Response.Body(), &ev); err != nil {
		t.Fatalf("decode %s: %v", ctx.Response.Body(), err)
	}
	return &ev
}

func TestEvaluationCreatePackChildren(t *testing.T) {
	old := evalCompat
	evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
		if s != nil && s.ID == "hd-swe-verified" {
			return false, []schemas.Capability{"gpu"}, ""
		}
		return true, nil, ""
	}
	defer func() { evalCompat = old }()

	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites(), OrgID: "org1"})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"name":"my eval","mode":"score","arms":[{"label":"A","agent":"scripted","model":"gpt-5.4-mini","provider":"openai"}],"pack":"coding","tasks_per_benchmark":4,"trials":1,"faults":["misleading-instruction"],"budget_usd":9}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if !evalstore.ValidID(ev.ID) || ev.Org != "org1" || ev.Status != schemas.EvaluationRunning {
		t.Fatalf("evaluation %+v", ev)
	}
	// coding pack on this server: hd-harbor-tb2, hd-swe-ib-lcb, hd-swe-verified (sorted).
	if len(ev.Children) != 3 {
		t.Fatalf("children %+v", ev.Children)
	}
	byBench := map[string]schemas.EvaluationChild{}
	for _, c := range ev.Children {
		byBench[c.Benchmark] = c
	}
	tb := byBench["hd-harbor-tb2"]
	if tb.Status != "running" || tb.ExperimentID == "" {
		t.Fatalf("tb2 child %+v", tb)
	}
	if c := byBench["hd-swe-verified"]; c.Status != schemas.EvaluationChildSkipped || !strings.Contains(c.Skipped, "needs gpu") {
		t.Fatalf("incompatible child %+v", c)
	}
	if c := byBench["hd-swe-ib-lcb"]; c.Status != schemas.EvaluationChildSkipped || !strings.Contains(c.Skipped, "no admitted task") {
		t.Fatalf("empty child %+v", c)
	}

	exp := l.planned[tb.ExperimentID]
	if exp.EvaluationID != ev.ID || exp.Suite != "hd-harbor-tb2@1.0.0" || exp.Base != "agents/scripted.yaml" {
		t.Fatalf("experiment %+v", exp)
	}
	if len(exp.Arms) != 2 || exp.Arms["A"].Set["model"] != "gpt-5.4-mini" || exp.Arms["B"].Set["env.HEIMDALL_QUICKRUN_ARM"] != "B" {
		t.Fatalf("arms %+v", exp.Arms)
	}
	if len(exp.Conditions) != 2 || exp.Conditions[0].ID != "clean" || exp.Conditions[1].ID != "stale-notes" {
		t.Fatalf("conditions %+v", exp.Conditions)
	}
	if got := strings.Join(exp.Conditions[0].Tasks.IDs, ","); got != "t01,t03,t06,t08" {
		t.Fatalf("tasks %s", got)
	}
	if exp.Budget.TotalUSD != 3 {
		t.Fatalf("budget %+v", exp.Budget)
	}
	if len(l.started) != 1 || l.started[0] != tb.ExperimentID {
		t.Fatalf("started %v", l.started)
	}
	ps, _ := projectfs.Open(dir)
	data, _, err := ps.Read(t.Context(), projectfs.AreaExperiments, exp.Name+".yaml")
	if err != nil || !strings.Contains(string(data), "evaluation_id: "+ev.ID) {
		t.Fatalf("saved file %v:\n%s", err, data)
	}
	if saved, err := evalstore.Open(dir).Load(ev.ID); err != nil || len(saved.Children) != 3 {
		t.Fatalf("stored evaluation %v %+v", err, saved)
	}
}

// The default task count must let a per-benchmark verdict reach
// significance at all: the sign-flip test needs verdictab.MinTasks paired tasks.
func TestEvaluationDefaultTasksCanBeSignificant(t *testing.T) {
	if EvaluationDefaultTasks < verdictab.MinTasks || EvalDefaultTasks < verdictab.MinTasks {
		t.Fatalf("default tasks %d / estimate %d below verdictab.MinTasks %d", EvaluationDefaultTasks, EvalDefaultTasks, verdictab.MinTasks)
	}
}

func TestEvaluationABArmsAndVersions(t *testing.T) {
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"mode":"opponent","arms":[{"label":"A","agent":"scripted","version":2,"model":"m1"},{"label":"B","agent":"agents/other.yaml","model":"m2","provider":"openrouter"}],"benchmarks":["longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if ev.Spec.TasksPerBenchmark != EvaluationDefaultTasks || ev.Spec.Trials != 2 || strings.Join(ev.Spec.Faults, ",") != "clean" {
		t.Fatalf("defaults %+v", ev.Spec)
	}
	exp := l.planned[ev.Children[0].ExperimentID]
	if exp.Arms["A"].Agent != "agents/scripted.yaml@v2" || exp.Arms["B"].Agent != "agents/other.yaml" || exp.Arms["B"].Set["gateway.provider"] != "openrouter" {
		t.Fatalf("arms %+v", exp.Arms)
	}
	if exp.Conditions[0].Trials != 2 || len(exp.Conditions[0].Tasks.IDs) != EvaluationDefaultTasks {
		t.Fatalf("conditions %+v", exp.Conditions)
	}
}

func TestEvaluationLiveStatusListCancel(t *testing.T) {
	l := &evalLauncher{}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{}}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites(), Index: idx})
	body := `{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval","hd-harbor-tb2"],"trials":1,"tasks_per_benchmark":1}`
	first := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", body))
	time.Sleep(1100 * time.Millisecond) // a newer CreatedAt and id
	second := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations", body))

	// List: newest first.
	var lst EvaluationList
	ctx := evalCall(r, "GET", APIPrefix+"/evaluations", "")
	if err := json.Unmarshal(ctx.Response.Body(), &lst); err != nil || len(lst.Evaluations) != 2 || lst.Evaluations[0].ID != second.ID {
		t.Fatalf("list %v %s", err, ctx.Response.Body())
	}

	// Live status: both children of first finish (a manifest with no slots
	// counts as not started; give each one sealed slot).
	for _, c := range first.Children {
		m := testManifest()
		m.ExpID = c.ExperimentID
		m.Slots = []schemas.Slot{{SlotID: c.ExperimentID + "_s1", ExpID: c.ExperimentID}}
		idx.manifests[c.ExperimentID] = m
		now := time.Now()
		idx.runs = append(idx.runs, RunView{Slot: m.Slots[0], Sealed: true,
			Run: schemas.Run{RunID: c.ExperimentID + "_r1", Outcome: schemas.OutcomePass, Timing: schemas.RunTiming{Sealed: &now}}})
	}
	got := decodeEval(t, evalCall(r, "GET", APIPrefix+"/evaluations/"+first.ID, ""))
	if got.Status != schemas.EvaluationDone || got.Children[0].Status != "done" {
		t.Fatalf("live status %+v", got)
	}

	// Cancel the second one.
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+second.ID+"/cancel", "")
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("cancel %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if c := decodeEval(t, ctx); c.Status != schemas.EvaluationCancelled || len(l.cancelled) != 2 {
		t.Fatalf("cancelled %+v %v", c, l.cancelled)
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations/"+second.ID+"/cancel", ""); got.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("second cancel %d", got.Response.StatusCode())
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations/"+first.ID+"/cancel", ""); got.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("cancel done %d", got.Response.StatusCode())
	}
}

func TestEvaluationErrors(t *testing.T) {
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	cases := map[string]int{
		`{}`: fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}]}`:                                                fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"missing","model":"m"}],"benchmarks":["longmemeval"]}`:                    fasthttp.StatusNotFound,
		`{"mode":"versions","arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"]}`: fasthttp.StatusBadRequest,
		`{"mode":"bogus","arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"]}`:    fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"../x","model":"m"}],"benchmarks":["longmemeval"]}`:                       fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}],"pack":"voice"}`:                                 fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"trials":99}`:       fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"bogus":1}`:         fasthttp.StatusBadRequest,
	}
	for body, want := range cases {
		if got := evalCall(r, "POST", APIPrefix+"/evaluations", body).Response.StatusCode(); got != want {
			t.Errorf("%s: status %d, want %d", body, got, want)
		}
	}
	if got := evalCall(r, "GET", APIPrefix+"/evaluations/ev_nope", "").Response.StatusCode(); got != fasthttp.StatusNotFound {
		t.Errorf("missing: %d", got)
	}
	if got := evalCall(r, "GET", APIPrefix+"/evaluations/..", "").Response.StatusCode(); got == fasthttp.StatusOK {
		t.Errorf("bad id answered 200")
	}
	r2, _ := evalServer(t, Services{})
	if got := evalCall(r2, "POST", APIPrefix+"/evaluations", `{}`).Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Errorf("no launcher: %d", got)
	}
}

func TestEvaluationOrgScoped(t *testing.T) {
	r, dir := evalServer(t, Services{Launcher: &evalLauncher{}, OrgID: "org1"})
	ev := &schemas.Evaluation{ID: "ev_other", Org: "org2", Status: schemas.EvaluationRunning, CreatedAt: time.Now()}
	if err := evalstore.Open(dir).Save(ev); err != nil {
		t.Fatal(err)
	}
	if got := evalCall(r, "GET", APIPrefix+"/evaluations/ev_other", "").Response.StatusCode(); got != fasthttp.StatusNotFound {
		t.Fatalf("other org's evaluation: %d", got)
	}
	var lst EvaluationList
	_ = json.Unmarshal(evalCall(r, "GET", APIPrefix+"/evaluations", "").Response.Body(), &lst)
	if len(lst.Evaluations) != 0 {
		t.Fatalf("list leaked %+v", lst.Evaluations)
	}
}

func TestSpreadPick(t *testing.T) {
	ids := []string{"a", "b", "c", "d", "e", "f"}
	if got := strings.Join(spreadPick(ids, 3), ","); got != "a,c,e" {
		t.Fatal(got)
	}
	if got := len(spreadPick(ids, 10)); got != 6 {
		t.Fatal(got)
	}
}
