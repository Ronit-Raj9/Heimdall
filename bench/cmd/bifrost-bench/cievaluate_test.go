package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdictab"
)

const ciTestToken = "hm_abcd1234_secretsecretsecretsecretsecretsecretsecre"

const liveYAML = "id: my-agent\nadapter: command\nimage: example/agent:1\nmodel: gpt-5-mini\n"

// fakeHeimdall is the hosted API `ci evaluate` calls.
type fakeHeimdall struct {
	mu         sync.Mutex
	t          *testing.T
	versions   map[string]int // yaml -> version
	live       int
	spec       *schemas.EvaluationSpec
	polls      int
	finalState string
	report     reportcard.Report
	cancelled  bool
	registered []string
}

func newFakeHeimdall(t *testing.T) *fakeHeimdall {
	return &fakeHeimdall{t: t, versions: map[string]int{liveYAML: 3}, live: 3, finalState: schemas.EvaluationDone}
}

func (f *fakeHeimdall) yamlOf(n int) string {
	for y, v := range f.versions {
		if v == n {
			return y
		}
	}
	return ""
}

func (f *fakeHeimdall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+ciTestToken {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"sign in"}}`)
		return
	}
	send := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/bench")
	switch {
	case r.Method == "GET" && p == "/agents/my-agent/versions":
		send(200, map[string]any{"agent": "my-agent", "live": f.live, "versions": []any{}})
	case r.Method == "GET" && strings.HasPrefix(p, "/agents/my-agent/versions/"):
		n := 0
		_, _ = fmtSscan(strings.TrimPrefix(p, "/agents/my-agent/versions/"), &n)
		send(200, map[string]any{"version": n, "hash": "h" + itoa(n), "yaml": f.yamlOf(n)})
	case r.Method == "POST" && p == "/agents/my-agent/versions":
		var body struct{ YAML, Note string }
		json.NewDecoder(r.Body).Decode(&body)
		f.registered = append(f.registered, body.Note)
		v, ok := f.versions[body.YAML]
		if !ok {
			v = len(f.versions) + 3
			f.versions[body.YAML] = v
		}
		send(201, map[string]any{"version": v, "hash": "h" + itoa(v), "created": !ok})
	case r.Method == "POST" && p == "/evaluations":
		var spec schemas.EvaluationSpec
		json.NewDecoder(r.Body).Decode(&spec)
		f.spec = &spec
		send(202, schemas.Evaluation{ID: "ev_1", Spec: spec, Status: schemas.EvaluationQueued})
	case r.Method == "GET" && p == "/evaluations/ev_1":
		f.polls++
		st := schemas.EvaluationRunning
		if f.polls >= 2 {
			st = f.finalState
		}
		send(200, schemas.Evaluation{ID: "ev_1", Status: st, Children: []schemas.EvaluationChild{{Benchmark: "b1", Status: "running"}}})
	case r.Method == "GET" && p == "/evaluations/ev_1/report":
		send(200, f.report)
	case r.Method == "POST" && p == "/evaluations/ev_1/cancel":
		f.cancelled = true
		send(200, map[string]any{})
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

// fakeReporter records the comment and statuses.
type fakeReporter struct {
	comments []string
	states   []string
	links    []string
}

func (r *fakeReporter) Comment(_ context.Context, marker, body string) error {
	if !strings.HasPrefix(body, marker) {
		return io.ErrUnexpectedEOF
	}
	r.comments = append(r.comments, body)
	return nil
}
func (r *fakeReporter) Status(_ context.Context, state, desc, link string) error {
	r.states = append(r.states, state)
	r.links = append(r.links, link)
	return nil
}
func (r *fakeReporter) Info() prContext {
	return prContext{Provider: "github", Number: "42", SHA: "0123456789abcdef"}
}

func regressedReport() reportcard.Report {
	return reportcard.Report{
		Evaluation: "ev_1", Mode: "versions",
		Benchmarks: []reportcard.Benchmark{{
			Benchmark: "hd-harbor-tb2",
			Arms: map[string]reportcard.Metrics{
				"A": {Score: 0.6, CILow: 0.5, CIHigh: 0.7, Runs: 20, CostUSD: 2},
				"B": {Score: 0.3, CILow: 0.2, CIHigh: 0.4, Runs: 20, CostUSD: 2.5},
			},
			Verdict: &verdictab.BenchmarkVerdict{Winner: "A", Delta: -0.3, Significant: true},
		}},
		Overall: &verdictab.OverallVerdict{Winner: "A", Significant: true, Delta: -0.3, Benchmarks: 1,
			Wins: map[string]int{"A": 1, "B": 0, "tie": 0, "unclear": 0}},
	}
}

func ciTestOptions(t *testing.T, srv string, config string) *ciEvalOptions {
	return &ciEvalOptions{url: srv, token: ciTestToken, agent: "my-agent", config: config, baseline: "live",
		pack: "coding", faults: []string{"model-down"}, failOn: ci.DefaultFailOn, timeout: time.Minute, poll: time.Millisecond,
		out: filepath.Join(t.TempDir(), "out")}
}

func writeConfig(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCIEvaluateRegressedFailsAndReports(t *testing.T) {
	t.Setenv("GITHUB_OUTPUT", "")
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	f := newFakeHeimdall(t)
	f.report = regressedReport()
	srv := httptest.NewServer(f)
	defer srv.Close()
	o := ciTestOptions(t, srv.URL, writeConfig(t, strings.Replace(liveYAML, "gpt-5-mini", "gpt-5", 1)))
	rep := &fakeReporter{}
	code := ciEvaluate(context.Background(), o, rep, io.Discard)
	if code != ci.GateExitRegressed {
		t.Fatalf("exit %d", code)
	}
	if f.spec == nil || f.spec.Mode != "versions" || len(f.spec.Arms) != 2 {
		t.Fatalf("spec %+v", f.spec)
	}
	a, b := f.spec.Arms[0], f.spec.Arms[1]
	if a.Version != 3 || b.Version != 4 || a.Model != "gpt-5-mini" || b.Model != "gpt-5" || f.spec.Pack != "coding" || f.spec.Faults[0] != "model-down" {
		t.Fatalf("arms %+v %+v spec %+v", a, b, f.spec)
	}
	if len(f.registered) != 1 || f.registered[0] != "pull request #42 at 0123456789ab" {
		t.Fatalf("note %q", f.registered)
	}
	if len(rep.comments) != 1 || !strings.Contains(rep.comments[0], "Gate failed") || !strings.Contains(rep.comments[0], srv.URL+"/evaluations/ev_1") {
		t.Fatalf("comment:\n%v", rep.comments)
	}
	if strings.Join(rep.states, ",") != "pending,failure" {
		t.Fatalf("states %v", rep.states)
	}
	for _, n := range []string{"report.json", "gate.json", "comment.md"} {
		if _, err := os.Stat(filepath.Join(o.out, n)); err != nil {
			t.Error(err)
		}
	}
}

func TestCIEvaluateNoChangePasses(t *testing.T) {
	f := newFakeHeimdall(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	rep := &fakeReporter{}
	if code := ciEvaluate(context.Background(), ciTestOptions(t, srv.URL, writeConfig(t, liveYAML)), rep, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if f.spec != nil {
		t.Fatal("launched an evaluation for an unchanged config")
	}
	if len(rep.comments) != 1 || !strings.Contains(rep.comments[0], "no agent change") || rep.states[0] != "success" {
		t.Fatalf("%+v", rep)
	}
}

func TestCIEvaluateImagePatchesLiveConfig(t *testing.T) {
	f := newFakeHeimdall(t)
	f.report = regressedReport()
	f.report.Benchmarks[0].Verdict = &verdictab.BenchmarkVerdict{Winner: "B", Delta: 0.3, Significant: true}
	f.report.Overall = &verdictab.OverallVerdict{Winner: "B", Significant: true, Delta: 0.3, Benchmarks: 1, Wins: map[string]int{"B": 1}}
	f.report.Benchmarks[0].Arms["A"], f.report.Benchmarks[0].Arms["B"] = f.report.Benchmarks[0].Arms["B"], f.report.Benchmarks[0].Arms["A"]
	srv := httptest.NewServer(f)
	defer srv.Close()
	o := ciTestOptions(t, srv.URL, "")
	o.image = "ghcr.io/me/agent@sha256:" + strings.Repeat("a", 64)
	if code := ciEvaluate(context.Background(), o, nil, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := f.yamlOf(4)
	if !strings.Contains(got, "image_digest: sha256:") || !strings.Contains(got, "ghcr.io/me/agent@sha256:") {
		t.Fatalf("registered:\n%s", got)
	}
}

func TestCIEvaluateTimeoutCancels(t *testing.T) {
	f := newFakeHeimdall(t)
	f.finalState = schemas.EvaluationRunning
	srv := httptest.NewServer(f)
	defer srv.Close()
	o := ciTestOptions(t, srv.URL, writeConfig(t, strings.Replace(liveYAML, "gpt-5-mini", "gpt-5", 1)))
	o.timeout = 5 * time.Millisecond
	o.poll = 2 * time.Millisecond
	rep := &fakeReporter{}
	if code := ciEvaluate(context.Background(), o, rep, io.Discard); code != exitInfra {
		t.Fatalf("exit %d", code)
	}
	if !f.cancelled || rep.states[len(rep.states)-1] != "error" {
		t.Fatalf("cancelled %v states %v", f.cancelled, rep.states)
	}
}

func TestCIEvaluateBadToken(t *testing.T) {
	srv := httptest.NewServer(newFakeHeimdall(t))
	defer srv.Close()
	o := ciTestOptions(t, srv.URL, writeConfig(t, liveYAML))
	o.token = "hm_wrong"
	if code := ciEvaluate(context.Background(), o, nil, io.Discard); code != exitInfra {
		t.Fatalf("exit %d", code)
	}
}

func TestParseCIEvaluateEnvAndRules(t *testing.T) {
	t.Setenv("HEIMDALL_URL", "https://heimdall.example.com/")
	t.Setenv("HEIMDALL_TOKEN", ciTestToken)
	t.Setenv("HEIMDALL_AGENT", "my-agent")
	t.Setenv("HEIMDALL_CONFIG", "agents/my-agent.yaml")
	t.Setenv("HEIMDALL_BENCHMARKS", "a,b")
	t.Setenv("HEIMDALL_FAIL_ON", "regressed,inconclusive")
	o, err := parseCIEvaluate([]string{"-trials", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if o.url != "https://heimdall.example.com" || len(o.benchmarks) != 2 || o.trials != 3 || len(o.failOn) != 2 {
		t.Fatalf("%+v", o)
	}
	t.Setenv("HEIMDALL_URL", "http://heimdall.example.com")
	if _, err := parseCIEvaluate(nil); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http to a remote host: %v", err)
	}
	t.Setenv("HEIMDALL_URL", "https://h.example.com")
	t.Setenv("HEIMDALL_TOKEN", "")
	if _, err := parseCIEvaluate(nil); err == nil || !strings.Contains(err.Error(), "HEIMDALL_TOKEN") {
		t.Fatalf("no token: %v", err)
	}
}

// TestGitHubAndGitLabReporters drives the two REST clients against fake APIs:
// the second comment edits the first, statuses land on the head commit.
func TestGitHubAndGitLabReporters(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	comments := []map[string]any{{"id": 7, "body": "unrelated"}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		switch {
		case r.Method == "GET":
			json.NewEncoder(w).Encode(comments)
		case r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/comments") || strings.HasSuffix(r.URL.Path, "/notes")):
			comments = append(comments, map[string]any{"id": 9, "body": body["body"]})
			w.WriteHeader(201)
		case strings.Contains(r.URL.Path, "/statuses/"):
			if body["state"] == "" {
				t.Errorf("status without state")
			}
			w.WriteHeader(201)
		default:
			w.WriteHeader(200)
		}
	}))
	defer api.Close()
	ctx := context.Background()
	marker := ci.EvaluationMarker("my-agent")

	gh := &githubReporter{api: api.URL, repo: "o/r", token: "t", agent: "my-agent", http: api.Client(), ctx: prContext{Number: "5", SHA: "abc"}}
	for i := 0; i < 2; i++ {
		if err := gh.Comment(ctx, marker, marker+"\nbody"); err != nil {
			t.Fatal(err)
		}
	}
	if err := gh.Status(ctx, "success", "ok", "https://x"); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /repos/o/r/issues/5/comments", "POST /repos/o/r/issues/5/comments", "GET /repos/o/r/issues/5/comments", "PATCH /repos/o/r/issues/comments/9", "POST /repos/o/r/statuses/abc"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("github calls:\n%v", calls)
	}

	calls, comments = nil, nil
	gl := &gitlabReporter{api: api.URL, project: "12", token: "t", agent: "my-agent", http: api.Client(), ctx: prContext{Number: "3", SHA: "def"}}
	for i := 0; i < 2; i++ {
		if err := gl.Comment(ctx, marker, marker+"\nbody"); err != nil {
			t.Fatal(err)
		}
	}
	if err := gl.Status(ctx, "pending", "running", ""); err != nil {
		t.Fatal(err)
	}
	want = []string{"GET /projects/12/merge_requests/3/notes", "POST /projects/12/merge_requests/3/notes", "GET /projects/12/merge_requests/3/notes", "PUT /projects/12/merge_requests/3/notes/9", "POST /projects/12/statuses/def"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("gitlab calls:\n%v", calls)
	}
}

func TestDetectPRFromGitHubEvent(t *testing.T) {
	ev := filepath.Join(t.TempDir(), "event.json")
	os.WriteFile(ev, []byte(`{"pull_request":{"number":17,"head":{"sha":"headsha"}}}`), 0o644)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITLAB_CI", "")
	t.Setenv("GITHUB_EVENT_PATH", ev)
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_TOKEN", "t")
	t.Setenv("GITHUB_SHA", "mergesha")
	pr := detectPR("auto")
	if pr == nil || pr.Info().Number != "17" || pr.Info().SHA != "headsha" {
		t.Fatalf("%+v", pr)
	}
	if detectPR("none") != nil {
		t.Fatal("none still reports")
	}
}

func fmtSscan(s string, n *int) (int, error) { return fmt.Sscan(s, n) }
func itoa(n int) string                      { return strconv.Itoa(n) }

func TestCIInstallHosted(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	if code := runCI([]string{"install", "github", "-agent", "my-agent", "-pack", "coding", "-fault", "model-down"}); code != exitOK {
		t.Fatalf("github exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "heimdall-evaluate.yml"))
	if err != nil || !strings.Contains(string(b), `config: "agents/my-agent.yaml"`) {
		t.Fatalf("%v\n%s", err, b)
	}
	if code := runCI([]string{"install", "gitlab", "-agent", "my-agent", "-benchmark", "hd-harbor-tb2", "-url", "https://h.example.com"}); code != exitOK {
		t.Fatalf("gitlab exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitlab", "heimdall-evaluate.gitlab-ci.yml")); err != nil {
		t.Fatal(err)
	}
}
