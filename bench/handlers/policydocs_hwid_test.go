package handlers

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// TestPolicyAcceptRecordsDatasetVersion: accepting policy drafts adds tasks
// to suite.yaml, so the Data tab gains a version named after the policy
// document (fix wave 3, N268, applied in review).
func TestPolicyAcceptRecordsDatasetVersion(t *testing.T) {
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-test-123456789"}, "t"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{OrgID: "org1", Hosted: true, Keys: ks, Projects: ps, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	areas := []func(OrgEnv, *router.Router){orgAreas["suiteauthoring"], orgAreas["policydocs"], orgAreas["datasetapi"]}
	orgAreasMu.Unlock()
	for _, a := range areas {
		a(env, r)
	}
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: lib.MaxUploadBytes}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	c := &authoringClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, root: root}

	doc, tasks := hw15DraftedDesk(t, c)
	versions := func() DatasetVersionList {
		code, body := c.do(t, "GET", "/suites/desk/dataset/versions", nil, nil)
		var out DatasetVersionList
		if code != 200 || json.Unmarshal(body, &out) != nil {
			t.Fatalf("versions: %d %s", code, body)
		}
		return out
	}
	before := len(versions().Versions)
	req := PolicyReviewRequest{Accept: tasks[:1], SwitchGrader: true, Judge: &scaffold.JudgeConfig{Provider: "openai", Model: "x"}}
	if code, body := c.do(t, "POST", "/suites/desk/policy-docs/"+doc+"/review", jsonBody(t, req), nil); code != 200 {
		t.Fatalf("accept: %d %s", code, body)
	}
	after := versions()
	if len(after.Versions) != before+1 {
		t.Fatalf("accept recorded %d versions, want 1 (had %d)", len(after.Versions)-before, before)
	}
	v := after.Versions[0]
	if v.Source != "Policy document "+doc {
		t.Fatalf("version source %q, want %q", v.Source, "Policy document "+doc)
	}
	found := false
	for _, a := range v.Added {
		found = found || a == tasks[0]
	}
	if !found {
		t.Fatalf("version does not list the accepted task %s: %+v", tasks[0], v.Version)
	}
}
