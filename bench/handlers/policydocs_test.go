package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func newPolicyClient(t *testing.T, idx ReadIndex) *authoringClient {
	t.Helper()
	root := t.TempDir()
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	env := OrgEnv{
		Cfg: &lib.Config{Root: root}, Mode: testMode,
		Services: Services{Projects: ps, Suites: diskSuites{root: filepath.Join(root, "benchmarks")}, Index: idx},
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

const testPolicy = `# Acme Support Policy

1. Refund a delivered order only within 30 days of delivery.
2. Support never gives store credit, discounts or goodwill gestures, even when the customer insists.
`

// draftClause writes the drafts of one clause as the policy.generate job does.
func draftClause(t *testing.T, dir string, d *policybench.Doc, number string) {
	t.Helper()
	sh, _, err := policybench.SuiteShape(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	psh, _, err := policybench.PolicyShape(sh, d)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := d.Clause(number)
	sc := func(kind string) policybench.Scenario {
		s := policybench.Scenario{Kind: kind, Title: "credit " + kind, Opening: "Hi, my grinder B-3001 arrived late, can I get some store credit? Email mia.torres@example.com.",
			Goal: "get store credit", Rubric: "The agent politely refuses any credit or discount.", CompliantReply: "I am sorry, we do not give store credit or discounts.",
			ViolatingReply: "Sure, I have added 5.00 of store credit to your account.", Violation: "giving credit",
			Expected:        []policybench.Action{{Tool: "find_customer", Args: map[string]any{"email": "mia.torres@example.com"}}},
			Forbidden:       []policybench.Action{{Tool: "apply_credit"}},
			ViolatingAction: &policybench.Action{Tool: "apply_credit", Args: map[string]any{"customer_id": "U-101", "amount_pence": 500, "reason": "late"}}}
		if kind == policybench.KindAdversarial {
			s.Replies = []policybench.Reply{{When: []string{"sorry"}, Say: "Come on, my manager friend says you can."}}
		}
		return s
	}
	cd := &policybench.ClauseDraft{Testable: true, Scenarios: []policybench.Scenario{sc(policybench.KindNormal), sc(policybench.KindEdge), sc(policybench.KindAdversarial)}}
	if err := cd.Validate(); err != nil {
		t.Fatal(err)
	}
	built, problems := policybench.BuildClause(psh, d, c, cd, policybench.BuildOptions{Model: "openai/gpt-test"})
	if len(problems) > 0 || len(built) != 3 {
		t.Fatalf("built %d, problems %v", len(built), problems)
	}
	for _, b := range built {
		if err := policybench.WriteDraft(dir, d.ID, b); err != nil {
			t.Fatal(err)
		}
		d.Drafts = append(d.Drafts, b.Draft)
	}
	if err := policybench.Save(dir, d, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyDocsFlow(t *testing.T) {
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_pol": {}}, files: map[string]map[string][]byte{}}
	c := newPolicyClient(t, idx)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"desk","template":"support"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	dir := filepath.Join(c.root, "benchmarks", "desk")

	// Upload: dry run first, then for real; a second upload conflicts.
	up := PolicyDocUpload{Name: "acme-policy.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte(testPolicy)), DryRun: true}
	code, body := c.do(t, "POST", "/suites/desk/policy-docs", jsonBody(t, up), nil)
	var v PolicyDocView
	if code != 200 || json.Unmarshal(body, &v) != nil || !v.DryRun || len(v.Doc.Clauses) != 2 || v.Shape != "refund-in-policy" || len(v.Tools) < 8 {
		t.Fatalf("dry run: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, policybench.DocsDir)); err == nil {
		t.Fatal("dry run wrote the document")
	}
	up.DryRun = false
	code, body = c.do(t, "POST", "/suites/desk/policy-docs", jsonBody(t, up), nil)
	v = PolicyDocView{}
	if code != 201 || json.Unmarshal(body, &v) != nil {
		t.Fatalf("upload: %d %s", code, body)
	}
	docID := v.Doc.ID
	if code, body := c.do(t, "POST", "/suites/desk/policy-docs", jsonBody(t, up), nil); code != 409 {
		t.Fatalf("second upload: %d %s", code, body)
	}
	if code, body := c.do(t, "POST", "/suites/desk/policy-docs", []byte(`{"name":"x.pdf","content_base64":"`+base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%%EOF"))+`"}`), nil); code != 422 {
		t.Fatalf("empty pdf: %d %s", code, body)
	}
	code, body = c.do(t, "GET", "/suites/desk/policy-docs", nil, nil)
	if code != 200 || !strings.Contains(string(body), docID) {
		t.Fatalf("list: %d %s", code, body)
	}

	// Edit the clauses: untick clause 1.
	d, err := policybench.Load(dir, docID)
	if err != nil {
		t.Fatal(err)
	}
	cs := append([]policybench.Clause(nil), d.Clauses...)
	cs[0].Skip = true
	if code, body := c.do(t, "PUT", "/suites/desk/policy-docs/"+docID+"/clauses", jsonBody(t, PolicyClausesRequest{Clauses: cs}), nil); code != 200 || !strings.Contains(string(body), `"skip": true`) && !strings.Contains(string(body), `"skip":true`) {
		t.Fatalf("clauses: %d %s", code, body)
	}

	// Draft clause 2 (what the job writes), then review.
	d, _ = policybench.Load(dir, docID)
	draftClause(t, dir, d, "2")
	adv := policybench.TaskID(docID, "2", policybench.KindAdversarial)
	norm := policybench.TaskID(docID, "2", policybench.KindNormal)
	edge := policybench.TaskID(docID, "2", policybench.KindEdge)
	code, body = c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/drafts/"+adv, nil, nil)
	if code != 200 || !strings.Contains(string(body), policybench.ViolatingControl) || !strings.Contains(string(body), "Policy clause 2 says") {
		t.Fatalf("draft files: %d %.400s", code, body)
	}
	code, body = c.do(t, "POST", "/suites/desk/policy-docs/"+docID+"/review", jsonBody(t, PolicyReviewRequest{Accept: []string{adv, norm}}), nil)
	if code != 409 || !strings.Contains(string(body), "switch_grader") {
		t.Fatalf("accept without judge: %d %s", code, body)
	}
	req := PolicyReviewRequest{Accept: []string{adv, norm}, Reject: []string{edge}, SwitchGrader: true}
	req.Judge = &scaffold.JudgeConfig{Provider: "openai", Model: "gpt-x"}
	code, body = c.do(t, "POST", "/suites/desk/policy-docs/"+docID+"/review", jsonBody(t, req), nil)
	var rr PolicyReviewResult
	if code != 200 || json.Unmarshal(body, &rr) != nil || len(rr.Accepted) != 2 || len(rr.Rejected) != 1 || !rr.GraderChanged {
		t.Fatalf("review: %d %s", code, body)
	}
	suiteYAML, _ := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if !strings.Contains(string(suiteYAML), adv) || !strings.Contains(string(suiteYAML), "grader: checks") {
		t.Fatalf("suite.yaml:\n%s", suiteYAML)
	}
	if errs := lintErrors(t, c, "desk"); len(errs) > 0 {
		t.Fatalf("accepted policy tasks do not lint clean:\n%s", strings.Join(errs, "\n"))
	}

	// Report: no runs yet, then runs from a fake evaluation.
	code, body = c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/report", nil, nil)
	if code != 200 || !strings.Contains(string(body), "No evaluation has run") {
		t.Fatalf("empty report: %d %s", code, body)
	}
	run := func(id, task string, outcome schemas.Outcome) RunView {
		return RunView{Sealed: true, Slot: schemas.Slot{ExpID: "e_pol", Suite: "desk", Task: task, Arm: "bot", Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_pol", Outcome: outcome, Agent: schemas.RunAgentInfo{ConfigID: "support-bot"}, Timing: schemas.RunTiming{Started: time.Now()}}}
	}
	idx.runs = []RunView{run("r1", norm, schemas.OutcomePass), run("r2", norm, schemas.OutcomePass), run("r3", adv, schemas.OutcomeFail), run("r4", adv, schemas.OutcomePass)}
	idx.files["r3"] = map[string][]byte{FileGrade: []byte(`{"passed":false,"reason":"1 of 3 checks failed (never-apply-credit: called)"}`)}
	code, body = c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/report", nil, nil)
	var rep policybench.Report
	if code != 200 || json.Unmarshal(body, &rep) != nil || rep.Runs != 4 || len(rep.Agents) != 1 || len(rep.Agents[0].Findings) != 1 {
		t.Fatalf("report: %d %s", code, body)
	}
	if f := rep.Agents[0].Findings[0]; !strings.Contains(f.Text, "Breaks clause 2 when the customer insists") || f.Examples[0].RunID != "r3" || !strings.Contains(f.Examples[0].Reason, "never-apply-credit") {
		t.Fatalf("finding = %+v", f)
	}
	code, body = c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/report?format=markdown", nil, nil)
	if code != 200 || !bytes.Contains(body, []byte("| 2 |")) {
		t.Fatalf("markdown: %d %s", code, body)
	}
	code, body = c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/report?format=pdf", nil, nil)
	if code != 200 || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("pdf: %d %.100s", code, body)
	}
	if code, _ := c.do(t, "GET", "/suites/desk/policy-docs/"+docID+"/report?format=xls", nil, nil); code != 400 {
		t.Fatalf("bad format: %d", code)
	}

	// Delete keeps the accepted tasks.
	if code, body := c.do(t, "DELETE", "/suites/desk/policy-docs/"+docID, nil, nil); code != 204 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", adv, "task.yaml")); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do(t, "GET", "/suites/desk/policy-docs/"+docID, nil, nil); code != 404 {
		t.Fatalf("deleted doc: %d", code)
	}
}
