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
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskgates"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
	"gopkg.in/yaml.v3"
)

// newGatesClient serves suite authoring, suite build and the authoring
// helper over one project folder.
func newGatesClient(t *testing.T) *authoringClient {
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
	areas := []func(OrgEnv, *router.Router){orgAreas["suiteauthoring"], orgAreas["suitebuild"], orgAreas["authoringgates"]}
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

func TestAuthoringGatesEndToEnd(t *testing.T) {
	c := newGatesClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"faq","template":"qa","blank":true}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	var rows strings.Builder
	rows.WriteString("question,answer\n")
	for _, q := range []string{
		"How many days is the refund window for annual plans bought through the web store?",
		"How many days is the refund window for annual plans bought through the web store",
		"Which plan includes single sign-on and audit logs for every workspace?",
		"What is the maximum file size an agent can upload in one request?",
		"Who approves a refund above five hundred dollars for an enterprise customer?",
	} {
		rows.WriteString(q + ",42\n")
	}
	code, body := c.do(t, "POST", "/suites/faq/rows", jsonBody(t, RowImportRequest{Format: "csv", Data: rows.String(), Source: "faq.csv"}), nil)
	var imp RowImportResult
	if code != 201 || json.Unmarshal(body, &imp) != nil || len(imp.Tasks) != 5 {
		t.Fatalf("import: %d %s", code, body)
	}
	ids := []string{}
	for _, tk := range imp.Tasks {
		ids = append(ids, tk.ID)
	}

	// quality: the two refund rows are duplicates
	code, body = c.do(t, "GET", "/suites/faq/quality", nil, nil)
	var rep taskgates.Report
	if code != 200 || json.Unmarshal(body, &rep) != nil || rep.Tasks != 5 || len(rep.Duplicates) != 1 || rep.Duplicates[0].Kind != taskgates.DupExact {
		t.Fatalf("quality: %d %s", code, body)
	}
	if code, _ := c.do(t, "GET", "/suites/faq/quality?threshold=2", nil, nil); code != 400 {
		t.Fatalf("bad threshold: %d", code)
	}

	// review: nothing generated yet; mark one task generated, as the assist does
	code, body = c.do(t, "GET", "/suites/faq/generated", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"tasks":[]`) {
		t.Fatalf("generated empty: %d %s", code, body)
	}
	gen := ids[2]
	ty := filepath.Join(c.root, "benchmarks", "faq", "tasks", gen, "task.yaml")
	raw, _ := os.ReadFile(ty)
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["review"] = "pending"
	doc["provenance"].(map[string]any)["generated"] = []map[string]any{{"field": "answer.wrong_answers[0]", "op": "near_miss_wrong", "model": "openai/gpt-test", "prompt_sha": "abc", "cost_usd": 0.01}}
	raw, _ = yaml.Marshal(doc)
	if err := os.WriteFile(ty, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, body = c.do(t, "GET", "/suites/faq/generated", nil, nil)
	var list GeneratedTaskList
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Tasks) != 1 || list.Pending != 1 || list.Tasks[0].Review != schemas.ReviewPending || list.Tasks[0].CostUSD != 0.01 {
		t.Fatalf("generated: %d %s\n%s", code, body, raw)
	}
	if code, body := c.do(t, "PUT", "/suites/faq/tasks/"+gen+"/review-state", []byte(`{"decision":"rejected"}`), nil); code != 422 || !strings.Contains(string(body), "needs a note") {
		t.Fatalf("reject without note: %d %s", code, body)
	}
	if code, body := c.do(t, "PUT", "/suites/faq/tasks/"+ids[0]+"/review-state", []byte(`{"decision":"accepted"}`), nil); code != 422 || !strings.Contains(string(body), "needs no review") {
		t.Fatalf("hand-written task: %d %s", code, body)
	}
	if code, body := c.do(t, "PUT", "/suites/faq/tasks/"+gen+"/review-state", []byte(`{"decision":"accepted","note":"proof looked right"}`), nil); code != 200 || !strings.Contains(string(body), `"review":"accepted"`) {
		t.Fatalf("accept: %d %s", code, body)
	}
	code, body = c.do(t, "GET", "/suites/faq/generated", nil, nil)
	list = GeneratedTaskList{}
	if code != 200 || json.Unmarshal(body, &list) != nil || list.Accepted != 1 || list.Tasks[0].Decision == nil || list.Tasks[0].Decision.Note != "proof looked right" {
		t.Fatalf("after accept: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "faq", schemas.TaskReviewFileName)); err != nil {
		t.Fatalf("review sidecar: %v", err)
	}

	// held-out: dry run writes nothing, then the split stamps the canary
	code, body = c.do(t, "POST", "/suites/faq/held-out", []byte(`{"held_out_pct":40,"seed":11,"dry_run":true}`), nil)
	var split HeldOutSplitResult
	if code != 200 || json.Unmarshal(body, &split) != nil || split.HeldOut != 2 || !split.DryRun || len(split.Written) != 0 {
		t.Fatalf("dry run: %d %s", code, body)
	}
	suiteYAML, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "faq", "suite.yaml"))
	if utils.SuiteYAMLCanary(suiteYAML) != "" {
		t.Fatal("dry run wrote the canary")
	}
	code, body = c.do(t, "POST", "/suites/faq/held-out", []byte(`{"held_out_pct":40,"seed":11}`), nil)
	split = HeldOutSplitResult{}
	if code != 200 || json.Unmarshal(body, &split) != nil || len(split.Written) != 5 || !utils.ValidCanary(split.Canary) {
		t.Fatalf("split: %d %s", code, body)
	}
	s, err := diskSuites{root: filepath.Join(c.root, "benchmarks")}.LoadSuite(context.Background(), "faq")
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, tk := range s.Tasks {
		if tk.Split == schemas.SplitHeldOut {
			held++
			in, _ := os.ReadFile(filepath.Join(tk.Dir, tk.Instruction))
			if !strings.Contains(string(in), split.Canary) {
				t.Fatalf("held-out %s not stamped", tk.ID)
			}
		}
	}
	if held != 2 {
		t.Fatalf("held out %d", held)
	}
	if code, body := c.do(t, "POST", "/suites/faq/held-out", []byte(`{"held_out_pct":95}`), nil); code != 422 {
		t.Fatalf("bad plan: %d %s", code, body)
	}
	if code, _ := c.do(t, "GET", "/suites/nope/generated", nil, nil); code != 404 {
		t.Fatalf("unknown suite: %d", code)
	}
}
