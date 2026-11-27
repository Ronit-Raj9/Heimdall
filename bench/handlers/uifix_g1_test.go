package handlers

import (
	"encoding/json"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/forecast"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/valyala/fasthttp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRunListRowTaskTitle(t *testing.T) {
	s := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{{ID: "t1", Title: " Fix the parser "}, {ID: "t2"}}}
	suites := map[string]suiteFacts{"s": suiteFactsOf(s)}
	row := runListRow(RunView{Slot: schemas.Slot{Suite: "s", Task: "t1"}}, suites)
	if row.TaskTitle != "Fix the parser" {
		t.Fatalf("title %q", row.TaskTitle)
	}
	if row := runListRow(RunView{Slot: schemas.Slot{Suite: "s", Task: "t2"}}, suites); row.TaskTitle != "" {
		t.Fatalf("untitled task %q", row.TaskTitle)
	}
	if row := runListRow(RunView{Slot: schemas.Slot{Suite: "gone", Task: "t1"}}, suites); row.TaskTitle != "" {
		t.Fatalf("unknown suite %q", row.TaskTitle)
	}
}

func TestRunDetailFailureGroup(t *testing.T) {
	d := fullDeps()
	d.index = newClusterIndex()
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil))
	if body["failure"] != nil {
		t.Fatalf("failure without ?failure=1: %v", body["failure"])
	}
	body = decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A?failure=1", nil, nil))
	f, _ := body["failure"].(map[string]any)
	if f == nil || f["signature"] == "" || f["count"] != float64(1) || f["exp_id"] != "e_test" {
		t.Fatalf("failure = %v", body["failure"])
	}
	if txt, _ := f["text"].(string); !strings.Contains(txt, "bad window") {
		t.Fatalf("text = %v", f["text"])
	}
	// An index that cannot group runs sends no failure, never an error.
	d.index = testIndex()
	c = newTestClient(t, d)
	if body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A?failure=1", nil, nil)); body["failure"] != nil {
		t.Fatalf("plain index: %v", body["failure"])
	}
}

func TestRunListExpName(t *testing.T) {
	idx := testIndex()
	m, _ := idx.Manifest("e_test")
	m.Experiment.Name = " Limiter fix "
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/runs?exp=e_test", nil, nil))
	runs, _ := body["runs"].([]any)
	if len(runs) == 0 {
		t.Fatalf("no runs: %v", body)
	}
	for _, r := range runs {
		if row := r.(map[string]any); row["exp_name"] != "Limiter fix" {
			t.Fatalf("exp_name = %v", row["exp_name"])
		}
	}
}

func TestSuiteTitleFromCatalog(t *testing.T) {
	if suiteTitle("") != "" || suiteTitle("no-such-suite") != "" {
		t.Fatal("unknown suite must have no title")
	}
	found := false
	for id, title := range catalogSuiteTitles() {
		if id == "" || title == "" || title == id {
			t.Fatalf("bad entry %q %q", id, title)
		}
		found = found || suiteTitle(id) == title
	}
	if !found {
		t.Fatal("catalog gave no suite title")
	}
}

func TestReleasePreviewProblemCode(t *testing.T) {
	r, root, _, ev := evalReleaseFixture(t, nil)
	ev.Spec.Arms[1].Version = 1
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	preview := func() EvaluationReleasePreview {
		ctx := evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID+"/release-preview", "")
		var p EvaluationReleasePreview
		if err := json.Unmarshal(ctx.Response.Body(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := preview(); p.ProblemCode != ReleaseProblemOnlyOneVersion {
		t.Fatalf("live version code %+v", p)
	}
	ev.Status = schemas.EvaluationRunning
	ev.Children[0].Status = string(schemas.ExperimentRunning)
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	if p := preview(); p.ProblemCode != ReleaseProblemNotFinished {
		t.Fatalf("running code %q", p.ProblemCode)
	}
}

func TestShareViewNamesCreator(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada"})
	h := &shareHandler{now: time.Now}
	st := sharelinks.Open(t.TempDir())
	sh := sharelinks.Share{ID: "sh_1", CreatedBy: "usr_1"}
	if v := h.viewNamed(asCaller(actorMember), st, sh, callerNames(asCaller(actorMember))); v.CreatedByName != "Ada" || v.CreatedBy != "usr_1" {
		t.Fatalf("member view %+v", v)
	}
	if v := h.view(&fasthttp.RequestCtx{}, st, sh); v.CreatedByName != "" {
		t.Fatalf("local view names %q", v.CreatedByName)
	}
}

// The list of every link (GET /shares without kind or target) reads only
// the caller's organization's links.
func TestShareListIsPerOrganization(t *testing.T) {
	ra, _ := shareRouter(t)
	rb, _ := shareRouter(t)
	if res := shareCall(ra, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"}); res.StatusCode() != 201 {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	var a, b ShareList
	_ = json.Unmarshal(shareCall(ra, "GET", APIPrefix+"/shares", nil).Body(), &a)
	_ = json.Unmarshal(shareCall(rb, "GET", APIPrefix+"/shares", nil).Body(), &b)
	if len(a.Shares) != 1 || len(b.Shares) != 0 {
		t.Fatalf("org a %d links, org b %d", len(a.Shares), len(b.Shares))
	}
}

func TestRetentionPreview(t *testing.T) {
	idx := testIndex()
	now := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	p := retentionPreview(idx, RetentionMinDays, now)
	if p.Days != RetentionMinDays || p.Experiments != 1 || p.Runs != len(idx.Runs(testExp)) {
		t.Fatalf("preview %+v", p)
	}
	if p := retentionPreview(idx, RetentionMaxDays, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)); p.Experiments != 0 {
		t.Fatalf("nothing is that old: %+v", p)
	}
	h := &dataOpsHandler{env: OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Index: idx}}}
	call := func(ctx *fasthttp.RequestCtx, q string) *fasthttp.RequestCtx {
		ctx.Request.SetRequestURI(APIPrefix + "/data/retention" + q)
		h.getRetention(ctx)
		return ctx
	}
	if c := call(asCaller(actorMember), "?preview_days=30"); c.Response.StatusCode() != 403 {
		t.Fatalf("member preview %d", c.Response.StatusCode())
	}
	if c := call(asCaller(actorAdmin), "?preview_days=0"); c.Response.StatusCode() != 400 {
		t.Fatalf("too short %d", c.Response.StatusCode())
	}
	c := call(asCaller(actorAdmin), "?preview_days=30")
	if c.Response.StatusCode() != 200 || !strings.Contains(string(c.Response.Body()), `"preview":{"days":30`) {
		t.Fatalf("admin preview %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if c := call(asCaller(actorAdmin), ""); strings.Contains(string(c.Response.Body()), "preview") {
		t.Fatalf("no preview without the parameter: %s", c.Response.Body())
	}
	// Another organization's server env has its own index: nothing of org a.
	other := &dataOpsHandler{env: OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Index: &fakeIndex{}}}}
	ctx := asCaller(actorAdmin)
	ctx.Request.SetRequestURI(APIPrefix + "/data/retention?preview_days=30")
	other.getRetention(ctx)
	if !strings.Contains(string(ctx.Response.Body()), `"experiments":0`) {
		t.Fatalf("other org %s", ctx.Response.Body())
	}
}

func TestCatalogInternalFlag(t *testing.T) {
	if v := catalogEntryView(benchcatalog.Entry{ID: "suite-heimdall-improver", SuiteID: "heimdall-improver"}); !v.Internal {
		t.Fatal("improver suite must be internal")
	}
	if v := catalogEntryView(benchcatalog.Entry{ID: "example-x", Suites: []string{"a", "heimdall-improver"}}); !v.Internal {
		t.Fatal("an example adding the improver suite is internal")
	}
	v := catalogEntryView(benchcatalog.Entry{ID: "suite-coding-core", SuiteID: "coding-core"})
	if b := answerOf(t, v); v.Internal || strings.Contains(b, "internal") || !strings.Contains(b, `"id":"suite-coding-core"`) {
		t.Fatalf("plain entry %s", b)
	}
}

func TestSuiteHealthTaskFilter(t *testing.T) {
	c := newHealthClient(t, healthSuites(t), healthRecord{}, false)
	resp := c.do(t, "GET", APIPrefix+"/suites/hs/health?tasks=pub,none", nil, nil)
	var h schemas.SuiteHealth
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &h) != nil || len(h.Tasks) != 1 || h.Tasks[0].Task != "pub" {
		t.Fatalf("filtered %d %s", resp.StatusCode(), resp.Body())
	}
}

func TestEvaluationChildCause(t *testing.T) {
	ev := &schemas.Evaluation{Children: []schemas.EvaluationChild{
		{Benchmark: "a", Skipped: "plan e_x: arm A: policy refused: base image ghcr.io/x/base:1 is not in the shared worker's image registry"},
		{Benchmark: "b"},
	}}
	fillChildCauses(ev)
	if ev.Children[0].Cause != schemas.CauseImageMissing || ev.Children[1].Cause != "" {
		t.Fatalf("causes %+v", ev.Children)
	}
}

func TestFindingAddRecordsOrigin(t *testing.T) {
	r, root, _, _ := w443Setup(t, w443NewDoc)
	rep := `{"id":"job1","exp_id":"e_test","items":[{"id":"i0","run_id":"r_t01_A","title":"t","hypothesis":"h","kind":"agent_mistake","origin":"model"}],
	"groups":[{"title":"Skips the tests","kind":"agent_mistake","items":["i0"],"runs":1,"tasks":["coding-core/t01"]}]}`
	if err := os.MkdirAll(filepath.Join(root, "investigations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "investigations", "job1.json"), []byte(rep), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"investigation","ref":"job1:0"}`))
	if code != fasthttp.StatusCreated || body["experiment"] != "e_test" {
		t.Fatalf("finding %d %v", code, body)
	}
}

func TestApplyForecastListPrice(t *testing.T) {
	var be EvalBenchmarkEstimate
	f := &schemas.Forecast{Arms: []schemas.ArmForecast{{Arm: "A", Basis: forecast.BasisAgentTokens, BasisText: "rough: from tokens"}}}
	applyForecast(&be, f, []string{"A"}, 0)
	if be.EstimateBasis != "list_price" || !slices.Contains(be.Notes, "arm A: rough: from tokens") {
		t.Fatalf("estimate %+v", be)
	}
	be = EvalBenchmarkEstimate{}
	applyForecast(&be, &schemas.Forecast{Arms: []schemas.ArmForecast{{Arm: "A", Basis: schemas.BasisLibrary}}}, []string{"A"}, 0)
	if be.EstimateBasis != "" {
		t.Fatalf("library basis %+v", be)
	}
}
