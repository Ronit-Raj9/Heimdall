package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// jobsRequest sends one request through register's routes as principal p
// (nil: no auth middleware ran, local mode).
func jobsRequest(t *testing.T, register func(*router.Router), p *auth.Principal, method, uri, body string, header ...string) *fasthttp.Response {
	t.Helper()
	r := router.New()
	register(r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	for i := 0; i+1 < len(header); i += 2 {
		ctx.Request.Header.Set(header[i], header[i+1])
	}
	ctx.Request.SetBodyString(body)
	if p != nil {
		auth.SetPrincipal(&ctx, p)
	}
	r.Handler(&ctx)
	resp := &fasthttp.Response{}
	streamed := ctx.Response.IsBodyStream()
	out := append([]byte(nil), ctx.Response.Body()...) // drains a stream writer (SSE)
	ctx.Response.CopyTo(resp)
	if streamed {
		resp.SetBody(out)
	}
	return resp
}

func passThrough(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }

func testRunner(t *testing.T) *jobs.Runner {
	t.Helper()
	r := jobs.New(dbtest.Open(t), jobs.Options{Owner: "test", PollEvery: 10 * time.Millisecond, HeartbeatEvery: 20 * time.Millisecond,
		LeaseFor: time.Second, ProgressEvery: 10 * time.Millisecond})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	r.Register("demo.echo", func(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		var p struct {
			Name string `json:"name"`
			Wait bool   `json:"wait"`
		}
		_ = j.DecodeParams(&p)
		log.Printf("hello %s", p.Name)
		progress(map[string]string{"step": "said hello"})
		if p.Wait {
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-release:
			}
		}
		return map[string]string{"greeted": p.Name}, nil
	}, jobs.WithValidator(func(_ context.Context, _ string, raw json.RawMessage) (any, string, error) {
		var p struct {
			Name string `json:"name"`
			Wait bool   `json:"wait"`
		}
		if err := json.Unmarshal(raw, &p); err != nil || p.Name == "" {
			return nil, "", fmt.Errorf("%w: name is required", jobs.ErrInvalidParams)
		}
		if strings.Contains(p.Name, "/") {
			return nil, "", fmt.Errorf("%w: name: path leaves the folder", jobs.ErrInvalidParams)
		}
		return p, "", nil
	}))
	r.Register("demo.internal", func(context.Context, *jobs.Job, jobs.Logger, func(any)) (any, error) { return nil, nil }, jobs.Internal())
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Stop(context.Background()) })
	return r
}

func decodeJob(t *testing.T, resp *fasthttp.Response) jobs.Job {
	t.Helper()
	var j jobs.Job
	if err := json.Unmarshal(resp.Body(), &j); err != nil {
		t.Fatalf("decode job %s: %v", resp.Body(), err)
	}
	return j
}

func TestJobsRoutes(t *testing.T) {
	r := testRunner(t)
	h := NewJobsHandler(testMode, r, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_m", OrgID: "org_a", Role: auth.RoleMember}
	viewer := &auth.Principal{Kind: auth.KindUser, UserID: "usr_v", OrgID: "org_a", Role: auth.RoleViewer}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "usr_o", OrgID: "org_b", Role: auth.RoleOwner}

	for _, tc := range []struct {
		name string
		p    *auth.Principal
		body string
		code int
		want string
	}{
		{"viewer may not create", viewer, `{"kind":"demo.echo","params":{"name":"x"}}`, 403, "member"},
		{"anonymous", auth.Anonymous(), `{"kind":"demo.echo","params":{"name":"x"}}`, 401, "sign in"},
		{"unknown kind", member, `{"kind":"nope"}`, 400, "unknown job kind"},
		{"internal kind", member, `{"kind":"demo.internal"}`, 400, "cannot be created"},
		{"missing params", member, `{"kind":"demo.echo"}`, 400, "name is required"},
		{"path outside", member, `{"kind":"demo.echo","params":{"name":"../etc"}}`, 400, "path leaves"},
		{"unknown field", member, `{"kind":"demo.echo","extra":1}`, 400, "unknown field"},
		{"no kind", member, `{}`, 400, "kind is required"},
	} {
		resp := jobsRequest(t, reg, tc.p, "POST", "/api/bench/jobs", tc.body)
		if resp.StatusCode() != tc.code || !strings.Contains(string(resp.Body()), tc.want) {
			t.Fatalf("%s: %d %s", tc.name, resp.StatusCode(), resp.Body())
		}
	}

	resp := jobsRequest(t, reg, member, "POST", "/api/bench/jobs", `{"kind":"demo.echo","params":{"name":"ada"}}`)
	if resp.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	j := decodeJob(t, resp)
	if j.OrgID != "org_a" || j.CreatedBy != "usr_m" || j.Kind != "demo.echo" {
		t.Fatalf("created %+v", j)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := decodeJob(t, jobsRequest(t, reg, viewer, "GET", "/api/bench/jobs/"+j.ID, ""))
		if got.Status == jobs.StatusSucceeded {
			if string(got.Result) != `{"greeted":"ada"}` || string(got.Progress) != `{"step":"said hello"}` {
				t.Fatalf("finished job %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never finished: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resp := jobsRequest(t, reg, other, "GET", "/api/bench/jobs/"+j.ID, ""); resp.StatusCode() != 404 {
		t.Fatalf("another org read the job: %d", resp.StatusCode())
	}
	if resp := jobsRequest(t, reg, other, "POST", "/api/bench/jobs/"+j.ID+"/cancel", ""); resp.StatusCode() != 404 {
		t.Fatalf("another org cancelled the job: %d", resp.StatusCode())
	}

	var list JobList
	resp = jobsRequest(t, reg, viewer, "GET", "/api/bench/jobs?kind=demo.echo&status=succeeded", "")
	if err := json.Unmarshal(resp.Body(), &list); err != nil || len(list.Jobs) != 1 || len(list.Kinds) != 1 || list.Kinds[0] != "demo.echo" {
		t.Fatalf("list: %v %s", err, resp.Body())
	}
	if resp := jobsRequest(t, reg, viewer, "GET", "/api/bench/jobs?status=bogus", ""); resp.StatusCode() != 400 {
		t.Fatalf("bad status filter: %d", resp.StatusCode())
	}
	resp = jobsRequest(t, reg, other, "GET", "/api/bench/jobs", "")
	if err := json.Unmarshal(resp.Body(), &list); err != nil || len(list.Jobs) != 0 {
		t.Fatalf("org_b list: %v %s", err, resp.Body())
	}

	var page jobs.LogPage
	resp = jobsRequest(t, reg, viewer, "GET", "/api/bench/jobs/"+j.ID+"/logs?since=0", "")
	if err := json.Unmarshal(resp.Body(), &page); err != nil || len(page.Lines) != 1 || page.Lines[0].Line != "hello ada" || page.Next != 1 {
		t.Fatalf("logs: %v %s", err, resp.Body())
	}
	resp = jobsRequest(t, reg, viewer, "GET", "/api/bench/jobs/"+j.ID+"/logs", "", "Accept", "text/event-stream")
	body := string(resp.Body())
	if !strings.Contains(body, "event: log\ndata: ") || !strings.Contains(body, `"line":"hello ada"`) || !strings.Contains(body, "id: 1\n") ||
		!strings.Contains(body, "event: end\n") || !strings.Contains(body, `"status":"succeeded"`) {
		t.Fatalf("SSE body %q", body)
	}
	if resp := jobsRequest(t, reg, member, "POST", "/api/bench/jobs/"+j.ID+"/cancel", ""); resp.StatusCode() != 409 {
		t.Fatalf("cancel a finished job: %d %s", resp.StatusCode(), resp.Body())
	}

	// Local mode: no principal means the local org; cancel mid-job.
	resp = jobsRequest(t, reg, nil, "POST", "/api/bench/jobs", `{"kind":"demo.echo","params":{"name":"bo","wait":true}}`)
	if resp.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("local create: %d %s", resp.StatusCode(), resp.Body())
	}
	lj := decodeJob(t, resp)
	if lj.OrgID != auth.LocalOrgID || lj.CreatedBy != "local" {
		t.Fatalf("local job %+v", lj)
	}
	for decodeJob(t, jobsRequest(t, reg, nil, "GET", "/api/bench/jobs/"+lj.ID, "")).Status != jobs.StatusRunning {
		time.Sleep(5 * time.Millisecond)
	}
	if resp := jobsRequest(t, reg, nil, "POST", "/api/bench/jobs/"+lj.ID+"/cancel", ""); resp.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("cancel: %d %s", resp.StatusCode(), resp.Body())
	}
	deadline = time.Now().Add(10 * time.Second)
	for decodeJob(t, jobsRequest(t, reg, nil, "GET", "/api/bench/jobs/"+lj.ID, "")).Status != jobs.StatusCancelled {
		if time.Now().After(deadline) {
			t.Fatal("job not cancelled")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestJobsWriteRoutesNeedWriteMiddleware(t *testing.T) {
	r := testRunner(t)
	deny := func(fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusUnauthorized) }
	}
	h := NewJobsHandler(testMode, r, deny)
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	for _, path := range []string{"/api/bench/jobs", "/api/bench/jobs/job_x/cancel"} {
		if resp := jobsRequest(t, reg, nil, "POST", path, `{"kind":"demo.echo","params":{"name":"x"}}`); resp.StatusCode() != 401 {
			t.Fatalf("POST %s without the write token: %d", path, resp.StatusCode())
		}
	}
	if resp := jobsRequest(t, reg, nil, "GET", "/api/bench/jobs", ""); resp.StatusCode() != 200 {
		t.Fatalf("GET jobs: %d", resp.StatusCode())
	}
	if resp := jobsRequest(t, func(rt *router.Router) { NewJobsHandler(testMode, nil, nil).RegisterRoutes(rt) }, nil, "GET", "/api/bench/jobs", ""); resp.StatusCode() != 503 {
		t.Fatalf("no runner: %d", resp.StatusCode())
	}
}

// runLauncher is a launcher whose experiments run as jobs.
type runLauncher struct {
	fakeLauncher
	cancelled map[string]bool
	running   bool
	regraded  *RegradeRequest
}

func (l *runLauncher) Cancel(_ context.Context, expID string) error {
	if !l.running {
		return fmt.Errorf("experiment %s is not running: %w", expID, ErrNotRunning)
	}
	l.running, l.cancelled[expID] = false, true
	return nil
}

func (l *runLauncher) Resume(_ context.Context, expID string) error {
	if l.running {
		return fmt.Errorf("experiment %s already has a queued or running job: %w", expID, schemas.ErrExists)
	}
	l.running, l.cancelled[expID] = true, false
	return nil
}

func (l *runLauncher) Cancelled(expID string) bool { return l.cancelled[expID] }
func (l *runLauncher) Active(string) bool          { return l.running }

func (l *runLauncher) Regrade(_ context.Context, expID string, req RegradeRequest) (*jobs.Job, error) {
	if strings.Contains(req.SuiteLock, "..") {
		return nil, fmt.Errorf("%w: suite_lock: path leaves the folder", jobs.ErrInvalidParams)
	}
	l.regraded = &req
	return &jobs.Job{ID: "job_1", Kind: "experiment.regate", Status: jobs.StatusQueued, Target: "exp:" + expID,
		Params: json.RawMessage(`{"exp_id":"` + expID + `","id":"rg_1"}`)}, nil
}

func TestExperimentCancelResumeRegradeRoutes(t *testing.T) {
	idx := testIndex()
	l := &runLauncher{cancelled: map[string]bool{}, running: true}
	h := NewExperimentsHandler(testMode, idx, nil, l, nil, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	base := "/api/bench/experiments/" + testExp
	// testIndex has every slot sealed: drop one so slots are left.
	idx.runs = idx.runs[:1]

	resp := jobsRequest(t, reg, nil, "POST", base+"/cancel", "")
	var st schemas.ExperimentStatus
	if resp.StatusCode() != fasthttp.StatusAccepted || json.Unmarshal(resp.Body(), &st) != nil || st.State != schemas.ExperimentCancelled {
		t.Fatalf("cancel: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := jobsRequest(t, reg, nil, "POST", base+"/cancel", ""); resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("cancel twice: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = jobsRequest(t, reg, nil, "GET", base, "")
	if json.Unmarshal(resp.Body(), &st) != nil || st.State != schemas.ExperimentCancelled {
		t.Fatalf("status after cancel %s", resp.Body())
	}
	// N2: the server refuses a resume of a comparison with no spend cap.
	idx.manifests[testExp].Experiment.Budget.TotalUSD = 0
	if resp := jobsRequest(t, reg, nil, "POST", base+"/resume", ""); resp.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(resp.Body()), "no spend cap") {
		t.Fatalf("resume without a cap: %d %s", resp.StatusCode(), resp.Body())
	}
	idx.manifests[testExp].Experiment.Budget.TotalUSD = 5
	resp = jobsRequest(t, reg, nil, "POST", base+"/resume", "")
	if resp.StatusCode() != fasthttp.StatusAccepted || json.Unmarshal(resp.Body(), &st) != nil || st.State != schemas.ExperimentRunning {
		t.Fatalf("resume: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := jobsRequest(t, reg, nil, "POST", base+"/resume", ""); resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("resume while running: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := jobsRequest(t, reg, nil, "POST", "/api/bench/experiments/exp_nope/cancel", ""); resp.StatusCode() != 404 {
		t.Fatalf("unknown experiment: %d", resp.StatusCode())
	}

	resp = jobsRequest(t, reg, nil, "POST", base+"/regrade", `{"note":"gate v2"}`)
	if resp.StatusCode() != fasthttp.StatusAccepted || l.regraded == nil || l.regraded.Note != "gate v2" {
		t.Fatalf("regrade: %d %s", resp.StatusCode(), resp.Body())
	}
	if j := decodeJob(t, resp); j.Kind != "experiment.regate" {
		t.Fatalf("regrade job %+v", j)
	}
	if resp := jobsRequest(t, reg, nil, "POST", base+"/regrade", `{"suite_lock":"../x"}`); resp.StatusCode() != 400 {
		t.Fatalf("regrade outside the project: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := jobsRequest(t, reg, nil, "POST", base+"/regrade", `{"bogus":1}`); resp.StatusCode() != 400 {
		t.Fatalf("regrade unknown field: %d %s", resp.StatusCode(), resp.Body())
	}

	// A launcher without jobs answers 503.
	plain := NewExperimentsHandler(testMode, testIndex(), nil, &fakeLauncher{}, nil, passThrough)
	for _, path := range []string{"/cancel", "/resume", "/regrade"} {
		if resp := jobsRequest(t, func(rt *router.Router) { plain.RegisterRoutes(rt) }, nil, "POST", base+path, ""); resp.StatusCode() != 503 {
			t.Fatalf("%s without jobs: %d", path, resp.StatusCode())
		}
	}
}

func TestBuildStatusCancelled(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	runs := idx.Runs(testExp)[:1]
	if st := BuildStatus(m, runs, StatusFacts{Cancelled: true}); st.State != schemas.ExperimentCancelled {
		t.Fatalf("slots left, cancelled: %s", st.State)
	}
	if st := BuildStatus(m, runs, StatusFacts{Cancelled: true, Active: true}); st.State != schemas.ExperimentRunning {
		t.Fatalf("resumed after a cancel: %s", st.State)
	}
	if st := BuildStatus(m, idx.Runs(testExp), StatusFacts{Cancelled: true}); st.State != schemas.ExperimentDone {
		t.Fatalf("complete experiment: %s", st.State)
	}
	if st := BuildStatus(m, runs, StatusFacts{}); st.State != schemas.ExperimentFailed {
		t.Fatalf("stopped without a cancel: %s", st.State)
	}
}

func TestJobsAuditRows(t *testing.T) {
	r := testRunner(t)
	var rows []string
	h := NewJobsHandler(testMode, r, passThrough).WithAudit(func(_ *fasthttp.RequestCtx, org, action, tt, tid string, details map[string]any) {
		rows = append(rows, org+" "+action+" "+tt+" "+fmt.Sprint(details["kind"]))
	})
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_m", OrgID: "org_a", Role: auth.RoleMember}
	j := decodeJob(t, jobsRequest(t, reg, member, "POST", "/api/bench/jobs", `{"kind":"demo.echo","params":{"name":"x","wait":true}}`))
	if resp := jobsRequest(t, reg, member, "POST", "/api/bench/jobs/"+j.ID+"/cancel", ""); resp.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("cancel: %d %s", resp.StatusCode(), resp.Body())
	}
	jobsRequest(t, reg, member, "POST", "/api/bench/jobs", `{"kind":"nope"}`) // refused: no row
	if strings.Join(rows, "|") != "org_a job.create job demo.echo|org_a job.cancel job demo.echo" {
		t.Fatalf("audit rows %q", rows)
	}
}
