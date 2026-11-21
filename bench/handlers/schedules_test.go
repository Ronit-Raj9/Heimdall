package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
)

func schedEnv(t *testing.T) (*schedulesHandler, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "experiments", "n.yaml"), []byte("name: n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &schedulesHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}}}, root
}

func schedCall(h fasthttp.RequestHandler, body string, name string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetBodyString(body)
	if name != "" {
		ctx.SetUserValue("name", name)
	}
	h(ctx)
	return ctx
}

func TestSchedulesPutGetRoundTrip(t *testing.T) {
	h, root := schedEnv(t)
	body := `{"file":{"alerts":[{"url_env":"HOOK_URL","format":"slack"}],"schedules":[{"name":"nightly","experiment":"experiments/n.yaml","cron":"0 6 * * *","max_usd_per_day":5}]}}`
	ctx := schedCall(h.put, body, "")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("put: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	f, err := LoadWebSchedules(root)
	if err != nil || len(f.Schedules) != 1 || f.Alerts[0].URLEnv != "HOOK_URL" {
		t.Fatalf("saved file %+v %v", f, err)
	}
	var resp SchedulesResponse
	if err := json.Unmarshal(ctx.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].Next == "" || resp.Rows[0].Kind != "experiment" {
		t.Fatalf("rows %+v", resp.Rows)
	}
}

func TestSchedulesPutRefuses(t *testing.T) {
	h, _ := schedEnv(t)
	for name, body := range map[string]string{
		"bad cron":     `{"file":{"schedules":[{"name":"a","experiment":"experiments/n.yaml","cron":"nope"}]}}`,
		"missing exp":  `{"file":{"schedules":[{"name":"a","experiment":"experiments/x.yaml","cron":"0 6 * * *"}]}}`,
		"url in env":   `{"file":{"alerts":[{"url_env":"https://hooks.example.com/x"}],"schedules":[]}}`,
		"reserved":     `{"file":{"schedules":[{"name":"canary-x","experiment":"experiments/n.yaml","cron":"0 6 * * *"}]}}`,
		"unknown yaml": `{"yaml":"schedules: []\nmax_usd: 3\n"}`,
	} {
		ctx := schedCall(h.put, body, "")
		if ctx.Response.StatusCode() < 400 {
			t.Errorf("%s: status %d", name, ctx.Response.StatusCode())
		}
	}
}

func TestTestAlertDeliversLocally(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("X-Heimdall-Event")
	}))
	defer srv.Close()
	t.Setenv("L17_TEST_HOOK", srv.URL)
	h, _ := schedEnv(t)
	ctx := schedCall(h.testAlert, `{"url_env":"L17_TEST_HOOK"}`, "")
	var resp TestAlertResponse
	_ = json.Unmarshal(ctx.Response.Body(), &resp)
	if !resp.Delivered {
		t.Fatalf("not delivered: %s", ctx.Response.Body())
	}
	if ev := <-got; ev == "" {
		t.Fatal("no event header")
	}
}

func TestAlertGetenvHostedIgnoresEnvAndPrivateURLs(t *testing.T) {
	t.Setenv("L17_SECRET_ENV", "https://example.com/hook")
	if v := AlertGetenv(t.Context(), Services{Hosted: true})("L17_SECRET_ENV"); v != "" {
		t.Fatalf("hosted read server env: %q", v)
	}
	for raw, want := range map[string]bool{"https://hooks.slack.com/x": true, "http://example.com": false, "https://127.0.0.1/x": false, "https://10.0.0.1": false, "https://localhost/x": false} {
		if publicHTTPS(raw) != want {
			t.Errorf("publicHTTPS(%s) != %v", raw, want)
		}
	}
}

func TestCanaryCreateAndDelete(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "agents"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "agents", "toy.yaml"), []byte("name: toy\n"), 0o644)
	h := &canaryInitHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}}}
	body := `{"name":"nightly","agent":"agents/toy.yaml","suite":"coding-core@1.0.0","tasks":["t01"],"trials":1,"targets":[{"provider":"openai","model":"gpt-4o-mini"}],"budget":{"per_run_usd":0,"per_day_usd":0},"smoke":true}`
	if ctx := schedCall(h.create, body, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if ctx := schedCall(h.create, body, ""); ctx.Response.StatusCode() != 409 {
		t.Fatalf("second create: %d", ctx.Response.StatusCode())
	}
	if ctx := schedCall(h.create, strings.Replace(body, "agents/toy.yaml", "../etc/passwd", 1), ""); ctx.Response.StatusCode() != 422 {
		t.Fatalf("escape: %d", ctx.Response.StatusCode())
	}
	if ctx := schedCall(h.remove, "", "nightly"); ctx.Response.StatusCode() != 204 {
		t.Fatalf("delete: %d", ctx.Response.StatusCode())
	}
	if ctx := schedCall(h.remove, "", "../x"); ctx.Response.StatusCode() != 404 {
		t.Fatalf("bad name delete: %d", ctx.Response.StatusCode())
	}
}

func TestFireJobFor(t *testing.T) {
	c := schedule.Schedule{Name: "canary-x", Experiment: "canaries/x.yaml"}
	if k, p := FireJobFor(c, true); k != "canary.run" || !strings.Contains(string(p), `"x"`) {
		t.Fatalf("hosted canary: %s %s", k, p)
	}
	if k, _ := FireJobFor(c, false); k != KindScheduleFire {
		t.Fatalf("local: %s", k)
	}
}
