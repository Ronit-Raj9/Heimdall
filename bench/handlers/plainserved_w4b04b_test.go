package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Wave 4, F1.3 (group B review): every GET or POST answer that carries
// engine, planner or worker text is cleaned once, where it leaves for a
// browser, on a hosted server. The routes this test covers:
//
//	GET  /experiments/{id}/events (poll)      live event messages
//	GET  /experiments/{id}/events (stream)    live event messages
//	GET  /experiments/{id}                    the run problem
//	GET  /experiments                         the run problem of each comparison
//	POST /experiments/plan                    the planner's errors (FailedPlan)
//	GET  /evaluations/{id}                    skipped reasons, the problem line
//	GET  /evaluations                         the same, for every evaluation
//	POST /evaluations/estimate                a refused benchmark's note
//	POST /estimate                            a refused forecast's notes
//	GET  /runs/{id}                           a run's part failures
//
// Served elsewhere and covered by their own tests: GET /jobs, GET /jobs/{id},
// GET /jobs/{id}/logs (jobs_w4b04_test.go, jobsorg_w4b04_test.go,
// jobsprogress_w4b04_test.go), GET /runs/{id}/receipts and /steps
// (runsplain_w4b04_test.go), every error answer (lib.SendError).

// w4b04bText names the benchmark, the task, a server path, a gateway slot
// built from an org id, a bare org id and command line flags.
const w4b04bText = "benchmark hd-swe task t01 could not start: open /var/lib/heimdall/runs/orgs/org_01m7yyh2wft8r2x5tjyxthksbj/agents/x.yaml: no such file or directory (slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20, org_01m7yyh2wft8r2x5tjyxthksbj); raise the cap with run -resume e_x -total-usd 5"

func w4b04bHosted(t *testing.T) {
	t.Helper()
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
}

// w4b04bClean fails when body names the server or lost what failed.
func w4b04bClean(t *testing.T, name, body string) {
	t.Helper()
	for _, gone := range []string{"/var/", "org_01m7", "org_org01m7", "-resume", "-total-usd"} {
		if strings.Contains(body, gone) {
			t.Errorf("%s: served %q: %s", name, gone, body)
		}
	}
	for _, keep := range []string{"benchmark hd-swe task t01 could not start", "agents/x.yaml does not exist", "provider slot openai 20"} {
		if !strings.Contains(body, keep) {
			t.Errorf("%s: lost %q: %s", name, keep, body)
		}
	}
}

// w4b04bProblemLauncher is a launcher that reports a run problem and refuses
// every plan with the fixture text.
type w4b04bProblemLauncher struct{ *fakeLauncher }

func (w4b04bProblemLauncher) RunProblem(string) (string, string) { return w4b04bText, "job_1" }

func (w4b04bProblemLauncher) Plan(context.Context, *schemas.Experiment) (*schemas.Manifest, error) {
	return nil, errors.New(w4b04bText)
}

func w4b04bClient(t *testing.T) *testClient {
	idx := testIndex()
	idx.runs = nil // nothing sealed: the comparison shows its run problem
	d := fullDeps()
	d.index = idx
	d.launcher = w4b04bProblemLauncher{&fakeLauncher{events: []schemas.LiveEvent{{Seq: 1, Kind: "error", SlotID: "s_t01_A", Message: w4b04bText}}}}
	return newTestClient(t, d)
}

// w4b04bEvalServer serves the evaluation routes over a store holding one
// evaluation with a skipped benchmark and a problem line, plus an estimate
// handler whose forecast refuses with the fixture text.
func w4b04bEvalServer(t *testing.T) *router.Router {
	t.Helper()
	r, dir := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	ev := &schemas.Evaluation{ID: "ev_plain", Status: "failed", CreatedAt: time.Now().UTC(), Problem: w4b04bText,
		Spec: schemas.EvaluationSpec{Name: "plain", Benchmarks: []string{"hd-swe-verified"}, TasksPerBenchmark: 2, Trials: 1},
		Children: []schemas.EvaluationChild{
			{Benchmark: "hd-swe-verified", Status: schemas.EvaluationChildSkipped, Skipped: w4b04bText},
		}}
	if err := evalstore.Open(dir).Save(ev); err != nil {
		t.Fatal(err)
	}
	ps, err := projectfs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: dir}, Mode: schemas.ModeRecorded, Services: Services{Suites: evalSuites(), Projects: ps}}
	est := &evalEstimateHandler{env: env, cache: newForecastCache(),
		forecast: func() ForecastSource {
			return &fakeForecast{estimateErr: fmt.Errorf("%w: %s", ErrPlanRefused, w4b04bText)}
		}}
	r.POST(APIPrefix+"/w4b04b/estimate", env.W(est.post))
	return r
}

// TestServedEngineTextPlain walks the routes listed above: in hosted mode no
// answer names a server path, an org id, a slot name or a flag, and each
// keeps the benchmark, the task and the sentence.
func TestServedEngineTextPlain(t *testing.T) {
	w4b04bHosted(t)
	c := w4b04bClient(t)
	er := w4b04bEvalServer(t)
	estBody := `{"arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":2,"trials":1}`

	routes := []struct {
		name string
		get  func() string
	}{
		{"GET /experiments/{id}/events (poll)", func() string {
			deadline := time.Now().Add(2 * time.Second)
			for {
				body := string(c.do(t, "GET", "/api/bench/experiments/e_test/events", nil, nil).Body())
				if strings.Contains(body, `"seq":1`) || time.Now().After(deadline) {
					return body
				}
				time.Sleep(5 * time.Millisecond)
			}
		}},
		{"GET /experiments/{id}/events (stream)", func() string {
			resp := c.do(t, "GET", "/api/bench/experiments/e_test/events", nil, map[string]string{"Accept": "text/event-stream"})
			for _, l := range readSSE(resp.Body()) {
				if strings.Contains(l, `"kind":"error"`) {
					return l
				}
			}
			return ""
		}},
		{"GET /experiments/{id}", func() string { return string(c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil).Body()) }},
		{"GET /experiments", func() string { return string(c.do(t, "GET", "/api/bench/experiments", nil, nil).Body()) }},
		{"POST /experiments/plan", func() string {
			body := []byte("name: demo\nsuite: coding-core@1.0.0\nconditions:\n  - {id: clean, tasks: all, trials: 1}\n")
			return string(c.do(t, "POST", "/api/bench/experiments/plan", body, authed("application/yaml")).Body())
		}},
		{"GET /evaluations/{id}", func() string {
			return string(evalCall(er, "GET", APIPrefix+"/evaluations/ev_plain", "").Response.Body())
		}},
		{"GET /evaluations", func() string { return string(evalCall(er, "GET", APIPrefix+"/evaluations", "").Response.Body()) }},
		{"POST /evaluations/estimate", func() string {
			return string(evalCall(er, "POST", APIPrefix+"/w4b04b/estimate", estBody).Response.Body())
		}},
		{"POST /estimate", func() string {
			_, out := serveForecastRoute(t, &fakeForecast{estimateErr: fmt.Errorf("%w: %s", ErrPlanRefused, w4b04bText)}, "POST", APIPrefix+"/estimate", []byte("name: demo\nsuite: s@1\n"), true)
			raw, _ := json.Marshal(out)
			return string(raw)
		}},
		{"GET /runs/{id}", func() string {
			idx := testIndex()
			idx.runs[0].Run.Failures = []schemas.PartFailure{{Stage: schemas.StageRun, Part: "verifier", Error: w4b04bText}}
			d := fullDeps()
			d.index = idx
			return string(newTestClient(t, d).do(t, "GET", "/api/bench/runs/"+idx.runs[0].Run.RunID, nil, nil).Body())
		}},
	}
	for _, rt := range routes {
		w4b04bClean(t, rt.name, rt.get())
	}
}

// TestServedEngineTextLocal: on the local server the same answers carry the
// text as stored.
func TestServedEngineTextLocal(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = nil
	c := w4b04bClient(t)
	er := w4b04bEvalServer(t)
	for name, body := range map[string]string{
		"GET /experiments/{id}": string(c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil).Body()),
		"GET /evaluations/{id}": string(evalCall(er, "GET", APIPrefix+"/evaluations/ev_plain", "").Response.Body()),
	} {
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if raw, _ := json.Marshal(v); !strings.Contains(string(raw), "/var/lib/heimdall/runs/orgs/org_01m7") {
			t.Errorf("%s: local answer changed: %s", name, body)
		}
	}
}
