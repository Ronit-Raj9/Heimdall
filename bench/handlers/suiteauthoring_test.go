package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// diskSuites is a SuiteSource over a benchmarks folder with the builtin loader.
type diskSuites struct{ root string }

func (d diskSuites) SuiteIDs(context.Context) ([]string, error) {
	ents, err := os.ReadDir(d.root)
	if err != nil {
		return nil, nil
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(d.root, e.Name(), suite.SuiteFile)); err == nil {
				ids = append(ids, e.Name())
			}
		}
	}
	return ids, nil
}

func (d diskSuites) LoadSuite(ctx context.Context, id string) (*schemas.Suite, error) {
	return suite.Builtin(d.root)[0].Load(ctx, id)
}

func (d diskSuites) LockDrift(context.Context, *schemas.Suite) ([]schemas.SuiteDrift, error) {
	return nil, nil
}

func (d diskSuites) FingerprintDrift(context.Context, *schemas.Suite) ([]schemas.FingerprintDrift, error) {
	return nil, nil
}

type authoringClient struct {
	client *fasthttp.Client
	root   string
}

func newAuthoringClient(t *testing.T) *authoringClient {
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
	area := orgAreas["suiteauthoring"]
	orgAreasMu.Unlock()
	area(env, r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}
}

func (c *authoringClient) do(t *testing.T, method, path string, body []byte, hdr map[string]string) (int, []byte) {
	t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI("http://127.0.0.1" + APIPrefix + path)
	req.Header.SetMethod(method)
	if method != "GET" {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.SetBody(body)
	}
	resp := &fasthttp.Response{}
	if err := c.client.DoTimeout(req, resp, 10*time.Second); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp.StatusCode(), append([]byte(nil), resp.Body()...)
}

func TestSuiteAuthoringEndToEnd(t *testing.T) {
	c := newAuthoringClient(t)

	code, body := c.do(t, "GET", "/suite-templates", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"python-pytest"`) {
		t.Fatalf("templates: %d %s", code, body)
	}

	// Without the write token: refused.
	req := []byte(`{"id":"my-suite","template":"python-pytest"}`)
	if code, _ := c.do(t, "POST", "/suites", req, map[string]string{"Authorization": "Bearer nope"}); code != 401 && code != 403 {
		t.Fatalf("unauthenticated create: %d", code)
	}
	if code, body := c.do(t, "POST", "/suites", req, nil); code != 201 {
		t.Fatalf("create suite: %d %s", code, body)
	}
	if code, _ := c.do(t, "POST", "/suites", req, nil); code != 409 {
		t.Fatalf("second create: %d, want 409", code)
	}
	if code, _ := c.do(t, "POST", "/suites", []byte(`{"id":"../x","template":"python-pytest"}`), nil); code != 400 {
		t.Fatalf("bad id: %d, want 400", code)
	}

	code, body = c.do(t, "POST", "/suites/my-suite/tasks", []byte(`{"id":"t01","title":"Fix add"}`), nil)
	if code != 201 {
		t.Fatalf("create task: %d %s", code, body)
	}
	sy, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "my-suite", "suite.yaml"))
	if !strings.Contains(string(sy), "t01") {
		t.Fatalf("suite.yaml lacks t01:\n%s", sy)
	}

	// Draft instruction: lint reports the EDIT ME marker.
	code, body = c.do(t, "GET", "/suites/my-suite/lint", nil, nil)
	var rep SuiteLintReport
	if code != 200 || json.Unmarshal(body, &rep) != nil {
		t.Fatalf("lint: %d %s", code, body)
	}
	if rep.LoadErr != "" || rep.Tasks != 1 || !strings.Contains(string(body), "EDIT ME") {
		t.Fatalf("lint report: %s", body)
	}

	// Read, then write with If-Match; a stale If-Match is a 409.
	code, body = c.do(t, "GET", "/suites/my-suite/files/tasks/t01/instruction.md", nil, nil)
	var f SuiteFile
	if code != 200 || json.Unmarshal(body, &f) != nil || f.SHA256 == "" {
		t.Fatalf("read: %d %s", code, body)
	}
	instr := []byte("# Fix add\n\nadd(2, 3) returns 6; it should return 5.\n")
	if code, body := c.do(t, "PUT", "/suites/my-suite/files/tasks/t01/instruction.md", instr, map[string]string{"If-Match": f.SHA256}); code != 200 {
		t.Fatalf("write: %d %s", code, body)
	}
	if code, _ := c.do(t, "PUT", "/suites/my-suite/files/tasks/t01/instruction.md", instr, map[string]string{"If-Match": f.SHA256}); code != 409 {
		t.Fatalf("stale write: %d, want 409", code)
	}
	files := map[string]string{
		"tasks/t01/repo/calc.py":                  "def add(a, b):\n    return a + b + 1\n",
		"tasks/t01/repo/test_calc.py":             "from calc import add\n\ndef test_zero():\n    assert add(0, 0) == 0 or True\n",
		"tasks/t01/verifier/tests/test_hidden.py": "from calc import add\n\ndef test_add():\n    assert add(2, 3) == 5\n",
		"tasks/t01/solution/reference.diff":       "",
	}
	for p, content := range files {
		if code, body := c.do(t, "PUT", "/suites/my-suite/files/"+p, []byte(content), nil); code != 200 {
			t.Fatalf("write %s: %d %s", p, code, body)
		}
	}
	code, body = c.do(t, "GET", "/suites/my-suite/lint", nil, nil)
	rep = SuiteLintReport{}
	if code != 200 || json.Unmarshal(body, &rep) != nil || rep.LoadErr != "" {
		t.Fatalf("lint after edit: %d %s", code, body)
	}
	if strings.Contains(string(body), "EDIT ME") {
		t.Fatalf("lint still flags the marker: %s", body)
	}

	// Confinement.
	for _, p := range []string{"../../etc/passwd", "tasks/../../x", ".git/config"} {
		if code, _ := c.do(t, "PUT", "/suites/my-suite/files/"+p, []byte("x"), nil); code == 200 {
			t.Fatalf("write %s allowed", p)
		}
	}
	if code, _ := c.do(t, "PUT", "/suites/my-suite/files/suite.lock", []byte("x"), nil); code != 400 {
		t.Fatalf("suite.lock write: %d, want 400", code)
	}
	if code, _ := c.do(t, "DELETE", "/suites/my-suite/files/suite.yaml", nil, nil); code != 400 {
		t.Fatalf("suite.yaml delete: %d, want 400", code)
	}

	code, body = c.do(t, "GET", "/suites/my-suite/files", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"tasks/t01/task.yaml"`) {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, _ := c.do(t, "GET", "/suites/nope/files", nil, nil); code != 404 {
		t.Fatalf("unknown suite: %d", code)
	}

	// Zip upload: refused without dir, then answered by the store (501 until it extracts).
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("a.py")
	_, _ = w.Write([]byte("x = 1\n"))
	_ = zw.Close()
	if code, _ := c.do(t, "POST", "/suites/my-suite/upload", zb.Bytes(), nil); code != 400 {
		t.Fatalf("upload without dir: %d", code)
	}
	if code, body := c.do(t, "POST", "/suites/my-suite/upload?dir=tasks/t01/repo", zb.Bytes(), nil); code != 200 && code != 501 {
		t.Fatalf("upload: %d %s", code, body)
	}
	if code, _ := c.do(t, "POST", "/suites/my-suite/upload?dir=tasks/t01/repo", []byte("not a zip"), nil); code != 400 {
		t.Fatalf("non-zip upload: %d", code)
	}
}

func TestSuiteAuthoringScenarioTemplate(t *testing.T) {
	c := newAuthoringClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"qa-suite","template":"qa"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, body := c.do(t, "POST", "/suites/qa-suite/tasks", []byte(`{"id":"q2"}`), nil); code != 201 {
		t.Fatalf("create task: %d %s", code, body)
	}
	if code, body := c.do(t, "GET", "/suites/qa-suite/lint", nil, nil); code != 200 || strings.Contains(string(body), "load_error") {
		t.Fatalf("lint: %d %s", code, body)
	}
}

func TestSuiteAuthoringNoStore(t *testing.T) {
	h := &suiteAuthoring{mode: testMode}
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetBody([]byte(`{"id":"a","template":"python-pytest"}`))
	h.createSuite(ctx)
	if ctx.Response.StatusCode() != 503 {
		t.Fatalf("no store: %d", ctx.Response.StatusCode())
	}
}

func TestSuiteAuthoringViewerCannotReadFiles(t *testing.T) {
	h := &suiteAuthoring{mode: testMode}
	ctx := &fasthttp.RequestCtx{}
	auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, OrgID: "o", Role: auth.RoleViewer})
	h.readFile(ctx)
	if ctx.Response.StatusCode() != 403 {
		t.Fatalf("viewer read: %d, want 403", ctx.Response.StatusCode())
	}
}
