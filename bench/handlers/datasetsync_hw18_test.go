package handlers

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// newDatasetSyncClient serves the suite authoring, build, scenario rows and
// dataset API areas over one project folder.
func newDatasetSyncClient(t *testing.T) *authoringClient {
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
	areas := []func(OrgEnv, *router.Router){orgAreas["suiteauthoring"], orgAreas["suitebuild"], orgAreas["scenariorows"], orgAreas["datasetapi"]}
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

// latestVersion reads GET /suites/{id}/dataset/versions and returns the
// newest version and how many there are.
func latestVersion(t *testing.T, c *authoringClient, id string) (DatasetVersionView, int) {
	t.Helper()
	code, body := c.do(t, "GET", "/suites/"+id+"/dataset/versions", nil, nil)
	var out DatasetVersionList
	if code != 200 || json.Unmarshal(body, &out) != nil {
		t.Fatalf("versions: %d %s", code, body)
	}
	if len(out.Versions) == 0 {
		t.Fatalf("%s has no dataset version", id)
	}
	return out.Versions[0], len(out.Versions)
}

// Every path that adds or changes tasks records a dataset version with its
// source (fix wave 3, N268).
func TestDatasetVersionAfterRowsImport(t *testing.T) {
	c := newDatasetSyncClient(t)
	newQASuite(t, c, "zz-test-qa")
	req := RowImportRequest{Format: "jsonl", Data: "{\"question\":\"What is 2+2?\",\"answer\":\"4\"}\n{\"question\":\"Capital of France?\",\"answer\":\"Paris\"}\n"}
	if code, body := c.do(t, "POST", "/suites/zz-test-qa/rows", jsonBody(t, req), nil); code != 201 {
		t.Fatalf("import: %d %s", code, body)
	}
	v, n := latestVersion(t, c, "zz-test-qa")
	if n != 1 || v.N != 1 || v.Source != "Rows import" || len(v.Added) != 2 {
		t.Fatalf("version after rows import: %+v (of %d)", v.Version, n)
	}
}

func TestDatasetVersionAfterScenarioRows(t *testing.T) {
	c := newDatasetSyncClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"desk","template":"support"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	rows := `{"id":"dup-charge","opening_message":"I was charged twice for B-3001","persona_goal":"Get a refund","expected_actions":[{"tool":"issue_refund","args":{"order_id":"B-3001","amount_pence":4500,"reason":"damaged"}}],"forbidden_actions":["apply_credit"]}
`
	req := ScenarioRowsRequest{Format: "jsonl", Data: rows, Source: "tickets.jsonl", SwitchGrader: true}
	if code, body := c.do(t, "POST", "/suites/desk/scenario-rows", jsonBody(t, req), nil); code != 201 {
		t.Fatalf("scenario rows: %d %s", code, body)
	}
	v, _ := latestVersion(t, c, "desk")
	if v.Source != "Scenario rows" || len(v.Added) == 0 {
		t.Fatalf("version after scenario rows: %+v", v.Version)
	}
}

func TestDatasetVersionAfterFileEditor(t *testing.T) {
	c := newDatasetSyncClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"my-suite","template":"python-pytest"}`), nil); code != 201 {
		t.Fatalf("create suite: %d %s", code, body)
	}
	if code, body := c.do(t, "POST", "/suites/my-suite/tasks", []byte(`{"id":"t01","title":"Fix add"}`), nil); code != 201 {
		t.Fatalf("create task: %d %s", code, body)
	}
	v, _ := latestVersion(t, c, "my-suite")
	if v.Source != "File editor" || len(v.Added) != 1 || v.Added[0] != "t01" {
		t.Fatalf("version after New task: %+v", v.Version)
	}
	if code, body := c.do(t, "PUT", "/suites/my-suite/files/tasks/t01/instruction.md", []byte("Make add handle negative numbers.\n"), nil); code != 200 {
		t.Fatalf("write instruction: %d %s", code, body)
	}
	v, n := latestVersion(t, c, "my-suite")
	if n != 2 || v.Source != "File editor" || len(v.Updated) != 1 || v.Updated[0] != "t01" {
		t.Fatalf("version after the editor saved an instruction: %+v (of %d)", v.Version, n)
	}
	// A file outside suite.yaml and tasks/ records nothing.
	if code, body := c.do(t, "PUT", "/suites/my-suite/files/README.md", []byte("notes\n"), nil); code != 200 {
		t.Fatalf("write readme: %d %s", code, body)
	}
	if _, n := latestVersion(t, c, "my-suite"); n != 2 {
		t.Fatalf("README edit recorded a version: %d versions", n)
	}
}
