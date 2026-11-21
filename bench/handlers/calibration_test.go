package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeCalibration serves one judged suite "qa" from a temp folder with the
// real calibration package: labels, one record, the status.
type fakeCalibration struct {
	suite *schemas.Suite
}

func newFakeCalibration(t *testing.T) *fakeCalibration {
	t.Helper()
	root := t.TempDir()
	s := &schemas.Suite{ID: "qa", Root: root, Grading: schemas.GradingJudged, Judge: &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: "qa-v1"}}
	f := &fakeCalibration{suite: s}
	if _, err := f.AddCalibrationLabel(context.Background(), "qa", &schemas.CalibrationLabelRequest{Task: "q1", Answer: "Max", Label: "correct", Labeller: "ana"}); err != nil {
		t.Fatal(err)
	}
	tmpls := map[string]string{"qa-v1-answer": "aaa"}
	rec := &schemas.CalibrationRecord{Suite: "qa", Provider: "openai", Model: "judge-model", PromptVersion: "qa-v1", Templates: tmpls, PromptSHA256: calibration.PromptHash(tmpls),
		Votes: 1, CreatedAt: time.Now().UTC(), USD: schemas.Observed(0.01, schemas.EvidenceGateway)}
	var runs []calibration.ItemRun
	for k := 0; k < 40; k++ {
		lab, v := schemas.CalibrationCorrect, schemas.CalibrationVerdictCorrect
		if k%2 == 1 {
			lab, v = schemas.CalibrationIncorrect, schemas.CalibrationVerdictIncorrect
		}
		ans := fmt.Sprintf("answer %d", k)
		it := schemas.CalibrationItem{ID: calibration.ItemID("q1", ans), Task: "q1", Answer: ans, Label: lab, Labeller: "ana", Source: schemas.CalibrationSourceLabel}
		runs = append(runs, calibration.ItemRun{Item: calibration.Labelled{Item: it, Label: lab, Labellers: []string{"ana"}}, Category: "single-hop",
			Votes: []string{v}, Repeat: []string{v}, Perturbed: map[string][]string{calibration.PerturbPreamble: {v}}})
	}
	calibration.Build(rec, runs, 0)
	if err := calibration.WriteRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakeCalibration) CalibrationStatus(_ context.Context, id string) (*schemas.CalibrationStatus, error) {
	if id != "qa" {
		return nil, fmt.Errorf("suite %s: %w", id, schemas.ErrNotFound)
	}
	return calibration.Status(f.suite, &calibration.StatusJudge{ID: calibration.JudgeID{Provider: f.suite.Judge.Provider, Model: f.suite.Judge.Model, Votes: 1},
		PromptVersion: f.suite.Judge.PromptVersion, Templates: map[string]string{"qa-v1-answer": "aaa"}}, calibration.Thresholds(nil, ""), time.Now().UTC())
}

func (f *fakeCalibration) CalibrationLabels(_ context.Context, id string) (*schemas.CalibrationLabels, error) {
	set, err := calibration.LoadSet(f.suite.Root)
	if err != nil {
		return nil, err
	}
	return &schemas.CalibrationLabels{Suite: id, SetSHA256: set.SHA256, Items: set.Items}, nil
}

func (f *fakeCalibration) AddCalibrationLabel(_ context.Context, _ string, req *schemas.CalibrationLabelRequest) (*schemas.CalibrationItem, error) {
	it, err := calibration.NewItem(*req, schemas.CalibrationSourceLabel, time.Now())
	if err != nil {
		return nil, err
	}
	if _, err := calibration.Append(f.suite.Root, []schemas.CalibrationItem{it}); err != nil {
		return nil, err
	}
	return &it, nil
}

func newCalibrationClient(t *testing.T, mode schemas.Mode, src CalibrationSource) *testClient {
	t.Helper()
	r := router.New()
	NewCalibrationHandler(mode, src, lib.RequireWriteToken(mode, testToken)).RegisterRoutes(r, lib.BodyLimit(mode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestCalibrationContract(t *testing.T) {
	c := newCalibrationClient(t, schemas.ModeLive, newFakeCalibration(t))
	st := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/qa/calibration", nil, nil))
	checkKeys(t, "CalibrationStatus", st, loadKeys(t, "CalibrationStatus"), true)
	if st["state"] != schemas.CalibrationStateCalibrated || st["records"].(float64) != 1 {
		t.Fatalf("status %v", st)
	}
	for path, typ := range map[string]string{"latest": "CalibrationRecord", "latest.human": "CalibrationStats", "latest.human.agreement": "CalibrationRate",
		"latest.human.fpr": "CalibrationRate", "thresholds": "JudgeCalibrationThresholds"} {
		for i, o := range objectsAt(t, st, path) {
			checkKeys(t, fmt.Sprintf("CalibrationStatus.%s #%d", path, i), o, loadKeys(t, typ), false)
		}
	}
	l := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/qa/calibration/labels", nil, nil))
	checkKeys(t, "CalibrationLabels", l, loadKeys(t, "CalibrationLabels"), true)
	for i, o := range objectsAt(t, l, "items[]") {
		checkKeys(t, fmt.Sprintf("CalibrationLabels.items #%d", i), o, loadKeys(t, "CalibrationItem"), false)
	}
	resp := c.do(t, "POST", APIPrefix+"/suites/qa/calibration/labels", []byte(`{"task":"q1","answer":"Rex","label":"incorrect","labeller":"bo","note":"wrong dog"}`), authed("application/json"))
	if resp.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("post label %d %s", resp.StatusCode(), resp.Body())
	}
	it := decodeBody(t, resp)
	checkKeys(t, "CalibrationItem", it, loadKeys(t, "CalibrationItem"), true)
	if it["source"] != schemas.CalibrationSourceLabel || it["id"] != calibration.ItemID("q1", "Rex") {
		t.Fatalf("item %v", it)
	}
}

func TestCalibrationRoutesRefuse(t *testing.T) {
	live := newCalibrationClient(t, schemas.ModeLive, newFakeCalibration(t))
	for _, tt := range []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
	}{
		{"unknown suite", "GET", "/suites/nope/calibration", "", nil, fasthttp.StatusNotFound},
		{"bad id", "GET", "/suites/a%20b/calibration", "", nil, fasthttp.StatusBadRequest},
		{"no token", "POST", "/suites/qa/calibration/labels", `{"task":"q1","answer":"x","label":"correct","labeller":"a"}`, map[string]string{"Content-Type": "application/json"}, fasthttp.StatusUnauthorized},
		{"bad label", "POST", "/suites/qa/calibration/labels", `{"task":"q1","answer":"x","label":"maybe","labeller":"a"}`, authed("application/json"), fasthttp.StatusBadRequest},
		{"no labeller", "POST", "/suites/qa/calibration/labels", `{"task":"q1","answer":"x","label":"correct"}`, authed("application/json"), fasthttp.StatusBadRequest},
		{"unknown field", "POST", "/suites/qa/calibration/labels", `{"task":"q1","answer":"x","label":"correct","labeller":"a","x":1}`, authed("application/json"), fasthttp.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			if tt.body != "" {
				body = []byte(tt.body)
			}
			if got := live.do(t, tt.method, APIPrefix+tt.path, body, tt.headers).StatusCode(); got != tt.status {
				t.Fatalf("status %d, want %d", got, tt.status)
			}
		})
	}
	rec := newCalibrationClient(t, schemas.ModeRecorded, newFakeCalibration(t))
	if got := rec.do(t, "POST", APIPrefix+"/suites/qa/calibration/labels", []byte(`{"task":"q1","answer":"x","label":"correct","labeller":"a"}`), authed("application/json")).StatusCode(); got != fasthttp.StatusForbidden {
		t.Fatalf("recorded mode post = %d, want 403", got)
	}
	none := newCalibrationClient(t, schemas.ModeLive, nil)
	if got := none.do(t, "GET", APIPrefix+"/suites/qa/calibration", nil, nil).StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no source = %d, want 503", got)
	}
}

// TestCalibrationLabelsHideHeldOut is audit qc2-17-01: unless held-out tasks
// are visible, the labels route leaves out labels of held-out tasks, whose
// rows carry answers marked correct or wrong.
func TestCalibrationLabelsHideHeldOut(t *testing.T) {
	src := newFakeCalibration(t)
	if _, err := src.AddCalibrationLabel(context.Background(), "qa", &schemas.CalibrationLabelRequest{Task: "secret", Answer: "42", Label: "correct", Labeller: "ana"}); err != nil {
		t.Fatal(err)
	}
	suite := *src.suite
	suite.Tasks = []*schemas.Task{{ID: "q1", Split: schemas.SplitPublic}, {ID: "secret", Split: schemas.SplitHeldOut}}
	suites := &fakeSuites{ids: []string{"qa"}, suites: map[string]*schemas.Suite{"qa": &suite}}
	get := func(visible bool) []string {
		r := router.New()
		NewCalibrationHandler(schemas.ModeLive, src, lib.RequireWriteToken(schemas.ModeLive, testToken)).WithHeldOut(suites, visible).RegisterRoutes(r)
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI(APIPrefix + "/suites/qa/calibration/labels")
		r.Handler(ctx)
		var l schemas.CalibrationLabels
		if err := json.Unmarshal(ctx.Response.Body(), &l); err != nil {
			t.Fatalf("%d %s: %v", ctx.Response.StatusCode(), ctx.Response.Body(), err)
		}
		var tasks []string
		for _, it := range l.Items {
			tasks = append(tasks, it.Task)
		}
		return tasks
	}
	for _, task := range get(false) {
		if task == "secret" {
			t.Fatal("held-out task's label served")
		}
	}
	if got := get(true); !slices.Contains(got, "secret") {
		t.Fatalf("held_out.visible labels = %v", got)
	}
}
