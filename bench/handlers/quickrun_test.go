package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func quickRunServer(t *testing.T, s Services) (*router.Router, string) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"agents/codex.yaml":     "id: codex\n",
		"policies/default.yaml": "name: default\n",
	})
	ps, err := projectfs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Projects == nil {
		s.Projects = ps
	}
	r := router.New()
	env := OrgEnv{Cfg: &lib.Config{Root: dir}, Mode: schemas.ModeRecorded, Services: s}
	for _, a := range OrgAreas() {
		a(env, r)
	}
	return r, dir
}

func postQuick(r *router.Router, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(APIPrefix + "/quickrun")
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	return ctx
}

func TestQuickRunEndToEnd(t *testing.T) {
	l := &fakeLauncher{}
	suites := &fakeSuites{suites: map[string]*schemas.Suite{
		"coding-core": {ID: "coding-core", Version: "1.0.0", TaskIDs: []string{"t03", "t01", "t02", "t04", "t05", "t06", "t07"}},
	}}
	r, _ := quickRunServer(t, Services{Launcher: l, Suites: suites})
	ctx := postQuick(r, `{"model":{"provider":"openai","model":"gpt-5.4-mini"},"tasks":3}`)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var resp QuickRunResponse
	if err := json.Unmarshal(ctx.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ExpID != testExp || !resp.Started || l.started != testExp || l.created == nil {
		t.Fatalf("resp %+v started %q", resp, l.started)
	}
	if !strings.HasPrefix(resp.Path, "experiments/quick-") || !strings.HasSuffix(resp.Path, ".yaml") {
		t.Fatalf("path %q", resp.Path)
	}
	exp := l.planned
	if exp.Suite != "coding-core@1.0.0" || exp.Base != "agents/codex.yaml" || exp.Policy != "policies/default.yaml" {
		t.Fatalf("experiment %+v", exp)
	}
	if got := exp.Conditions[0].Tasks.IDs; strings.Join(got, ",") != "t01,t02,t03" {
		t.Fatalf("tasks %v", got)
	}
	// One model is one arm scored on its own: no hidden repeat arm B.
	if len(exp.Arms) != 1 || exp.Arms["A"].Set["model"] != "gpt-5.4-mini" || exp.Arms["A"].Set["gateway.provider"] != "openai" {
		t.Fatalf("arms %+v", exp.Arms)
	}
	for _, a := range exp.Arms {
		if _, ok := a.Set["env.HEIMDALL_QUICKRUN_ARM"]; ok {
			t.Fatalf("hidden repeat marker on arm %+v", a)
		}
	}
}

func TestQuickRunFileSaved(t *testing.T) {
	l := &fakeLauncher{}
	r, dir := quickRunServer(t, Services{Launcher: l})
	ctx := postQuick(r, `{"agent":"codex","model":{"provider":"anthropic","model":"claude-x"},"compare_model":{"provider":"openai","model":"gpt-5.4"},"start":false}`)
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var resp QuickRunResponse
	_ = json.Unmarshal(ctx.Response.Body(), &resp)
	if resp.Started || l.started != "" {
		t.Fatalf("start:false still started")
	}
	ps, _ := projectfs.Open(dir)
	data, _, err := ps.Read(t.Context(), "experiments", strings.TrimPrefix(resp.Path, "experiments/"))
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	exp, _, err := DecodeExperiment("application/yaml", data)
	if err != nil {
		t.Fatalf("saved file does not decode: %v\n%s", err, data)
	}
	if !exp.Conditions[0].Tasks.All || exp.Arms["B"].Set["model"] != "gpt-5.4" {
		t.Fatalf("saved experiment %+v", exp)
	}
}

func TestQuickRunErrors(t *testing.T) {
	r, _ := quickRunServer(t, Services{Launcher: &fakeLauncher{}})
	cases := map[string]int{
		`{}`: fasthttp.StatusBadRequest,
		`{"model":{"provider":"openai","model":"m"},"agent":"../../etc/passwd"}`: fasthttp.StatusBadRequest,
		`{"model":{"provider":"openai","model":"m"},"agent":"missing"}`:          fasthttp.StatusNotFound,
		`{"model":{"provider":"evil","model":"m"}}`:                              fasthttp.StatusBadRequest,
		`{"model":{"provider":"openai","model":"m"},"tasks":500}`:                fasthttp.StatusBadRequest,
		`{"model":{"provider":"openai","model":"m"},"bogus":1}`:                  fasthttp.StatusBadRequest,
	}
	for body, want := range cases {
		if got := postQuick(r, body).Response.StatusCode(); got != want {
			t.Errorf("%s: status %d, want %d", body, got, want)
		}
	}
	r2, _ := quickRunServer(t, Services{})
	if got := postQuick(r2, `{"model":{"provider":"openai","model":"m"}}`).Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
		t.Errorf("no launcher: status %d, want 503", got)
	}
}
