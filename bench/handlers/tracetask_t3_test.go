package handlers

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeTraceJobs records created jobs; finish plays lane T1's job.
type fakeTraceJobs struct {
	JobsAPI
	mu   sync.Mutex
	list []jobs.Job
}

func (f *fakeTraceJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := jobs.Job{ID: "job_" + string(rune('a'+len(f.list))), OrgID: n.OrgID, Kind: n.Kind, Params: n.Params, Status: jobs.StatusQueued,
		CreatedAt: time.Unix(int64(1000+len(f.list)), 0)}
	f.list = append(f.list, j)
	return &j, nil
}

func (f *fakeTraceJobs) List(_ context.Context, org string, flt jobs.Filter) (*jobs.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []jobs.Job{}
	for _, j := range f.list {
		if j.OrgID == org && j.Kind == flt.Kind {
			out = append(out, j)
		}
	}
	return &jobs.Page{Jobs: out}, nil
}

func (f *fakeTraceJobs) finish(i int, result []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list[i].Status, f.list[i].Result = jobs.StatusSucceeded, result
}

func traceTaskServer(t *testing.T, role auth.Role) (*testClient, *fakeTraceJobs, projectfs.Store) {
	t.Helper()
	store, err := projectfs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fj := &fakeTraceJobs{}
	r := router.New()
	orgAreasMu.Lock()
	area := orgAreas["tracetask"]
	orgAreasMu.Unlock()
	area(OrgEnv{Mode: testMode, Services: Services{OrgID: "org_a", Jobs: fj, Projects: store}}, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", Email: "ada@example.com", OrgID: "org_a", Role: role})
		r.Handler(ctx)
	}}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}, fj, store
}

func readState(t *testing.T, c *testClient) TraceTaskState {
	t.Helper()
	resp := c.do(t, "GET", APIPrefix+"/inbox/inc-1/build-task", nil, nil)
	var st TraceTaskState
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &st) != nil {
		t.Fatalf("get: %d %s", resp.StatusCode(), resp.Body())
	}
	return st
}

// The form's body starts a tracetask.build job; the job's result (fixture of
// the contract) is read back with Approve offered; approving sets
// instruction_reviewed in the stored task.yaml and the guard then holds.
func TestTraceTaskWebFlow(t *testing.T) {
	c, fj, store := traceTaskServer(t, auth.RoleMember)
	if st := readState(t, c); st.JobID != "" {
		t.Fatalf("no job yet: %+v", st)
	}
	resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"support","model":"gpt-6-luna","budget_usd":0.5}`), nil)
	if resp.StatusCode() != 202 {
		t.Fatalf("start: %d %s", resp.StatusCode(), resp.Body())
	}
	var p TraceTaskParams
	if err := json.Unmarshal(fj.list[0].Params, &p); err != nil || p.InboxID != "inc-1" || p.BudgetUSD != 0.5 || fj.list[0].Kind != TraceTaskKind {
		t.Fatalf("params %s", fj.list[0].Params)
	}
	if st := readState(t, c); st.Status != "queued" || st.CanApprove {
		t.Fatalf("queued: %+v", st)
	}
	fixture, err := os.ReadFile("testdata/tracetask_result_t3.json")
	if err != nil {
		t.Fatal(err)
	}
	var res TraceTaskResult
	if err := json.Unmarshal(fixture, &res); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(context.Background(), projectfs.AreaBenchmarks, "support/tasks/from-inbox-1-built/task.yaml", taskYAML(res), projectfs.WriteOpts{}); err != nil {
		t.Fatal(err)
	}
	fj.finish(0, fixture)
	st := readState(t, c)
	if !st.CanApprove || st.Result == nil || st.Result.Failure.Kind != "false_claim" || len(st.Result.Admission.Controls) != 7 || st.Result.Mutants[0].Control != "variant_claims_success" {
		t.Fatalf("done: %+v", st)
	}
	resp = c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task/approve", []byte(`{"job_id":"`+st.JobID+`"}`), nil)
	var ap TraceTaskApproved
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &ap) != nil || ap.FinalizeJobID != "job_b" || ap.Decision.By != "ada@example.com" {
		t.Fatalf("approve: %d %s", resp.StatusCode(), resp.Body())
	}
	raw, _, err := store.Read(context.Background(), projectfs.AreaBenchmarks, "support/tasks/from-inbox-1-built/task.yaml")
	if err != nil || !strings.Contains(string(raw), "instruction_reviewed: true") {
		t.Fatalf("task.yaml %s %v", raw, err)
	}
	// The approval record is written, then the finalize job starts with T1's params.
	if dec, _, err := store.Read(context.Background(), projectfs.AreaUploads, "tracetask/inc-1/decision.json"); err != nil || !strings.Contains(string(dec), `"approved"`) {
		t.Fatalf("decision %s %v", dec, err)
	}
	var fp TraceTaskParams
	if err := json.Unmarshal(fj.list[1].Params, &fp); err != nil || fp.Mode != "finalize" || fp.InboxID != "inc-1" || fp.Suite != "support" || fp.TaskID != "from-inbox-1-built" || fp.Model != "" || fp.BudgetUSD != 0 {
		t.Fatalf("finalize params %s", fj.list[1].Params)
	}
	if st := readState(t, c); st.CanApprove || st.Decision == nil || st.Finalize == nil || st.Finalize.Status != "queued" || st.JobID != "job_a" {
		t.Fatalf("after approve: %+v", st)
	}
	// A finalize that admitted the task: ready.
	fj.finish(1, []byte(`{"task":{"suite":"support","id":"from-inbox-1-built","path":"support/tasks/from-inbox-1-built"},"admission":{"status":"ran","admitted":true,"controls":[{"name":"reference","want":"pass","got":"pass"}],"guards":[{"name":"guard_instruction_clear","got":"pass"}]}}`))
	if st := readState(t, c); st.Finalize == nil || !st.Finalize.Admitted || st.CanRetry {
		t.Fatalf("admitted: %+v", st.Finalize)
	}
}

// A finalize that failed leaves the task a draft with the failing control and Try again.
func TestTraceTaskFinalizeRefused(t *testing.T) {
	c, fj, store := traceTaskServer(t, auth.RoleMember)
	c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"support","model":"m","budget_usd":0.5}`), nil)
	fixture, _ := os.ReadFile("testdata/tracetask_result_t3.json")
	var res TraceTaskResult
	_ = json.Unmarshal(fixture, &res)
	if _, err := store.Write(context.Background(), projectfs.AreaBenchmarks, "support/tasks/from-inbox-1-built/task.yaml", taskYAML(res), projectfs.WriteOpts{}); err != nil {
		t.Fatal(err)
	}
	fj.finish(0, fixture)
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task/approve", []byte(`{"job_id":"job_a"}`), nil); resp.StatusCode() != 200 {
		t.Fatalf("approve: %d %s", resp.StatusCode(), resp.Body())
	}
	fj.finish(1, []byte(`{"task":{"suite":"support","id":"from-inbox-1-built"},"admission":{"status":"ran","admitted":false,"controls":[{"name":"variant_no_call","want":"fail","got":"pass"}],"guards":[]},"failing":"variant_no_call: want fail got pass"}`))
	st := readState(t, c)
	if st.Finalize == nil || st.Finalize.Admitted || len(st.Failing) != 2 || !strings.Contains(st.Failing[0], "variant_no_call") || !st.CanRetry || st.CanApprove {
		t.Fatalf("refused finalize: %+v %+v", st, st.Finalize)
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"","model":"","budget_usd":0,"retry":true}`), nil); resp.StatusCode() != 202 {
		t.Fatalf("retry: %d %s", resp.StatusCode(), resp.Body())
	}
}

// A refused draft offers no Approve, offers Try again once with the
// failing control as feedback and the rest of the cap.
func TestTraceTaskRefusedRetryOnce(t *testing.T) {
	c, fj, _ := traceTaskServer(t, auth.RoleMember)
	c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"s","model":"m","budget_usd":0.5}`), nil)
	fixture, _ := os.ReadFile("testdata/tracetask_result_t3.json")
	var res TraceTaskResult
	if err := json.Unmarshal(fixture, &res); err != nil {
		t.Fatal(err)
	}
	res.Admission.Controls[6].Got = "pass" // variant_no_call passes: admission refuses
	refused, _ := json.Marshal(res)
	fj.finish(0, refused)
	st := readState(t, c)
	if st.CanApprove || !st.CanRetry || len(st.Failing) != 1 || !strings.Contains(st.Failing[0], "variant_no_call") {
		t.Fatalf("refused: %+v", st)
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task/approve", []byte(`{"job_id":"`+st.JobID+`"}`), nil); resp.StatusCode() != 409 {
		t.Fatalf("approve refused draft: %d", resp.StatusCode())
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"","model":"","budget_usd":0,"retry":true}`), nil); resp.StatusCode() != 202 {
		t.Fatalf("retry: %d %s", resp.StatusCode(), resp.Body())
	}
	var p TraceTaskParams
	_ = json.Unmarshal(fj.list[1].Params, &p)
	if p.RetryOf != "job_a" || p.BudgetUSD < 0.479 || p.BudgetUSD > 0.481 || !strings.Contains(p.Feedback, "variant_no_call") {
		t.Fatalf("retry params %+v", p)
	}
	fj.finish(1, refused)
	if st := readState(t, c); st.CanRetry {
		t.Fatalf("second retry offered: %+v", st)
	}
}

// A viewer reads but is never offered Approve and cannot start a build.
func TestTraceTaskViewer(t *testing.T) {
	c, fj, _ := traceTaskServer(t, auth.RoleViewer)
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"s","model":"m","budget_usd":0.5}`), nil); resp.StatusCode() != 403 {
		t.Fatalf("viewer start: %d", resp.StatusCode())
	}
	fj.list = append(fj.list, jobs.Job{ID: "job_x", OrgID: "org_a", Kind: TraceTaskKind, Params: []byte(`{"inbox_id":"inc-1","budget_usd":0.5}`)})
	fixture, _ := os.ReadFile("testdata/tracetask_result_t3.json")
	fj.finish(0, fixture)
	if st := readState(t, c); st.CanApprove || st.Blocked == "" {
		t.Fatalf("viewer: %+v", st)
	}
	if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(`{"suite":"s","model":"","budget_usd":0.5}`), nil); resp.StatusCode() != 403 {
		t.Fatalf("viewer: %d", resp.StatusCode())
	}
}

// Without a model or a cap nothing starts.
func TestTraceTaskRefusesWithoutModelOrCap(t *testing.T) {
	c, fj, _ := traceTaskServer(t, auth.RoleMember)
	for _, b := range []string{`{"suite":"s","model":"","budget_usd":0.5}`, `{"suite":"s","model":"m","budget_usd":0}`, `{"suite":"","model":"m","budget_usd":0.5}`} {
		if resp := c.do(t, "POST", APIPrefix+"/inbox/inc-1/build-task", []byte(b), nil); resp.StatusCode() != 400 {
			t.Fatalf("%s: %d", b, resp.StatusCode())
		}
	}
	if len(fj.list) != 0 {
		t.Fatal("a job started")
	}
}

func taskYAML(res TraceTaskResult) []byte {
	for _, f := range res.Files {
		if f.Path == "task.yaml" {
			return []byte(f.Content)
		}
	}
	return nil
}
