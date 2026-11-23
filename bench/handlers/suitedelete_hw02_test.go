package handlers

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// Fix wave 3, N288 (benchmark delete) and N265 (create refuses a taken id).

// hw02Suites is a disk suite source with some ids marked built-in.
type hw02Suites struct {
	diskSuites
	builtin map[string]bool
}

func (s hw02Suites) SuiteBuiltin(id string) bool { return s.builtin[id] }

// suiteDeleteClient serves the suite authoring routes over a fresh project
// whose index holds idx (nil: empty).
func suiteDeleteClient(t *testing.T, idx ReadIndex) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if idx == nil {
		idx = &fakeIndex{manifests: map[string]*schemas.Manifest{}}
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Projects: ps, Index: idx,
			Suites: hw02Suites{diskSuites: diskSuites{root: filepath.Join(root, "benchmarks")}, builtin: map[string]bool{"coding-core": true}}},
		Write:  lib.RequireWriteToken(testMode, testToken),
		Common: []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload: []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	area := orgAreas["suiteauthoring"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

func createTestSuite(t *testing.T, c *authoringClient, id string) {
	t.Helper()
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"`+id+`","template":"qa","blank":true}`), nil); code != 201 {
		t.Fatalf("create %s: %d %s", id, code, body)
	}
}

func TestDeleteSuiteRemovesFolderKeepsRuns(t *testing.T) {
	idx := testIndex() // e_test used zz-del; its runs are sealed (not running)
	idx.manifests[testExp].SuiteID = "zz-del"
	c := suiteDeleteClient(t, idx)
	createTestSuite(t, c, "zz-del")
	if err := os.MkdirAll(filepath.Join(c.root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.root, "experiments", "keep.yaml"), []byte("name: keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := evalstore.Open(c.root).Save(&schemas.Evaluation{ID: "ev_done", Status: schemas.EvaluationDone, Spec: schemas.EvaluationSpec{Benchmarks: []string{"zz-del"}}}); err != nil {
		t.Fatal(err)
	}
	code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil)
	if code != 204 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "zz-del")); !os.IsNotExist(err) {
		t.Fatalf("benchmark folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.root, "experiments", "keep.yaml")); err != nil {
		t.Fatalf("experiments touched: %v", err)
	}
	if ev, err := evalstore.Open(c.root).Load("ev_done"); err != nil || ev == nil {
		t.Fatalf("finished evaluation gone: %v", err)
	}
	if code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil); code != 404 || !strings.Contains(string(body), "Benchmark zz-del not found.") {
		t.Fatalf("second delete: %d %s", code, body)
	}
}

func TestDeleteSuiteRefusesBuiltin(t *testing.T) {
	c := suiteDeleteClient(t, nil)
	code, body := c.do(t, "DELETE", "/suites/coding-core", nil, nil)
	if code != 409 || !strings.Contains(string(body), "coding-core is a built-in benchmark and cannot be deleted.") {
		t.Fatalf("built-in delete: %d %s", code, body)
	}
}

func TestDeleteSuiteNeedsWriteToken(t *testing.T) {
	c := suiteDeleteClient(t, nil)
	createTestSuite(t, c, "zz-del")
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://127.0.0.1" + APIPrefix + "/suites/zz-del")
	req.Header.SetMethod("DELETE")
	resp := &fasthttp.Response{}
	if err := c.client.DoTimeout(req, resp, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != 401 && resp.StatusCode() != 403 {
		t.Fatalf("delete without token: %d", resp.StatusCode())
	}
}

func TestDeleteSuiteRefusesRunningExperiment(t *testing.T) {
	idx := testIndex()
	idx.manifests[testExp].SuiteID = "zz-del"
	idx.runs[0].Sealed = false // one run still going
	c := suiteDeleteClient(t, idx)
	createTestSuite(t, c, "zz-del")
	code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil)
	want := "zz-del has comparisons or evaluations still running. Stop them first, then delete it."
	if code != 409 || !strings.Contains(string(body), want) {
		t.Fatalf("running comparison: %d %s", code, body)
	}

	// A running evaluation that lists the benchmark refuses too.
	c = suiteDeleteClient(t, nil)
	createTestSuite(t, c, "zz-del")
	if err := evalstore.Open(c.root).Save(&schemas.Evaluation{ID: "ev_run", Status: schemas.EvaluationRunning, Spec: schemas.EvaluationSpec{Benchmarks: []string{"zz-del"}}}); err != nil {
		t.Fatal(err)
	}
	if code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil); code != 409 || !strings.Contains(string(body), want) {
		t.Fatalf("running evaluation: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "zz-del", "suite.yaml")); err != nil {
		t.Fatalf("refused delete removed files: %v", err)
	}
}

func TestDeleteSuiteRefusesScheduled(t *testing.T) {
	c := suiteDeleteClient(t, nil)
	createTestSuite(t, c, "zz-del")
	if err := evalstore.Open(c.root).Save(&schemas.Evaluation{ID: "ev_sched", Status: schemas.EvaluationDone, Spec: schemas.EvaluationSpec{Benchmarks: []string{"zz-del"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(c.root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	sched := "schedules:\n  - name: nightly\n    evaluation: ev_sched\n    cron: \"0 3 * * *\"\n"
	if err := os.WriteFile(filepath.Join(c.root, "policies", "schedules.yaml"), []byte(sched), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil)
	if code != 409 || !strings.Contains(string(body), "Schedule nightly repeats an evaluation that uses zz-del. Remove or change the schedule first.") {
		t.Fatalf("scheduled: %d %s", code, body)
	}
}

// TestRunPageAfterSuiteDeleted: the run, comparison and grid pages of
// a comparison whose benchmark was deleted still open.
func TestRunPageAfterSuiteDeleted(t *testing.T) {
	idx := testIndex()
	idx.manifests[testExp].SuiteID = "zz-del"
	a := suiteDeleteClient(t, idx)
	createTestSuite(t, a, "zz-del")
	if code, body := a.do(t, "DELETE", "/suites/zz-del", nil, nil); code != 204 {
		t.Fatalf("delete: %d %s", code, body)
	}
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	for _, p := range []string{"/runs/r_t01_A", "/experiments/" + testExp, "/experiments/" + testExp + "/grid"} {
		if resp := c.do(t, "GET", APIPrefix+p, nil, nil); resp.StatusCode() != 200 {
			t.Fatalf("GET %s after the benchmark was deleted: %d %s", p, resp.StatusCode(), resp.Body())
		}
	}
}

func TestCreateSuiteRefusesTakenID(t *testing.T) {
	c := suiteDeleteClient(t, nil)
	createTestSuite(t, c, "zz-taken")
	code, body := c.do(t, "POST", "/suites", []byte(`{"id":"zz-taken","template":"qa","blank":true}`), nil)
	if code != 409 || !strings.Contains(string(body), "A benchmark named zz-taken already exists. Pick another id.") {
		t.Fatalf("taken id: %d %s", code, body)
	}
	code, body = c.do(t, "POST", "/suites", []byte(`{"id":"coding-core","template":"qa","blank":true}`), nil)
	if code != 409 || !strings.Contains(string(body), "coding-core is a built-in benchmark. Pick another id.") {
		t.Fatalf("built-in id: %d %s", code, body)
	}
}
