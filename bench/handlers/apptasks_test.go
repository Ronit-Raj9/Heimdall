package handlers

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/apptasks"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

const appTasksSpec = `openapi: 3.0.3
info: {title: Acme Shop API, version: 1.0.0}
paths:
  /orders/{order_id}:
    get:
      operationId: getOrder
      parameters: [{name: order_id, in: path, required: true, schema: {type: string}}]
    patch:
      operationId: updateOrder
      parameters: [{name: order_id, in: path, required: true, schema: {type: string}}]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                status: {type: string, enum: [delivered, refunded]}
                total: {type: number}
`

// appChat is the pinned model in the test: one rule, a seed and one
// scenario per call.
type appChat struct{}

func (appChat) Chat(_ context.Context, _, user string, _ int) (string, *schemas.Tokens, string, error) {
	tok := &schemas.Tokens{Fresh: 100, Out: 50}
	switch {
	case strings.HasPrefix(user, "Read the company's API"):
		return `{"rules": [{"id": "R1", "text": "Refund delivered orders within 30 days.", "kind": "must", "operations": ["updateOrder"]}]}`, tok, "m", nil
	case strings.HasPrefix(user, "Write the starting data"):
		return `{"collections": {"orders": [{"id": "O-1", "status": "delivered", "total": 45}]}}`, tok, "m", nil
	}
	return `{"scenarios": [{"rule_id": "R1", "title": "Refund O-1", "opening_message": "Refund O-1 please", "persona_goal": "Refund O-1",
	 "expected_actions": [{"tool": "getOrder", "args": {"order_id": "O-1"}}, {"tool": "updateOrder", "args": {"order_id": "O-1", "body": {"status": "refunded"}}}]}]}`, tok, "m", nil
}

func newAppTasksClient(t *testing.T) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Projects: ps, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["apptasks"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

func TestAppTasksFlow(t *testing.T) {
	c := newAppTasksClient(t)
	code, body := c.do(t, "POST", "/app-tasks", jsonBody(t, AppTasksCreateRequest{Suite: "shop-support", App: "shop", Spec: appTasksSpec, Policy: "Refund delivered orders within 30 days."}), nil)
	if code != fasthttp.StatusCreated {
		t.Fatalf("create %d %s", code, body)
	}
	var v struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		Operations []struct {
			Name string `json:"name"`
		} `json:"operations"`
		Next string `json:"next"`
	}
	_ = json.Unmarshal(body, &v)
	if v.Status != apptasks.StatusNew || len(v.Operations) != 2 || !strings.Contains(v.Next, "stage rules") {
		t.Fatalf("draft %s", body)
	}
	for name, req := range map[string]AppTasksCreateRequest{
		"no spec":   {Suite: "x", App: "shop", Policy: "p"},
		"bad spec":  {Suite: "x", App: "shop", Spec: "swagger: '2.0'\n", Policy: "p"},
		"no policy": {Suite: "x", App: "shop", Spec: appTasksSpec},
	} {
		if code, body := c.do(t, "POST", "/app-tasks", jsonBody(t, req), nil); code < 400 {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if code, _ := c.do(t, "GET", "/app-tasks/ag_000000000000", nil, nil); code != fasthttp.StatusNotFound {
		t.Errorf("missing draft %d", code)
	}

	// The job's model steps, in process with the test's model.
	st := apptasks.Store{Dir: filepath.Join(c.root, apptasks.Folder)}
	d, err := st.Load(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _, _ := st.Mock(d)
	_, policy, _ := st.Files(d)
	a := &apptasks.Assistant{Chat: appChat{}, Model: apptasks.Pinned{Provider: "openai", Model: "m"}, Budget: &apptasks.Budget{MaxCalls: 10}}
	if err := apptasks.DraftRules(context.Background(), a, d, m, policy); err != nil {
		t.Fatal(err)
	}
	_ = st.Save(d)

	// Writing before the rules are approved and the tasks drafted is refused.
	if code, body := c.do(t, "POST", "/app-tasks/"+v.ID+"/write", []byte(`{}`), nil); code != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("early write %d %s", code, body)
	}
	if code, body := c.do(t, "PUT", "/app-tasks/"+v.ID+"/rules", []byte(`{"rules":[{"id":"R1","text":"x","kind":"maybe","operations":[]}],"approve":true}`), nil); code != fasthttp.StatusUnprocessableEntity || !strings.Contains(string(body), "kind") {
		t.Fatalf("bad rules %d %s", code, body)
	}
	code, body = c.do(t, "PUT", "/app-tasks/"+v.ID+"/rules", []byte(`{"rules":[{"id":"R1","text":"Refund delivered orders within 30 days of delivery.","kind":"must","operations":["updateOrder"]}],"approve":true}`), nil)
	if code != fasthttp.StatusOK || !strings.Contains(string(body), apptasks.StatusRulesApproved) {
		t.Fatalf("approve %d %s", code, body)
	}

	// A running job holds the draft: edits wait.
	unlock, err := st.TryLock(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do(t, "POST", "/app-tasks/"+v.ID+"/write", []byte(`{}`), nil); code != fasthttp.StatusConflict {
		t.Errorf("write during a job %d", code)
	}
	unlock()

	d, _ = st.Load(v.ID)
	if err := apptasks.GenerateTasks(context.Background(), a, d, m, policy, apptasks.GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = st.Save(d)
	if len(d.Plans) != 1 {
		t.Fatalf("plans %d dropped %+v", len(d.Plans), d.Dropped)
	}

	code, body = c.do(t, "POST", "/app-tasks/"+v.ID+"/write", []byte(`{}`), nil)
	if code != fasthttp.StatusCreated {
		t.Fatalf("write %d %s", code, body)
	}
	var wr AppTasksWriteResult
	_ = json.Unmarshal(body, &wr)
	if !wr.Created || len(wr.Tasks) != 1 || wr.Tasks[0].Review != apptasks.ReviewPending {
		t.Fatalf("write result %s", body)
	}
	if errs := lintErrors(t, c, "shop-support"); len(errs) > 0 {
		t.Fatalf("lint: %v", errs)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "shop-support", "services", "shop", "mock.json")); err != nil {
		t.Fatal(err)
	}

	code, body = c.do(t, "GET", "/app-tasks", nil, nil)
	if code != fasthttp.StatusOK || !strings.Contains(string(body), `"pending":1`) {
		t.Fatalf("list %d %s", code, body)
	}
	if code, _ := c.do(t, "POST", "/app-tasks/"+v.ID+"/review", []byte(`{"task":"`+wr.Tasks[0].ID+`","decision":"maybe"}`), nil); code != fasthttp.StatusUnprocessableEntity {
		t.Errorf("bad decision %d", code)
	}
	code, body = c.do(t, "POST", "/app-tasks/"+v.ID+"/review", []byte(`{"task":"`+wr.Tasks[0].ID+`","decision":"accepted"}`), nil)
	if code != fasthttp.StatusOK || !strings.Contains(string(body), `"accepted":1`) {
		t.Fatalf("review %d %s", code, body)
	}
	ty, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "shop-support", "tasks", wr.Tasks[0].ID, "task.yaml"))
	if !strings.Contains(string(ty), "instruction_reviewed: true") {
		t.Errorf("task.yaml after accept:\n%s", ty)
	}

	// Another benchmark without this app copy can not take a draft.
	if code, body := c.do(t, "POST", "/app-tasks", jsonBody(t, AppTasksCreateRequest{Suite: "shop-support", App: "billing", Spec: appTasksSpec, Policy: "p"}), nil); code != fasthttp.StatusConflict {
		t.Errorf("other app into the benchmark %d %s", code, body)
	}
	// Writes need the write token.
	if code, _ := c.do(t, "POST", "/app-tasks", jsonBody(t, AppTasksCreateRequest{Suite: "s", App: "shop", Spec: appTasksSpec, Policy: "p"}), map[string]string{"Authorization": "Bearer wrong"}); code != fasthttp.StatusUnauthorized && code != fasthttp.StatusForbidden {
		t.Errorf("wrong token %d", code)
	}
}
