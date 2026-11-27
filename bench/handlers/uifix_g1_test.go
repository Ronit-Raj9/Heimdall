package handlers

import (
	"encoding/json"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/valyala/fasthttp"
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
