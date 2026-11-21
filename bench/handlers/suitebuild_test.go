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
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// newBuildClient serves the suite authoring and suite build areas over one project folder.
func newBuildClient(t *testing.T) *authoringClient {
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

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lintErrors loads the suite from disk and returns its lint errors.
func lintErrors(t *testing.T, c *authoringClient, id string) []string {
	t.Helper()
	s, err := diskSuites{root: filepath.Join(c.root, "benchmarks")}.LoadSuite(context.Background(), id)
	if err != nil {
		t.Fatalf("load %s: %v", id, err)
	}
	var out []string
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			out = append(out, p.Path+": "+p.Message)
		}
	}
	return out
}

func TestSuiteBuildRowsGraderSplit(t *testing.T) {
	c := newBuildClient(t)
	code, body := c.do(t, "POST", "/suites", []byte(`{"id":"support-faq","template":"qa","blank":true}`), nil)
	if code != 201 || strings.Contains(string(body), `"sample"`) {
		t.Fatalf("create blank: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "support-faq", "tasks", "hotel-cap")); err == nil {
		t.Fatal("blank suite kept the sample task")
	}

	csv := "question,answer,wrong_answers,split\n" +
		"How many days is the refund window?,30,14,\n" +
		"Which plan includes SSO?,Enterprise,Team,held_out\n" +
		"Rubric only,,,\n"
	req := RowImportRequest{Format: "csv", Data: csv, DryRun: true, Source: "faq.csv"}
	code, body = c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, req), nil)
	var res RowImportResult
	if code != 200 || json.Unmarshal(body, &res) != nil || !res.DryRun || len(res.Tasks) != 2 || len(res.Problems) != 1 {
		t.Fatalf("dry run: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "support-faq", "tasks", res.Tasks[0].ID)); err == nil {
		t.Fatal("dry run wrote a task")
	}

	req.DryRun = false
	code, body = c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, req), nil)
	res = RowImportResult{}
	if code != 201 || json.Unmarshal(body, &res) != nil || len(res.Tasks) != 2 || res.Grader != "answer_match" {
		t.Fatalf("import: %d %s", code, body)
	}
	if errs := lintErrors(t, c, "support-faq"); len(errs) > 0 {
		t.Fatalf("imported tasks do not lint clean:\n%s", strings.Join(errs, "\n"))
	}
	s, err := diskSuites{root: filepath.Join(c.root, "benchmarks")}.LoadSuite(context.Background(), "support-faq")
	if err != nil || len(s.Tasks) != 2 {
		t.Fatalf("loaded %v tasks: %v", s, err)
	}
	heldOut := res.Tasks[1].ID
	if s.TaskByID(heldOut).Split != "held_out" {
		t.Fatalf("task %s split = %q", heldOut, s.TaskByID(heldOut).Split)
	}

	// The same rows again: slugs get a suffix, nothing is overwritten.
	code, body = c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, req), nil)
	if code != 201 || !strings.Contains(string(body), res.Tasks[0].ID+"-2") {
		t.Fatalf("second import: %d %s", code, body)
	}

	// Split: public again, then held out.
	code, body = c.do(t, "PUT", "/suites/support-faq/tasks/"+heldOut+"/split", []byte(`{"held_out":false}`), nil)
	if code != 200 || !strings.Contains(string(body), `"split":"public"`) {
		t.Fatalf("split public: %d %s", code, body)
	}
	ty, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "support-faq", "tasks", heldOut, "task.yaml"))
	if strings.Contains(string(ty), "split:") {
		t.Fatalf("split kept:\n%s", ty)
	}
	if code, _ := c.do(t, "PUT", "/suites/support-faq/tasks/nope/split", []byte(`{"held_out":true}`), nil); code != 404 {
		t.Fatalf("unknown task split: %d", code)
	}

	// Tickets under the exact grader are refused row by row.
	tickets := `[{"subject":"Charged twice","body":"Billed twice in May","resolution":"Apologise and refund the duplicate charge"}]`
	code, body = c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, RowImportRequest{Format: "tickets", Data: tickets}), nil)
	if code != 200 || !strings.Contains(string(body), "rubric judge") {
		t.Fatalf("tickets under exact: %d %s", code, body)
	}

	// Grader: rubric judge needs a model; then pin one.
	if code, _ := c.do(t, "PUT", "/suites/support-faq/grader", []byte(`{"grader":"judge"}`), nil); code != 400 {
		t.Fatalf("judge without model: %d", code)
	}
	code, body = c.do(t, "PUT", "/suites/support-faq/grader", []byte(`{"grader":"judge","judge":{"provider":"openai","model":"gpt-4.1-mini"}}`), nil)
	if code != 200 || !strings.Contains(string(body), `"grading":"judged"`) || !strings.Contains(string(body), `"prompt_version":"qa-v1"`) {
		t.Fatalf("pin judge: %d %s", code, body)
	}
	code, body = c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, RowImportRequest{Format: "tickets", Data: tickets}), nil)
	if code != 201 || !strings.Contains(string(body), `"grader":"judge"`) {
		t.Fatalf("tickets under judge: %d %s", code, body)
	}
	if errs := lintErrors(t, c, "support-faq"); len(errs) > 0 {
		t.Fatalf("judged suite does not lint clean:\n%s", strings.Join(errs, "\n"))
	}
	if code, body := c.do(t, "PUT", "/suites/support-faq/grader", []byte(`{"grader":"answer_match"}`), nil); code != 200 || !strings.Contains(string(body), `"grading":"verified"`) {
		t.Fatalf("back to exact: %d %s", code, body)
	}

	// A code suite keeps its tests.
	if code, _ := c.do(t, "POST", "/suites", []byte(`{"id":"code","template":"python-pytest"}`), nil); code != 201 {
		t.Fatal("create code suite")
	}
	if code, body := c.do(t, "POST", "/suites/code/rows", jsonBody(t, RowImportRequest{Format: "csv", Data: csv}), nil); code != 422 {
		t.Fatalf("rows into a code suite: %d %s", code, body)
	}
	if code, _ := c.do(t, "PUT", "/suites/code/grader", []byte(`{"grader":"judge","judge":{"provider":"openai","model":"m"}}`), nil); code != 422 {
		t.Fatalf("grader of a code suite: %d", code)
	}

	// Bad input.
	if code, _ := c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, RowImportRequest{Format: "xlsx", Data: "a"}), nil); code != 400 {
		t.Fatalf("bad format: %d", code)
	}
	if code, _ := c.do(t, "POST", "/suites/support-faq/rows", []byte(`{"format":"csv","data":"a","extra":1}`), nil); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
	if code, _ := c.do(t, "POST", "/suites/nope/rows", jsonBody(t, RowImportRequest{Format: "csv", Data: csv}), nil); code != 404 {
		t.Fatalf("unknown suite: %d", code)
	}
	if code, _ := c.do(t, "POST", "/suites/support-faq/rows", jsonBody(t, req), map[string]string{"Authorization": "Bearer nope"}); code != 401 && code != 403 {
		t.Fatalf("unauthenticated import: %d", code)
	}
}
