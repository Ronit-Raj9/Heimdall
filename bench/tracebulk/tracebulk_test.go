package tracebulk

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

const (
	testKey      = "mx-key-bulk-1"
	plantedEmail = "jane.doe@example.com"
)

var testNow = time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

// FakeMaxim is a local Maxim log repository: search (errors, evaluator
// and unfiltered pages) and trace reads, keyed by x-maxim-api-key.
type fakeMaxim struct {
	mu       sync.Mutex
	searches []map[string]any
}

func refundTrace(id, request string) map[string]any {
	return map[string]any{"id": id, "name": "support", "input": request,
		"timeline": []any{
			map[string]any{"id": "g-" + id, "type": "generation", "model": "gpt-5.4-mini", "messages": []any{map[string]any{"role": "user", "content": request}}},
			map[string]any{"id": "t-" + id, "type": "tool_call", "name": "refund_order", "args": map[string]any{"order": "A1"}, "status": "error", "error": "refund service down"},
		},
		"output": "Sorry, I could not refund it."}
}

func (f *fakeMaxim) handler(t *testing.T) http.Handler {
	at := testNow.Add(-2 * time.Hour).Format(time.RFC3339)
	traces := map[string]map[string]any{
		"mx-1": refundTrace("mx-1", "Refund order A1 please, my email is "+plantedEmail),
		"mx-3": refundTrace("mx-3", "I was charged twice for my last order, give me my money back"),
		"mx-2": {"id": "mx-2", "input": "What is the return window for shoes?", "output": "You can return shoes within 90 days.",
			"evaluationData": []any{map[string]any{"evaluatorName": "Faithfulness", "pass": false, "reasoning": "policy says 30 days"}}},
		"mx-4": {"id": "mx-4", "input": "How do I reset my password?", "output": "Try again later."},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-maxim-api-key") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/log-repositories/logs/search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.searches = append(f.searches, body)
			f.mu.Unlock()
			if body["workspaceId"] != "ws1" || body["id"] != "repo1" {
				t.Errorf("search body %v", body)
			}
			var logs []any
			switch {
			case body["traceFilters"] != nil:
				logs = []any{map[string]any{"id": "mx-1", "startTimestamp": at}, map[string]any{"id": "mx-3", "startTimestamp": at}}
			case body["evaluatorFilters"] != nil:
				logs = []any{map[string]any{"id": "mx-2", "startTimestamp": at,
					"evaluationData": []any{map[string]any{"evaluatorName": "Faithfulness", "pass": false, "reasoning": "policy says 30 days"}}}}
			default:
				logs = []any{
					map[string]any{"id": "mx-4", "startTimestamp": at, "evaluationData": []any{map[string]any{"evaluatorName": "Helpfulness", "score": 0.2}}},
					map[string]any{"id": "mx-5", "startTimestamp": at, "evaluationData": []any{map[string]any{"evaluatorName": "Helpfulness", "score": "0.9"}}},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"logs": logs}, "pagination": map[string]any{"total": len(logs)}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/log-repositories/logs/traces":
			tr, ok := traces[r.URL.Query().Get("id")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": tr})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// NewProject writes a project with a tool-use and a question and answer
// benchmark and returns its inbox.
func newProject(t *testing.T) (*traceinbox.Inbox, string) {
	t.Helper()
	root := t.TempDir()
	bench := filepath.Join(root, "benchmarks")
	for id, tmpl := range map[string]string{"tools": scaffold.TemplateToolUse, "answers": scaffold.TemplateQA} {
		files, err := scaffold.Suite(scaffold.SuiteOptions{ID: id, Template: tmpl})
		if err != nil {
			t.Fatal(err)
		}
		if err := scaffold.WriteFiles(filepath.Join(bench, id), files, false); err != nil {
			t.Fatal(err)
		}
	}
	ib := traceinbox.New(filepath.Join(root, traceinbox.Dir), nil, bench)
	ib.ProjectRoot = root
	ib.Now = func() time.Time { return testNow }
	return ib, root
}

func pull(t *testing.T, ib *traceinbox.Inbox, srv *httptest.Server) *Batch {
	t.Helper()
	below := 0.5
	m := Maxim{Client: maxim.Client{APIBase: srv.URL}, APIKey: testKey, Settings: maxim.Settings{WorkspaceID: "ws1", RepoID: "repo1"}}
	b, err := PullMaxim(context.Background(), ib, m, PullOptions{Now: testNow, CreatedBy: "ana",
		Filter: Filter{Errors: true, Evaluators: []string{"Faithfulness"}, ScoreBelow: &below}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func clusterBy(t *testing.T, b *Batch, pred func(Cluster) bool) Cluster {
	t.Helper()
	for _, c := range b.Clusters {
		if pred(c) {
			return c
		}
	}
	t.Fatalf("no such cluster in %+v", b.Clusters)
	return Cluster{}
}

func TestPullClustersDraftReview(t *testing.T) {
	fake := &fakeMaxim{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ib, root := newProject(t)
	b := pull(t, ib, srv)

	if b.Counts.Failed != 4 || b.Counts.Fetched != 4 || b.Counts.Errored != 2 || b.Counts.EvaluatorFailed != 1 || b.Counts.LowScore != 1 || b.Counts.Scanned != 5 {
		t.Fatalf("counts %+v warnings %v", b.Counts, b.Warnings)
	}
	if len(b.Clusters) != 3 {
		t.Fatalf("clusters %+v", b.Clusters)
	}
	refund := clusterBy(t, b, func(c Cluster) bool { return c.Tool == "refund_order" })
	if len(refund.Items) != 2 || refund.Occurrences != 2 || !strings.Contains(refund.Label, "refund_order fails") || b.Clusters[0].ID != refund.ID {
		t.Fatalf("refund cluster %+v", refund)
	}
	faith := clusterBy(t, b, func(c Cluster) bool { return c.Evaluator == "Faithfulness" })
	if faith.Label != "Maxim evaluator Faithfulness failed" || faith.Kind != "qa" || len(faith.Links) != 1 || !strings.Contains(faith.Links[0], "ws1") {
		t.Fatalf("faithfulness cluster %+v", faith)
	}
	low := clusterBy(t, b, func(c Cluster) bool { return c.Evaluator == "Helpfulness" })
	if low.Label != "Low Helpfulness score" || !strings.Contains(strings.Join(low.Reasons, " "), "scored 0.2, below 0.5") {
		t.Fatalf("low score cluster %+v", low)
	}

	st := Open(filepath.Join(root, traceinbox.Dir))
	if err := st.Save(b); err != nil {
		t.Fatal(err)
	}

	// Refund cluster into the tool-use benchmark: one task, the other item a duplicate.
	res, err := Draft(context.Background(), ib, b, DraftOptions{Suite: "tools", Clusters: []string{refund.ID}}, testNow)
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("draft %+v %v", res, err)
	}
	tool := res.Tasks[0]
	if tool.State != StatePending || tool.Kind != "tool-use" || !slices.Contains(tool.Generated, "succeeds-refund-order") || !slices.Contains(tool.Generated, "reply") {
		t.Fatalf("tool task %+v", tool)
	}
	dir := filepath.Join(root, "benchmarks", "tools", "tasks", tool.ID)
	checks := read(t, filepath.Join(dir, ChecksFile))
	var spec struct {
		Version int `yaml:"version"`
		Checks  []struct {
			ID         string         `yaml:"id"`
			ToolCalled map[string]any `yaml:"tool_called"`
			Rubric     string         `yaml:"reply_rubric"`
		} `yaml:"checks"`
	}
	if err := yaml.Unmarshal([]byte(checks), &spec); err != nil || spec.Version != 1 || len(spec.Checks) != 2 ||
		spec.Checks[0].ToolCalled["tool"] != "refund_order" || spec.Checks[0].ToolCalled["succeeded"] != true ||
		!strings.Contains(spec.Checks[1].Rubric, "Sorry, I could not refund it.") || !strings.Contains(checks, "pending review") {
		t.Fatalf("checks.yaml %v\n%s", err, checks)
	}
	if strings.Contains(checks, "# EDIT ME") {
		t.Fatal("checks.yaml must not hold an EDIT ME line")
	}
	if meta := read(t, filepath.Join(dir, "task.yaml")); !strings.Contains(meta, TagPendingReview) || !strings.Contains(meta, "regression") {
		t.Fatalf("task.yaml tags:\n%s", meta)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, ScenarioFile))), &row); err != nil || row["generated"] != true || row["opening_message"] == "" {
		t.Fatalf("scenario %v %v", row, err)
	}
	items, _ := ib.Store.Items()
	dup := 0
	for _, it := range items {
		if it.State == traceinbox.StateDuplicate && it.DuplicateOf == tool.Item {
			dup++
		}
	}
	if dup != 1 {
		t.Fatalf("want the other refund item marked duplicate, got %d", dup)
	}
	// A second draft of the same cluster finds nothing open.
	again, err := Draft(context.Background(), ib, b, DraftOptions{Suite: "tools", Clusters: []string{refund.ID}}, testNow)
	if err != nil || len(again.Tasks) != 0 || len(again.Warnings) != 1 {
		t.Fatalf("second draft %+v %v", again, err)
	}

	// The evaluator cluster into the question and answer benchmark.
	res, err = Draft(context.Background(), ib, b, DraftOptions{Suite: "answers", Clusters: []string{faith.ID}}, testNow)
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("draft qa %+v %v", res, err)
	}
	qa, _ := b.Task(res.Tasks[0].Key())
	if qa.Kind != "qa" || len(qa.EditMe) == 0 {
		t.Fatalf("qa draft %+v", qa)
	}
	if err := Accept(ib.BenchmarksRoot, ib, b, qa, Review{}, "ana", testNow); err == nil {
		t.Fatal("a question and answer review without an answer must fail")
	}
	if err := Accept(ib.BenchmarksRoot, ib, b, qa, Review{Answer: "Shoes can be returned within 30 days."}, "ana", testNow); err != nil {
		t.Fatal(err)
	}
	if qa.State != StateAccepted || len(qa.EditMe) != 0 || qa.ReviewedBy != "ana" {
		t.Fatalf("qa after review %+v", qa)
	}
	qdir := filepath.Join(root, "benchmarks", "answers", "tasks", qa.ID)
	ans := read(t, filepath.Join(qdir, "verifier", "answer.yaml"))
	if !strings.Contains(ans, "match: exact") || !strings.Contains(ans, "Shoes can be returned within 30 days.") || !strings.Contains(ans, "You can return shoes within 90 days.") {
		t.Fatalf("answer.yaml:\n%s", ans)
	}
	if meta := read(t, filepath.Join(qdir, "task.yaml")); strings.Contains(meta, TagPendingReview) || !strings.Contains(meta, TagMaxim) {
		t.Fatalf("reviewed task.yaml tags:\n%s", meta)
	}
	if c := read(t, filepath.Join(qdir, ChecksFile)); !strings.Contains(c, "reviewed and accepted by ana") {
		t.Fatalf("reviewed checks:\n%s", c)
	}
	if s, tk, err := suite.LoadTask(ib.BenchmarksRoot, "answers", qa.ID); err != nil {
		t.Fatal(err)
	} else {
		for _, p := range suite.LintTask(s, tk) {
			if p.Severity == suite.SeverityError {
				t.Errorf("reviewed task lint: %s: %s", p.Path, p.Message)
			}
		}
	}
	if err := Accept(ib.BenchmarksRoot, ib, b, qa, Review{Answer: "x"}, "ana", testNow); err == nil {
		t.Fatal("a second review must conflict")
	}

	// The tool-use review keeps its state-check file to finish by hand.
	tp, _ := b.Task(tool.Key())
	if err := Accept(ib.BenchmarksRoot, ib, b, tp, Review{Rubric: "Refund A1 once the service answers, and tell the customer."}, "ana", testNow); err != nil {
		t.Fatal(err)
	}
	if tp.State != StateNeedFiles || len(tp.EditMe) == 0 || tp.Suggested != "Refund A1 once the service answers, and tell the customer." {
		t.Fatalf("tool task after review %+v", tp)
	}

	// Readiness: queued, then not ready once its job finished without an admission.
	SetReadiness(qa, "job-1", testNow)
	RefreshReadiness(ib.BenchmarksRoot, b, func(string) bool { return false })
	if qa.Readiness.Status != ReadyQueued {
		t.Fatalf("readiness %+v", qa.Readiness)
	}
	RefreshReadiness(ib.BenchmarksRoot, b, func(id string) bool { return id == "job-1" })
	if qa.Readiness.Status != ReadyNotReady {
		t.Fatalf("readiness %+v", qa.Readiness)
	}

	// Reject removes the folder and the suite entry.
	if err := Reject(ib.BenchmarksRoot, b, tp, "not a real failure", "ana", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("rejected task folder still there: %v", err)
	}
	if s := read(t, filepath.Join(root, "benchmarks", "tools", "suite.yaml")); strings.Contains(s, tool.ID) {
		t.Fatalf("suite.yaml still lists %s:\n%s", tool.ID, s)
	}

	if err := st.Save(b); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(b.ID)
	if err != nil || len(got.Tasks) != 2 || len(got.Clusters) != 3 {
		t.Fatalf("stored batch %+v %v", got, err)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Tasks != 2 || list[0].Pending != 0 {
		t.Fatalf("list %+v %v", list, err)
	}
	checkNoLeak(t, root)
}

func TestFilterNormalize(t *testing.T) {
	var f Filter
	if err := f.Normalize(); err == nil {
		t.Fatal("an empty filter must say which traces failed")
	}
	f = Filter{Errors: true}
	if err := f.Normalize(); err != nil || f.Days != DefaultDays || f.Limit != DefaultLimit {
		t.Fatalf("%+v %v", f, err)
	}
	for _, bad := range []Filter{{Errors: true, Days: 91}, {Errors: true, Limit: 5000}, {ScoreEvaluators: []string{"x"}}, {Evaluators: []string{"a\nb"}}} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
}

func TestPullRefusedKey(t *testing.T) {
	srv := httptest.NewServer((&fakeMaxim{}).handler(t))
	defer srv.Close()
	ib, _ := newProject(t)
	m := Maxim{Client: maxim.Client{APIBase: srv.URL}, APIKey: "wrong", Settings: maxim.Settings{WorkspaceID: "ws1", RepoID: "repo1"}}
	_, err := PullMaxim(context.Background(), ib, m, PullOptions{Now: testNow, Filter: Filter{Errors: true}})
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("want the key refused, got %v", err)
	}
}

func TestStoreIDs(t *testing.T) {
	st := Open(t.TempDir())
	if _, err := st.Get("../x"); err == nil {
		t.Fatal("a bad id must not read")
	}
	if !ValidID(NewID(testNow)) {
		t.Fatal("new ids must be valid")
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkNoLeak(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, _ := os.ReadFile(p); strings.Contains(string(b), plantedEmail) || strings.Contains(string(b), testKey) {
			t.Errorf("%s holds a planted value", p)
		}
		return nil
	})
}
