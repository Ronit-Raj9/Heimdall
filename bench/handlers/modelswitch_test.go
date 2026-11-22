package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/modelswitch"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func modelSwitchServe(t *testing.T, h *modelSwitchHandler, method, uri, body string) (int, []byte) {
	t.Helper()
	r := router.New()
	p := APIPrefix + "/evaluations/{id}/model-switch"
	r.GET(p, h.getReport)
	r.GET(p+"/bifrost-config", h.getConfig)
	r.POST(p+"/apply", h.apply)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if body != "" {
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.SetBodyString(body)
	}
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), ctx.Response.Body()
}

func modelSwitchEnv(t *testing.T, ev schemas.Evaluation) (*modelSwitchHandler, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, ev.ID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}
	h := &modelSwitchHandler{env: env, evals: NewFileEvaluations(root)}
	return h, root
}

func TestModelSwitchRoutes(t *testing.T) {
	ev := schemas.Evaluation{ID: "ev_ms", Status: "done", CreatedAt: time.Now(),
		Spec: schemas.EvaluationSpec{Mode: schemas.EvaluationModeModelSwitch, Trials: 1, Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "codex", Model: "gpt-5.4-mini", Provider: "openai"},
			{Label: "B", Agent: "codex", Model: "gpt-5.4", Provider: "openai"},
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: "done"}},
	}
	h, _ := modelSwitchEnv(t, ev)

	code, body := modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_ms/model-switch", "")
	var rep modelswitch.Report
	if code != fasthttp.StatusOK || json.Unmarshal(body, &rep) != nil || rep.Evaluation != "ev_ms" || len(rep.Models) != 2 || rep.Recommendation.Summary == "" {
		t.Fatalf("report %d %s", code, body)
	}
	if code, _ := modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_none/model-switch", ""); code != fasthttp.StatusNotFound {
		t.Fatalf("missing evaluation: %d", code)
	}
	if code, _ := modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_ms/model-switch?alpha=2", ""); code != fasthttp.StatusBadRequest {
		t.Fatalf("bad alpha: %d", code)
	}

	code, body = modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_ms/model-switch/bifrost-config?scope=virtual_key&scope_id=vk-1", "")
	var ex modelswitch.Export
	if code != fasthttp.StatusOK || json.Unmarshal(body, &ex) != nil || ex.Options.ScopeID != "vk-1" {
		t.Fatalf("config %d %s", code, body)
	}
	code, body = modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_ms/model-switch/bifrost-config?download=1", "")
	var cfg modelswitch.BifrostConfig
	if code != fasthttp.StatusOK || json.Unmarshal(body, &cfg) != nil || cfg.Schema != modelswitch.BifrostSchemaURL || strings.Contains(string(body), `"mode"`) {
		t.Fatalf("download %d %s", code, body)
	}
	if code, _ := modelSwitchServe(t, h, "GET", APIPrefix+"/evaluations/ev_ms/model-switch/bifrost-config?scope=team", ""); code != fasthttp.StatusBadRequest {
		t.Fatalf("team scope without id: %d", code)
	}

	// Apply against a fake gateway (local mode reaches loopback).
	var writes int
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"rules":[]}`))
			return
		}
		writes++
		_, _ = w.Write([]byte(`{"rule":{"id":"x"}}`))
	}))
	defer gw.Close()
	h.gateway = func(base, cred string, public bool) modelswitch.Gateway {
		if public {
			t.Error("local mode used the public client")
		}
		return modelswitch.Gateway{BaseURL: base, Credential: cred, Client: gw.Client()}
	}
	code, body = modelSwitchServe(t, h, "POST", APIPrefix+"/evaluations/ev_ms/model-switch/apply", `{"gateway_url":"`+gw.URL+`","scope":"virtual_key","scope_id":"vk-1","dry_run":true}`)
	if code != fasthttp.StatusOK || writes != 0 || !strings.Contains(string(body), `"dry_run":true`) {
		t.Fatalf("dry run %d %s writes %d", code, body, writes)
	}
	code, body = modelSwitchServe(t, h, "POST", APIPrefix+"/evaluations/ev_ms/model-switch/apply", `{"gateway_url":"`+gw.URL+`"}`)
	if code != fasthttp.StatusBadRequest || !strings.Contains(string(body), "allow_global") {
		t.Fatalf("global without confirm %d %s", code, body)
	}
	code, body = modelSwitchServe(t, h, "POST", APIPrefix+"/evaluations/ev_ms/model-switch/apply", `{"gateway_url":"`+gw.URL+`","credential_secret":"BF_ADMIN","allow_global":true}`)
	if code != fasthttp.StatusBadRequest || !strings.Contains(string(body), "BF_ADMIN") {
		t.Fatalf("missing secret %d %s", code, body)
	}
	code, body = modelSwitchServe(t, h, "POST", APIPrefix+"/evaluations/ev_ms/model-switch/apply", `{"gateway_url":"`+gw.URL+`","allow_global":true,"bogus":1}`)
	if code != fasthttp.StatusBadRequest {
		t.Fatalf("unknown field %d %s", code, body)
	}
	h.env.Services.Hosted = true
	code, body = modelSwitchServe(t, h, "POST", APIPrefix+"/evaluations/ev_ms/model-switch/apply", `{"gateway_url":"`+gw.URL+`","allow_global":true}`)
	if code != fasthttp.StatusBadRequest || !strings.Contains(string(body), "https") {
		t.Fatalf("hosted reaches a local http gateway %d %s", code, body)
	}

	// Two agents are not a model switch.
	ev.ID = "ev_two"
	ev.Spec.Arms[1].Agent = "claude"
	h2, _ := modelSwitchEnv(t, ev)
	if code, body := modelSwitchServe(t, h2, "GET", APIPrefix+"/evaluations/ev_two/model-switch", ""); code != fasthttp.StatusBadRequest {
		t.Fatalf("two agents %d %s", code, body)
	}
	if code, _ := modelSwitchServe(t, &modelSwitchHandler{env: OrgEnv{Mode: testMode}}, "GET", APIPrefix+"/evaluations/ev_ms/model-switch", ""); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no index: %d", code)
	}
}

func TestCheckModelSwitchArms(t *testing.T) {
	arm := func(agent, model string) schemas.EvaluationArm {
		return schemas.EvaluationArm{Agent: agent, Model: model}
	}
	ok := []schemas.EvaluationArm{arm("bot", "a"), arm("agents/bot.yaml", "b"), arm("bot", "")}
	if err := checkModelSwitchArms(ok); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]schemas.EvaluationArm{
		"one":       {arm("bot", "a")},
		"agents":    {arm("bot", "a"), arm("other", "b")},
		"same":      {arm("bot", "a"), arm("bot", "a")},
		"two blank": {arm("bot", ""), arm("bot", "")},
		"seven":     {arm("bot", "1"), arm("bot", "2"), arm("bot", "3"), arm("bot", "4"), arm("bot", "5"), arm("bot", "6"), arm("bot", "7")},
	}
	for name, arms := range bad {
		if err := checkModelSwitchArms(arms); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	v := []schemas.EvaluationArm{arm("bot", "a"), arm("bot", "b")}
	v[1].Version = 3
	if err := checkModelSwitchArms(v); err == nil {
		t.Error("two versions accepted")
	}
}

func TestTaskSegments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task.yaml"), []byte("id: t1\nmetadata:\n  tier: enterprise\n  tag: ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := taskSegments(&schemas.Task{ID: "t1", Kind: "code", Tags: []string{"sql", "joins"}, Difficulty: "hard", Dir: dir})
	if got["tier"] != "enterprise" || got["task_kind"] != "code" || got["tag"] != "sql\njoins" || got["difficulty"] != "hard" {
		t.Fatalf("segments %v", got)
	}
}
