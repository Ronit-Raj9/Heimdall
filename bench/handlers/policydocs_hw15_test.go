package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// hw15PolicyClient is newPolicyClient with a key store and the hosted flag,
// so the review route's key check can be tested (fix wave 3, N276).
func hw15PolicyClient(t *testing.T, ks keystore.Store, hosted bool) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{OrgID: "org1", Hosted: hosted, Keys: ks, Projects: ps, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}},
		Write:    lib.RequireWriteToken(testMode, testToken),
		Common:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)},
		Upload:   []lib.Middleware{lib.BodyLimit(testMode, lib.MaxUploadBytes)},
	}
	orgAreasMu.Lock()
	areas := []func(OrgEnv, *router.Router){orgAreas["suiteauthoring"], orgAreas["policydocs"]}
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

// hw15DraftedDesk makes a Support template benchmark (grader verifier_command,
// no judge) with clause 2 of a policy drafted, and returns the doc id and the
// drafted task ids.
func hw15DraftedDesk(t *testing.T, c *authoringClient) (string, []string) {
	t.Helper()
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"desk","template":"support"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	up := PolicyDocUpload{Name: "acme-policy.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte(testPolicy))}
	code, body := c.do(t, "POST", "/suites/desk/policy-docs", jsonBody(t, up), nil)
	var v PolicyDocView
	if code != 201 || json.Unmarshal(body, &v) != nil {
		t.Fatalf("upload: %d %s", code, body)
	}
	dir := filepath.Join(c.root, "benchmarks", "desk")
	d, err := policybench.Load(dir, v.Doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	draftClause(t, dir, d, "2")
	return v.Doc.ID, []string{policybench.TaskID(v.Doc.ID, "2", policybench.KindNormal), policybench.TaskID(v.Doc.ID, "2", policybench.KindAdversarial)}
}

func TestPolicyAcceptWithoutJudgeIsPlain409(t *testing.T) {
	c := hw15PolicyClient(t, nil, false)
	doc, tasks := hw15DraftedDesk(t, c)
	code, body := c.do(t, "POST", "/suites/desk/policy-docs/"+doc+"/review", jsonBody(t, PolicyReviewRequest{Accept: tasks[:1]}), nil)
	if code != 409 {
		t.Fatalf("accept without judge: %d %s", code, body)
	}
	s := string(body)
	if strings.Contains(s, "switch_grader") || !strings.Contains(s, "Pick a reply judge before accepting") {
		t.Fatalf("409 text is not plain: %s", s)
	}
}

func TestPolicyAcceptSwitchesGraderWithJudge(t *testing.T) {
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-test-123456789"}, "t"); err != nil {
		t.Fatal(err)
	}
	c := hw15PolicyClient(t, ks, true)
	doc, tasks := hw15DraftedDesk(t, c)
	req := PolicyReviewRequest{Accept: tasks[:1], SwitchGrader: true, Judge: &scaffold.JudgeConfig{Provider: "openai", Model: "x"}}
	code, body := c.do(t, "POST", "/suites/desk/policy-docs/"+doc+"/review", jsonBody(t, req), nil)
	var rr PolicyReviewResult
	if code != 200 || json.Unmarshal(body, &rr) != nil || len(rr.Accepted) != 1 || !rr.GraderChanged {
		t.Fatalf("accept with judge: %d %s", code, body)
	}
	raw, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "desk", "suite.yaml"))
	y := string(raw)
	if !strings.Contains(y, "grader: checks") || !strings.Contains(y, "grading: judged") || !strings.Contains(y, tasks[0]) {
		t.Fatalf("suite.yaml:\n%s", y)
	}
}

func TestPolicyAcceptRefusesProviderWithoutKey(t *testing.T) {
	c := hw15PolicyClient(t, keystore.NewMemory(), true)
	doc, tasks := hw15DraftedDesk(t, c)
	req := PolicyReviewRequest{Accept: tasks[:1], SwitchGrader: true, Judge: &scaffold.JudgeConfig{Provider: "openai", Model: "x"}}
	code, body := c.do(t, "POST", "/suites/desk/policy-docs/"+doc+"/review", jsonBody(t, req), nil)
	if code != 422 || !strings.Contains(string(body), "No working openai key in this organization. Add one under Models, then accept again.") {
		t.Fatalf("accept with a keyless judge: %d %s", code, body)
	}
	raw, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "desk", "suite.yaml"))
	if strings.Contains(string(raw), tasks[0]) || strings.Contains(string(raw), "grader: checks") {
		t.Fatalf("a refused accept changed suite.yaml:\n%s", raw)
	}
}
