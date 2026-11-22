package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/tracebulk"
	"github.com/valyala/fasthttp"
)

// maximLogsFake is a Maxim log repository with one errored trace and one
// trace an evaluator failed.
func maximLogsFake(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-maxim-api-key") != maximTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/log-repositories/logs/search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			logs := []any{}
			if body["traceFilters"] != nil {
				logs = append(logs, map[string]any{"id": "mx-1", "startTimestamp": "2026-11-21T10:00:00Z"})
			}
			if body["evaluatorFilters"] != nil {
				logs = append(logs, map[string]any{"id": "mx-2", "startTimestamp": "2026-11-21T11:00:00Z",
					"evaluationData": []any{map[string]any{"evaluatorName": "Faithfulness", "pass": false, "reasoning": "policy says 30 days"}}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"logs": logs}, "pagination": map[string]any{"total": len(logs)}})
		case "/v1/log-repositories/logs/traces":
			var tr map[string]any
			switch r.URL.Query().Get("id") {
			case "mx-1":
				tr = map[string]any{"id": "mx-1", "input": "Refund order A1", "output": "Sorry.", "timeline": []any{
					map[string]any{"id": "t1", "type": "tool_call", "name": "refund_order", "args": map[string]any{"order": "A1"}, "status": "error", "error": "refund service down"}}}
			case "mx-2":
				tr = map[string]any{"id": "mx-2", "input": "What is the return window for shoes?", "output": "90 days.",
					"evaluationData": []any{map[string]any{"evaluatorName": "Faithfulness", "pass": false}}}
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": tr})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// fakeReadinessJobs records the jobs it is asked to create.
type fakeReadinessJobs struct {
	mu      sync.Mutex
	created []jobs.NewJob
	status  jobs.Status
}

func (f *fakeReadinessJobs) Kinds() []string { return []string{readinessJobKind} }
func (f *fakeReadinessJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, n)
	return &jobs.Job{ID: "job-" + string(rune('0'+len(f.created))), Kind: n.Kind, Status: jobs.StatusQueued}, nil
}
func (f *fakeReadinessJobs) Get(_ context.Context, _, id string) (*jobs.Job, error) {
	return &jobs.Job{ID: id, Status: f.status}, nil
}
func (f *fakeReadinessJobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) {
	return &jobs.Page{}, nil
}
func (f *fakeReadinessJobs) Logs(context.Context, string, string, int64, int) (*jobs.LogPage, error) {
	return &jobs.LogPage{}, nil
}
func (f *fakeReadinessJobs) Cancel(context.Context, string, string) (*jobs.Job, error) {
	return nil, nil
}

func TestMaximFailuresToTasks(t *testing.T) {
	srv := maximLogsFake(t)
	defer srv.Close()
	old := MaximClient
	MaximClient = maxim.Client{APIBase: srv.URL}
	defer func() { MaximClient = old }()

	root := t.TempDir()
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "answers", Template: scaffold.TemplateQA})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(root, "benchmarks", "answers"), files, false); err != nil {
		t.Fatal(err)
	}
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fj := &fakeReadinessJobs{status: jobs.StatusRunning}
	env := OrgEnv{Cfg: &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}, Mode: testMode, Services: Services{OrgID: "o1", Keys: ks, Jobs: fj}}
	r := router.New()
	mh := &maximHandler{env: env}
	r.PUT(APIPrefix+"/integrations/maxim", env.W(mh.put))
	h := &maximTasksHandler{env: env}
	base := APIPrefix + "/integrations/maxim/batches"
	r.GET(base, env.R(h.list))
	r.POST(base, env.W(h.create))
	r.GET(base+"/{id}", env.R(h.get))
	r.POST(base+"/{id}/draft", env.W(h.draft))
	r.POST(base+"/{id}/tasks/{suite}/{task}/accept", env.W(h.accept))
	r.POST(base+"/{id}/tasks/{suite}/{task}/reject", env.W(h.reject))
	r.POST(base+"/{id}/tasks/{suite}/{task}/readiness", env.W(h.readiness))
	call := func(method, path, body string) (int, string) { return secretsCall(r.Handler, method, path, body) }

	if st, body := call("POST", base, `{"filter":{"errors":true}}`); st != fasthttp.StatusConflict || !strings.Contains(body, "not connected") {
		t.Fatalf("pull before connect: %d %s", st, body)
	}
	if st, body := call("PUT", APIPrefix+"/integrations/maxim", `{"api_key":"`+maximTestKey+`","workspace_id":"ws1","repo_id":"repo1"}`); st != 200 {
		t.Fatalf("connect: %d %s", st, body)
	}
	if st, _ := call("POST", base, `{"filter":{}}`); st != fasthttp.StatusBadRequest {
		t.Fatalf("empty filter: %d", st)
	}
	st, body := call("POST", base, `{"filter":{"errors":true,"evaluators":["Faithfulness"],"days":3}}`)
	if st != fasthttp.StatusCreated || strings.Contains(body, maximTestKey) {
		t.Fatalf("pull: %d %s", st, body)
	}
	var b tracebulk.Batch
	if err := json.Unmarshal([]byte(body), &b); err != nil || b.Counts.Failed != 2 || len(b.Clusters) != 2 {
		t.Fatalf("batch %+v %v", b, err)
	}
	var faith string
	for _, c := range b.Clusters {
		if c.Evaluator == "Faithfulness" {
			faith = c.ID
		}
	}
	if faith == "" {
		t.Fatalf("no Faithfulness cluster in %+v", b.Clusters)
	}
	if st, body = call("GET", base, ""); st != 200 || !strings.Contains(body, b.ID) || !strings.Contains(body, `"connected":true`) {
		t.Fatalf("list: %d %s", st, body)
	}
	if st, _ = call("POST", base+"/"+b.ID+"/draft", `{"suite":"nope","clusters":["`+faith+`"]}`); st != fasthttp.StatusNotFound {
		t.Fatalf("draft into a missing benchmark: %d", st)
	}
	st, body = call("POST", base+"/"+b.ID+"/draft", `{"suite":"answers","clusters":["`+faith+`"]}`)
	var dr MaximDraftResult
	if st != 200 || json.Unmarshal([]byte(body), &dr) != nil || len(dr.Tasks) != 1 || dr.Tasks[0].State != tracebulk.StatePending {
		t.Fatalf("draft: %d %s", st, body)
	}
	task := dr.Tasks[0]
	path := base + "/" + b.ID + "/tasks/answers/" + task.ID
	if st, _ = call("POST", path+"/readiness", ""); st != fasthttp.StatusConflict {
		t.Fatalf("readiness before review: %d", st)
	}
	if st, _ = call("POST", path+"/accept", `{"rubric":"x"}`); st != fasthttp.StatusBadRequest {
		t.Fatalf("qa accept without an answer: %d", st)
	}
	st, body = call("POST", path+"/accept", `{"answer":"Shoes can be returned within 30 days."}`)
	var tr MaximTaskResult
	if st != 200 || json.Unmarshal([]byte(body), &tr) != nil || tr.Task.State != tracebulk.StateAccepted || tr.Task.Readiness.Status != tracebulk.ReadyQueued {
		t.Fatalf("accept: %d %s", st, body)
	}
	if len(fj.created) != 1 || fj.created[0].Kind != "suite.admit" || !strings.Contains(string(fj.created[0].Params), `"task":"`+task.ID+`"`) {
		t.Fatalf("readiness jobs %+v", fj.created)
	}
	// The job finished without an admission: not ready.
	fj.status = jobs.StatusFailed
	if st, body = call("GET", base+"/"+b.ID, ""); st != 200 || !strings.Contains(body, `"status": "not_ready"`) && !strings.Contains(body, `"status":"not_ready"`) {
		t.Fatalf("get after job: %d %s", st, body)
	}
	if st, _ = call("POST", path+"/readiness", ""); st != 200 || len(fj.created) != 2 {
		t.Fatalf("readiness again: %d %d", st, len(fj.created))
	}
	if st, _ = call("POST", path+"/reject", `{"reason":""}`); st != fasthttp.StatusBadRequest {
		t.Fatalf("reject without a reason: %d", st)
	}
	if st, body = call("POST", path+"/reject", `{"reason":"not a real failure"}`); st != 200 || !strings.Contains(body, `"rejected"`) {
		t.Fatalf("reject: %d %s", st, body)
	}
	if _, err := os.Stat(filepath.Join(root, "benchmarks", "answers", "tasks", task.ID)); !os.IsNotExist(err) {
		t.Fatalf("rejected draft still on disk: %v", err)
	}
	if st, _ = call("GET", base+"/b_20260101_deadbeef", ""); st != fasthttp.StatusNotFound {
		t.Fatalf("missing batch: %d", st)
	}
	if st, _ = call("POST", base+"/"+b.ID+"/tasks/answers/nope/accept", `{"answer":"x"}`); st != fasthttp.StatusNotFound {
		t.Fatalf("missing task: %d", st)
	}
}
