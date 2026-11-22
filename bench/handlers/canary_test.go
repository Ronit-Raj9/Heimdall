package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeCanaries serves the fixture list and detail (generated from the
// canary package's scenario: a drifted canary on two providers).
type fakeCanaries struct {
	list    *canary.List
	detail  *canary.Detail
	busy    bool
	started []string
}

func loadCanaryFixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "canary", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func (f *fakeCanaries) List(context.Context) (*canary.List, error) { return f.list, nil }

func (f *fakeCanaries) Get(_ context.Context, name string) (*canary.Detail, error) {
	if name != f.detail.Summary.Name {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	return f.detail, nil
}

func (f *fakeCanaries) Start(_ context.Context, name string) (*canary.RunAccepted, error) {
	if name != f.detail.Summary.Name {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	if f.busy {
		return nil, fmt.Errorf("canary %s: %w", name, canary.ErrBusy)
	}
	f.started = append(f.started, name)
	return &canary.RunAccepted{Canary: name, Started: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC), Message: "started"}, nil
}

func newCanaryClient(t *testing.T, src CanarySource) *testClient {
	t.Helper()
	write := lib.RequireWriteToken(testMode, testToken)
	common := []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)}
	r := router.New()
	NewCanaryHandler(testMode, src, write).RegisterRoutes(r, common...)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestCanaryRoutesMatchContract(t *testing.T) {
	src := &fakeCanaries{list: &canary.List{}, detail: &canary.Detail{}}
	loadCanaryFixture(t, "list.json", src.list)
	loadCanaryFixture(t, "detail.json", src.detail)
	c := newCanaryClient(t, src)
	name := src.detail.Summary.Name
	tests := []struct {
		method, path string
		status       int
		typ          string
		nested       map[string]string
	}{
		{"GET", "/canaries", 200, "CanaryList", map[string]string{
			"canaries[]": "CanarySummary", "canaries[].targets[]": "CanaryTarget", "canaries[].baseline": "CanaryBaseline",
			"canaries[].baseline.served[]": "CanaryServed", "canaries[].last_look": "CanaryRecord", "errors[]": "CanaryDefinitionLoad",
		}},
		{"GET", "/canaries/" + name, 200, "CanaryDetail", map[string]string{
			"summary": "CanarySummary", "definition": "CanaryDefinition", "definition.budget": "CanaryBudget",
			"definition.schedule": "CanaryScheduleSpec", "baselines[]": "CanaryBaseline", "records[]": "CanaryRecord",
			"records[].comparison": "CanaryComparison", "records[].comparison.tests[]": "CanaryMetricTest",
			"records[].comparison.flips[]": "CanaryTaskFlip", "records[].changes": "CanaryChanges",
			"records[].changes.arms[]": "CanaryArmChanges", "records[].changes.new_clusters[]": "CanaryClusterRef",
			"records[].equivalence[]": "CanaryEquivalence", "records[].served[]": "CanaryServed", "records[].points[]": "CanaryArmPoint",
			"history[]": "TrendLineage", "history[].points[]": "TrendPoint", "pairs[]": "CanaryEquivalencePair",
		}},
		{"POST", "/canaries/" + name + "/run", 202, "CanaryRunAccepted", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var headers map[string]string
			if tt.method == "POST" {
				headers = authed("")
			}
			resp := c.do(t, tt.method, APIPrefix+tt.path, nil, headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			body := decodeBody(t, resp)
			checkKeys(t, tt.typ, body, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				for i, obj := range objectsAt(t, body, path) {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), obj, loadKeys(t, typ), false)
				}
			}
		})
	}
	if len(src.started) != 1 {
		t.Errorf("started = %v", src.started)
	}
}

func TestCanaryRouteErrors(t *testing.T) {
	src := &fakeCanaries{list: &canary.List{}, detail: &canary.Detail{}}
	loadCanaryFixture(t, "detail.json", src.detail)
	c := newCanaryClient(t, src)
	name := src.detail.Summary.Name
	cases := []struct {
		method, path string
		headers      map[string]string
		status       int
	}{
		{"GET", "/canaries/missing", nil, 404},
		{"GET", "/canaries/Bad..Name", nil, 404},
		{"POST", "/canaries/" + name + "/run", nil, 401}, // no write token
		{"POST", "/canaries/missing/run", authed(""), 404},
	}
	for _, cc := range cases {
		if resp := c.do(t, cc.method, APIPrefix+cc.path, nil, cc.headers); resp.StatusCode() != cc.status {
			t.Errorf("%s %s = %d, want %d: %s", cc.method, cc.path, resp.StatusCode(), cc.status, resp.Body())
		}
	}
	src.busy = true
	if resp := c.do(t, "POST", APIPrefix+"/canaries/"+name+"/run", nil, authed("")); resp.StatusCode() != 409 {
		t.Errorf("busy run = %d, want 409", resp.StatusCode())
	}
	// No source: 503.
	nc := newCanaryClient(t, nil)
	if resp := nc.do(t, "GET", APIPrefix+"/canaries", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("no source = %d, want 503", resp.StatusCode())
	}
}

// fw17Canary is a canary init and control env over a temp project with the
// agent file and one benchmark, coding-core@1.0.0.
func fw17Canary(t *testing.T) (*canaryInitHandler, *canaryControlHandler, string) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "agents"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "agents", "toy.yaml"), []byte("name: toy\n"), 0o644)
	env := OrgEnv{Cfg: &lib.Config{Root: root, RunsRoot: "runs"}, Services: Services{Suites: &fakeSuites{
		ids: []string{"coding-core"}, suites: map[string]*schemas.Suite{"coding-core": {ID: "coding-core", Version: "1.0.0"}},
	}}}
	return &canaryInitHandler{env: env}, &canaryControlHandler{env: env}, root
}

const fw17CanaryBody = `{"name":"nightly","agent":"agents/toy.yaml","suite":"coding-core@1.0.0","tasks":["t01"],"trials":1,"targets":[{"provider":"openai","model":"gpt-4o-mini"}],"budget":{"per_run_usd":0,"per_day_usd":0},"schedule":{"cron":"0 6 * * *"},"smoke":true}`

func TestCreateCanaryUnknownBenchmark422(t *testing.T) {
	h, _, _ := fw17Canary(t)
	ctx := schedCall(h.create, strings.Replace(fw17CanaryBody, "coding-core@1.0.0", "not-a-benchmark", 1), "")
	if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), "Benchmark not-a-benchmark does not exist. Pick one from the Benchmarks page.") {
		t.Fatalf("unknown benchmark: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = schedCall(h.create, strings.Replace(fw17CanaryBody, "coding-core@1.0.0", "coding-core@2.0.0", 1), "")
	if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), "Benchmark coding-core has no version 2.0.0.") {
		t.Fatalf("wrong version: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := schedCall(h.create, fw17CanaryBody, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("good benchmark: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestDeleteCanaryRemovesState(t *testing.T) {
	h, _, root := fw17Canary(t)
	if ctx := schedCall(h.create, fw17CanaryBody, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	state := filepath.Join(root, "runs", ".canaries", "nightly")
	other := filepath.Join(root, "runs", ".canaries", "other")
	for _, d := range []string{state, other} {
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "baselines.jsonl"), []byte("{}\n"), 0o644)
	}
	if ctx := schedCall(h.remove, "", "nightly"); ctx.Response.StatusCode() != 204 {
		t.Fatalf("delete: %d", ctx.Response.StatusCode())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state folder kept: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other canary's state removed: %v", err)
	}
}

func TestCanaryPauseResume(t *testing.T) {
	h, c, root := fw17Canary(t)
	if ctx := schedCall(h.create, fw17CanaryBody, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx := schedCall(c.pauseCanary, "", "nightly")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("pause: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var d canary.Definition
	if err := json.Unmarshal(ctx.Response.Body(), &d); err != nil || d.Schedule == nil || !d.Schedule.Paused {
		t.Fatalf("pause answer %s %v", ctx.Response.Body(), err)
	}
	if saved, err := canary.Load(canary.Path(root, "nightly")); err != nil || !saved.Schedule.Paused {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if ctx := schedCall(c.resumeCanary, "", "nightly"); ctx.Response.StatusCode() != 200 {
		t.Fatalf("resume: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if saved, _ := canary.Load(canary.Path(root, "nightly")); saved.Schedule.Paused {
		t.Fatal("still paused after resume")
	}
	// The Schedules tab pauses a drift monitor through its canary- row.
	if ctx := schedCall(c.pauseSchedule, "", "canary-nightly"); ctx.Response.StatusCode() != 200 {
		t.Fatalf("pause canary schedule: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if saved, _ := canary.Load(canary.Path(root, "nightly")); !saved.Schedule.Paused {
		t.Fatal("canary- schedule pause did not pause the drift monitor")
	}
	if ctx := schedCall(c.pauseCanary, "", "missing"); ctx.Response.StatusCode() != 404 {
		t.Fatalf("unknown: %d", ctx.Response.StatusCode())
	}
}

func TestPutCanaryEdits(t *testing.T) {
	h, c, root := fw17Canary(t)
	if ctx := schedCall(h.create, fw17CanaryBody, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := schedCall(c.pauseCanary, "", "nightly"); ctx.Response.StatusCode() != 200 {
		t.Fatalf("pause: %d", ctx.Response.StatusCode())
	}
	state := filepath.Join(root, "runs", ".canaries", "nightly")
	_ = os.MkdirAll(state, 0o755)
	_ = os.WriteFile(filepath.Join(state, "baselines.jsonl"), []byte("{}\n"), 0o644)
	// The name in the path wins over the body's.
	edit := strings.Replace(strings.Replace(fw17CanaryBody, `"t01"`, `"t01","t02"`, 1), `"name":"nightly"`, `"name":"other"`, 1)
	ctx := schedCall(c.edit, edit, "nightly")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("edit: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	saved, err := canary.Load(canary.Path(root, "nightly"))
	if err != nil || len(saved.Tasks.IDs) != 2 || saved.Name != "nightly" || !saved.Schedule.Paused {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if _, err := os.Stat(canary.Path(root, "other")); err == nil {
		t.Fatal("edit wrote the body's name")
	}
	if _, err := os.Stat(filepath.Join(state, "baselines.jsonl")); err != nil {
		t.Fatalf("edit dropped the state: %v", err)
	}
	if ctx := schedCall(c.edit, strings.Replace(fw17CanaryBody, "coding-core@1.0.0", "not-a-benchmark", 1), "nightly"); ctx.Response.StatusCode() != 422 {
		t.Fatalf("edit unknown benchmark: %d", ctx.Response.StatusCode())
	}
	if ctx := schedCall(c.edit, fw17CanaryBody, "missing"); ctx.Response.StatusCode() != 404 {
		t.Fatalf("edit unknown canary: %d", ctx.Response.StatusCode())
	}
}
