package handlers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// captureReleases records release requests; it starts with codex v1 live.
type captureReleases struct {
	mu   sync.Mutex
	recs []schemas.ReleaseRecord
	last *schemas.ReleaseRequest
}

func (c *captureReleases) Create(_ context.Context, req *schemas.ReleaseRequest) (*schemas.ReleaseRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = req
	e := req.Evaluation
	rec := schemas.ReleaseRecord{ReleaseID: "rel_2", EvaluationID: req.EvaluationID, Approver: req.Approver, Override: req.Override, CreatedAt: time.Now()}
	if e != nil {
		rec.AgentID, rec.Version, rec.ReplacesVersion, rec.Summary = e.AgentID, e.Version, e.ReplacesVersion, e.Summary
	}
	c.recs = append(c.recs, rec)
	return &rec, nil
}

func (c *captureReleases) List(context.Context) (*schemas.ReleaseList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &schemas.ReleaseList{Releases: append([]schemas.ReleaseRecord(nil), c.recs...), ChainOK: true}, nil
}
func (c *captureReleases) Get(context.Context, string) (*schemas.ReleaseRecord, error) {
	return nil, schemas.ErrNotFound
}
func (c *captureReleases) Evidence(context.Context, string) ([]byte, error) {
	return nil, schemas.ErrNotFound
}

func evalReleaseFixture(t *testing.T, gate *schemas.EvaluationGate) (*router.Router, string, *captureReleases, *schemas.Evaluation) {
	t.Helper()
	evalRefreshChildren = false
	t.Cleanup(func() { evalRefreshChildren = true })
	root := t.TempDir()
	rl := &captureReleases{recs: []schemas.ReleaseRecord{{ReleaseID: "rel_1", AgentID: "codex", Version: 1, Approver: "ana", CreatedAt: time.Now().Add(-time.Hour)}}}
	ev := &schemas.Evaluation{ID: "ev_20261120_000000_abc", Status: schemas.EvaluationDone, CreatedAt: time.Now(),
		Spec: schemas.EvaluationSpec{Name: "v2 vs v1", Mode: schemas.EvaluationModeVersions, Benchmarks: []string{"coding-core"}, Gate: gate,
			Arms: []schemas.EvaluationArm{
				{Label: "A", Agent: "codex", Version: 1, Model: "gpt-5.4-mini"},
				{Label: "B", Agent: "codex", Version: 2, Model: "gpt-5.4-mini"},
			}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: string(schemas.ExperimentDone)}},
	}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Releases: rl}}
	env.Services.Index = leaderboardIndex()
	r := router.New()
	h := &evalReleaseHandler{evaluationsHandler{env: env, now: time.Now}}
	p := APIPrefix + "/evaluations/{id}"
	r.GET(p+"/release-preview", h.preview)
	r.POST(p+"/release", h.release)
	r.POST(p+"/schedule", h.schedule)
	r.GET(APIPrefix+"/agents/{id}/live-version", h.live)
	return r, root, rl, ev
}

func TestEvaluationReleasePreviewAndRelease(t *testing.T) {
	r, _, rl, ev := evalReleaseFixture(t, nil)
	ctx := evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID+"/release-preview", "")
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("preview %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var p EvaluationReleasePreview
	if err := json.Unmarshal(ctx.Response.Body(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Problem != "" || p.Agent != "codex" || p.Version != 2 || p.ReplacesVersion != 1 || p.Candidate != "B" {
		t.Fatalf("preview %+v", p)
	}
	if p.Baseline == nil || p.Baseline.Arm != "A" || !p.Baseline.Live || p.Summary == nil || p.Summary.Baseline == nil || p.Summary.Candidate.Runs == 0 {
		t.Fatalf("baseline %+v summary %+v", p.Baseline, p.Summary)
	}
	if len(p.Children) != 1 || !p.Children[0].Included || len(p.Summary.Capabilities) == 0 {
		t.Fatalf("children %+v caps %+v", p.Children, p.Summary.Capabilities)
	}

	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/release", `{}`)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest || !strings.Contains(string(ctx.Response.Body()), "approver") {
		t.Fatalf("no approver: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	want := fasthttp.StatusCreated
	body := `{"approver":"ana","note":"weekly"}`
	if !p.Ready {
		body = `{"approver":"ana","override":"accepted"}`
	}
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/release", body)
	if ctx.Response.StatusCode() != want {
		t.Fatalf("release %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	e := rl.last.Evaluation
	if e == nil || e.AgentID != "codex" || e.Version != 2 || e.ReplacesVersion != 1 || e.CandidateArm != "B" || len(e.Children) != 1 || e.Children[0].ExpID != testExp {
		t.Fatalf("release request %+v", e)
	}

	ctx = evalCall(r, "GET", APIPrefix+"/agents/codex/live-version", "")
	var lv LiveVersion
	_ = json.Unmarshal(ctx.Response.Body(), &lv)
	if lv.Version != 2 || lv.ReleaseID != "rel_2" {
		t.Fatalf("live %+v", lv)
	}
	// v2 is now live: releasing it again is refused.
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/release", `{"approver":"ana"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(ctx.Response.Body()), "already the live version") {
		t.Fatalf("second release %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestEvaluationReleaseGateBlocks(t *testing.T) {
	r, _, _, ev := evalReleaseFixture(t, &schemas.EvaluationGate{MinScore: map[string]float64{"terminal": 0.5}})
	ctx := evalCall(r, "GET", APIPrefix+"/evaluations/"+ev.ID+"/release-preview", "")
	var p EvaluationReleasePreview
	if err := json.Unmarshal(ctx.Response.Body(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Gate == nil || p.Gate.State != schemas.GateFail || p.Ready || len(p.Blocked) == 0 {
		t.Fatalf("gate %+v blocked %v ready %v", p.Gate, p.Blocked, p.Ready)
	}
}

func TestEvaluationNotDoneCannotRelease(t *testing.T) {
	r, root, _, ev := evalReleaseFixture(t, nil)
	ev.Status = schemas.EvaluationRunning
	ev.Children[0].Status = string(schemas.ExperimentRunning)
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	h := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/release", `{"approver":"ana"}`)
	if h.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("release of a running evaluation: %d %s", h.Response.StatusCode(), h.Response.Body())
	}
}

func TestEvaluationScheduleRepeat(t *testing.T) {
	r, root, _, ev := evalReleaseFixture(t, nil)
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/schedule", `{"cadence":"hourly"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("bad cadence %d", ctx.Response.StatusCode())
	}
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/schedule", `{"cadence":"nightly","timezone":"UTC"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("schedule %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	f, err := LoadWebSchedules(root)
	if err != nil || len(f.Schedules) != 1 {
		t.Fatalf("schedules %+v %v", f, err)
	}
	s := f.Schedules[0]
	if s.Evaluation != ev.ID || s.Cron != "0 2 * * *" || s.MaxUSDPerDay != QuickRunDefaultBudget {
		t.Fatalf("schedule %+v", s)
	}
	if _, err := os.Stat(WebSchedulesPath(root)); err != nil {
		t.Fatal(err)
	}
	ctx = evalCall(r, "POST", APIPrefix+"/evaluations/"+ev.ID+"/schedule", `{"cadence":"nightly"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("duplicate %d", ctx.Response.StatusCode())
	}
}

func TestNormalizeGate(t *testing.T) {
	for _, g := range []*schemas.EvaluationGate{
		{MaxRegression: 2}, {MinScore: map[string]float64{"Coding": 0.5}}, {MinScore: map[string]float64{"coding": 1.5}}, {BaselineVersion: -1},
	} {
		if err := normalizeGate(&schemas.EvaluationSpec{Gate: g}); err == nil {
			t.Errorf("gate %+v accepted", g)
		}
	}
	spec := &schemas.EvaluationSpec{Gate: &schemas.EvaluationGate{BaselineVersion: 2}}
	if err := normalizeGate(spec); err != nil || spec.Gate != nil {
		t.Errorf("a gate with no rule: %+v %v", spec.Gate, err)
	}
}

func TestNormalizeGateSlices(t *testing.T) {
	half, over := 0.5, 1.5
	for _, g := range []*schemas.EvaluationGate{
		{Slices: []schemas.GateSliceRule{{Key: "ti er", Value: "x", MinScore: &half}}},
		{Slices: []schemas.GateSliceRule{{Key: "tier", Value: "", MinScore: &half}}},
		{Slices: []schemas.GateSliceRule{{Key: "tier", Value: "x", MinScore: &over}}},
		{Slices: []schemas.GateSliceRule{{Key: "tier", Value: "x", MaxRegression: 2}}},
		{Slices: []schemas.GateSliceRule{{Key: "tier", Value: "x", MinScore: &half}, {Key: "tier", Value: "x", MaxRegression: 0.1}}},
	} {
		if err := normalizeGate(&schemas.EvaluationSpec{Gate: g}); err == nil {
			t.Errorf("gate %+v accepted", g.Slices)
		}
	}
	spec := &schemas.EvaluationSpec{Gate: &schemas.EvaluationGate{Slices: []schemas.GateSliceRule{{Key: "tier", Value: "free"}}}}
	if err := normalizeGate(spec); err != nil || spec.Gate != nil {
		t.Errorf("a slice rule with no check: %+v %v", spec.Gate, err)
	}
	spec = &schemas.EvaluationSpec{Gate: &schemas.EvaluationGate{Slices: []schemas.GateSliceRule{{Key: " tier ", Value: " enterprise ", MaxRegression: 0.05}}}}
	if err := normalizeGate(spec); err != nil || spec.Gate == nil || spec.Gate.Slices[0].Key != "tier" || spec.Gate.Slices[0].Value != "enterprise" {
		t.Errorf("slice rule kept: %+v %v", spec.Gate, err)
	}
}
