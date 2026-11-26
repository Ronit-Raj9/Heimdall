package handlers

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// stampDiskSuites is diskSuites that also reports the folder of each suite,
// so forgetSuite can find the cache slot to drop.
type stampDiskSuites struct{ diskSuites }

func (d stampDiskSuites) SuiteStamp(id string) (string, string) {
	return filepath.Join(d.root, id), "stamp"
}

func (d stampDiskSuites) SuiteHead(id string) (*schemas.Suite, error) {
	return d.LoadSuite(context.Background(), id)
}

// newStampBuildClient is newBuildClient over stampDiskSuites.
func newStampBuildClient(t *testing.T) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Projects: ps, Suites: stampDiskSuites{diskSuites{root: filepath.Join(root, "benchmarks")}}},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	areas := []func(OrgEnv, *router.Router){orgAreas["suiteauthoring"], orgAreas["suitebuild"]}
	orgAreasMu.Unlock()
	for _, a := range areas {
		a(env, r)
	}
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

// seedSummarySlot puts a cached summary for the suite folder and reports a
// function that tells whether it is still there.
func seedSummarySlot(t *testing.T, c *authoringClient, id string) func() bool {
	t.Helper()
	dir := filepath.Join(c.root, "benchmarks", id)
	suiteSummaries.mu.Lock()
	suiteSummaries.slots[dir] = &summarySlot{have: true, stamp: "old"}
	suiteSummaries.mu.Unlock()
	t.Cleanup(func() { forgetSuiteDir(dir) })
	return func() bool {
		suiteSummaries.mu.Lock()
		defer suiteSummaries.mu.Unlock()
		_, ok := suiteSummaries.slots[dir]
		return ok
	}
}

func createBlankQA(t *testing.T, c *authoringClient, id string) {
	t.Helper()
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"`+id+`","template":"qa","blank":true}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
}

func TestSetGraderForgetsSuiteCache(t *testing.T) {
	c := newStampBuildClient(t)
	createBlankQA(t, c, "grader-cache")
	cached := seedSummarySlot(t, c, "grader-cache")
	body := []byte(`{"grader":"judge","judge":{"provider":"openai","model":"gpt-4o-mini"}}`)
	if code, out := c.do(t, "PUT", "/suites/grader-cache/grader", body, nil); code != 200 {
		t.Fatalf("set grader: %d %s", code, out)
	}
	if cached() {
		t.Fatal("the cached summary is still served after the grader changed")
	}
}

func TestImportRowsForgetsSuiteCache(t *testing.T) {
	c := newStampBuildClient(t)
	createBlankQA(t, c, "rows-cache")
	cached := seedSummarySlot(t, c, "rows-cache")
	req := RowImportRequest{Format: "csv", Data: "question,answer,wrong_answers\nHow many days?,30,14\n", Source: "faq.csv"}
	if code, out := c.do(t, "POST", "/suites/rows-cache/rows", jsonBody(t, req), nil); code != 201 {
		t.Fatalf("import: %d %s", code, out)
	}
	if cached() {
		t.Fatal("the cached summary is still served after rows were imported")
	}
}

func TestWriteFileInvalidYAMLPlain(t *testing.T) {
	c := newStampBuildClient(t)
	createBlankQA(t, c, "bad-yaml")
	code, out := c.do(t, "PUT", "/suites/bad-yaml/files/notes.yaml", []byte("a: [1, 2\n"), nil)
	if code != 422 {
		t.Fatalf("write: %d %s", code, out)
	}
	s := string(out)
	if !strings.Contains(s, "Could not save notes.yaml: ") || strings.Contains(s, "projectfs:") || strings.Contains(s, "benchmarks/") || strings.Contains(s, "yaml: yaml") {
		t.Fatalf("message is not plain: %s", s)
	}
}

func TestFilePathUnescaped(t *testing.T) {
	c := newStampBuildClient(t)
	createBlankQA(t, c, "odd-names")
	if code, out := c.do(t, "PUT", "/suites/odd-names/files/notes%20one.txt", []byte("hi\n"), nil); code/100 != 2 {
		t.Fatalf("write: %d %s", code, out)
	}
	code, out := c.do(t, "GET", "/suites/odd-names/files", nil, nil)
	if code != 200 || !strings.Contains(string(out), `"notes one.txt"`) || strings.Contains(string(out), "%20") {
		t.Fatalf("list: %d %s", code, out)
	}
}

func TestHeldOutTextsPlain(t *testing.T) {
	var ctx fasthttp.RequestCtx
	if !refuseHeldOutBoard(&ctx, testMode, false, schemas.SplitHeldOut) {
		t.Fatal("board not refused")
	}
	if s := string(ctx.Response.Body()); !strings.Contains(s, "The held-out board stays private.") || strings.Contains(s, "held_out.visible") {
		t.Fatalf("board text: %s", s)
	}
	var ctx2 fasthttp.RequestCtx
	h := &SuitesHandler{mode: testMode}
	if !h.refuseHeldOut(&ctx2, "faq", &schemas.Task{ID: "t1", Split: schemas.SplitHeldOut}) {
		t.Fatal("task not refused")
	}
	if s := string(ctx2.Response.Body()); !strings.Contains(s, "This task is held out: its instruction and files stay private.") || strings.Contains(s, "held_out.visible") {
		t.Fatalf("task text: %s", s)
	}
}
