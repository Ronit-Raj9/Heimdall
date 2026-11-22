package handlers

import (
	"context"
	"encoding/json"
	"github.com/maximhq/bifrost/bench/jobs"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// onboardFakeJobs records created jobs and reports each one queued.
type onboardFakeJobs struct{ catalogFakeJobs }

func (f *onboardFakeJobs) Get(_ context.Context, org, id string) (*jobs.Job, error) {
	return &jobs.Job{ID: id, OrgID: org, Status: jobs.StatusQueued}, nil
}

func newOnboardClient(t *testing.T, fj *onboardFakeJobs) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}, Mode: testMode,
		Services: Services{Projects: ps, Jobs: fj, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["onboard"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

func TestOnboardRoutes(t *testing.T) {
	fj := &onboardFakeJobs{}
	c := newOnboardClient(t, fj)
	csv := "question,answer\nWhat is 6 x 7?,42\nCapital of France?,Paris\n"
	code, body := c.do(t, "POST", "/onboard/upload?filename=faq.csv", []byte(csv), nil)
	if code != 200 {
		t.Fatalf("upload %d %s", code, body)
	}
	var up OnboardUploadResult
	_ = json.Unmarshal(body, &up)
	in := map[string]any{"upload": up.Upload}
	code, body = c.do(t, "POST", "/onboard/detect", jsonBody(t, map[string]any{"input": in}), nil)
	if code != 200 || !strings.Contains(string(body), `"source":"rows"`) {
		t.Fatalf("detect %d %s", code, body)
	}
	code, body = c.do(t, "POST", "/onboard/inspect", jsonBody(t, map[string]any{"input": in}), nil)
	var inv onboard.Inventory
	if code != 200 || json.Unmarshal(body, &inv) != nil || inv.Units != 2 {
		t.Fatalf("inspect %d %s", code, body)
	}
	code, body = c.do(t, "POST", "/onboard/drafts", jsonBody(t, map[string]any{"input": in, "plan": inv.Suggest}), nil)
	var set onboard.DraftSet
	if code != 201 || json.Unmarshal(body, &set) != nil || len(set.Tasks) != 2 {
		t.Fatalf("draft %d %s", code, body)
	}
	code, body = c.do(t, "GET", "/onboard/drafts", nil, nil)
	if code != 200 || !strings.Contains(string(body), set.ID) {
		t.Fatalf("list %d %s", code, body)
	}
	code, body = c.do(t, "PATCH", "/onboard/drafts/"+set.ID+"/tasks/"+set.Tasks[0].ID, []byte(`{"review":"rejected"}`), nil)
	if code != 200 || !strings.Contains(string(body), `"rejected"`) {
		t.Fatalf("edit %d %s", code, body)
	}
	// Nothing is ready yet: publish refuses before a job is queued.
	code, body = c.do(t, "POST", "/onboard/drafts/"+set.ID+"/publish", nil, nil)
	if code != 409 || len(fj.created) != 0 {
		t.Fatalf("publish unready %d %s", code, body)
	}
	code, body = c.do(t, "POST", "/onboard/drafts/"+set.ID+"/ready", nil, nil)
	if code != 202 || len(fj.created) != 1 || fj.created[0].Kind != OnboardReadyKind {
		t.Fatalf("ready %d %s %v", code, body, fj.created)
	}
	code, body = c.do(t, "GET", "/onboard/drafts/"+set.ID, nil, nil)
	if code != 200 || !strings.Contains(string(body), `"status":"queued"`) {
		t.Fatalf("get %d %s", code, body)
	}
	code, _ = c.do(t, "POST", "/onboard/drafts/"+set.ID+"/ready", nil, nil)
	if code != 409 {
		t.Errorf("second ready %d", code)
	}
	code, _ = c.do(t, "POST", "/onboard/drafts/"+set.ID+"/publish", []byte(`{"include_unready":true}`), nil)
	if code != 202 || fj.created[len(fj.created)-1].Kind != OnboardPublishKind {
		t.Errorf("publish %d", code)
	}
	code, _ = c.do(t, "GET", "/onboard/drafts/nope-nope", nil, nil)
	if code != 404 {
		t.Errorf("missing draft %d", code)
	}
	code, _ = c.do(t, "POST", "/onboard/detect", []byte(`{"input":{}}`), nil)
	if code != 422 && code != 400 {
		t.Errorf("empty input %d", code)
	}
	code, _ = c.do(t, "DELETE", "/onboard/drafts/"+set.ID, nil, nil)
	if code != 204 {
		t.Errorf("delete %d", code)
	}
}
