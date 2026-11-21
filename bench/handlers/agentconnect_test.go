package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

func TestConnectHTTPFormRendersAuthAndTools(t *testing.T) {
	tpl, _ := findTemplate("http-url")
	files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "bot", Params: map[string]string{
		"url": "https://agent.example.com/run", "auth_secret": "BOT_TOKEN", "auth_header": "X-API-Key", "sign_secret": "BOT_SIGN",
		"tools": "true", "tool_allow": "crm, tickets", "artifacts": "true", "model_traffic": "true", "timeout_s": "120",
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(files[0].Data, "agents/bot.yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, files[0].Data)
	}
	r := cfg.Remote
	if r.AuthSecret != "BOT_TOKEN" || r.AuthHeader != "X-API-Key" || r.SignSecret != "BOT_SIGN" || r.Tools != schemas.RemoteToolsExpose ||
		strings.Join(r.ToolAllow, ",") != "crm,tickets" || r.Artifacts != "inline" || r.ModelTraffic != "gateway" || r.TimeoutS != 120 {
		t.Fatalf("remote %+v", r)
	}
	bad := []map[string]string{
		{"url": "https://x", "auth_secret": "bad name"},
		{"url": "https://x", "auth_header": "X-Key"},
		{"url": "https://x", "tool_allow": "crm"},
		{"url": "https://x", "timeout_s": "1"},
	}
	for _, p := range bad {
		if _, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "bot", Params: p}); err == nil {
			t.Errorf("%v: want error", p)
		}
	}
}

func TestWrapAppTemplate(t *testing.T) {
	tpl, ok := findTemplate("wrap-app")
	if !ok {
		t.Fatal("no wrap-app template")
	}
	files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "support", Params: map[string]string{
		"framework": "langgraph", "repo_url": "https://github.com/org/support-agent.git", "repo_ref": "v2",
		"command": "uvicorn app:api --port 9000", "port": "9000", "health_path": "/ok", "env": "LOG_LEVEL=info\n# c\n", "secrets": "CRM_KEY",
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(files[0].Data, "agents/support.yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, files[0].Data)
	}
	if cfg.Env["AGENT_SERVER_CMD"] != "uvicorn app:api --port 9000" || cfg.Env["AGENT_HEALTH_URL"] != "http://127.0.0.1:9000/ok" ||
		cfg.Env["AGENT_RUN_URL"] != "http://127.0.0.1:9000/run" || cfg.Env["AGENT_FRAMEWORK"] != "langgraph" ||
		cfg.Env["LOG_LEVEL"] != "info" || cfg.Env["CRM_KEY"] != "{{secret.CRM_KEY}}" {
		t.Fatalf("env %+v", cfg.Env)
	}
	if len(files) != 2 || files[1].Rel != "images/support/Dockerfile" || !strings.Contains(string(files[1].Data), "ADD --keep-git-dir=false https://github.com/org/support-agent.git#v2 /opt/agent") {
		t.Fatalf("files %v", files)
	}
	if cfg.Build == nil || cfg.Build.Dockerfile != "agents/images/support/Dockerfile" {
		t.Fatalf("build %+v", cfg.Build)
	}
	img, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "img", Params: map[string]string{"image": "ghcr.io/org/a@sha256:" + strings.Repeat("a", 64), "command": "python3 -m app"}})
	if err != nil {
		t.Fatal(err)
	}
	icfg, err := decodeAgent(img[0].Data, "agents/img.yaml")
	if err != nil || icfg.Build != nil || !strings.HasPrefix(icfg.Image, "ghcr.io/org/a@") || len(img) != 1 {
		t.Fatalf("image variant: %v %+v %d", err, icfg, len(img))
	}
	for _, p := range []map[string]string{
		{"command": "x"},
		{"command": "x", "image": "a:1", "repo_url": "https://g/x"},
		{"command": "x", "repo_url": "http://g/x"},
		{"command": "x", "image": "a:1", "env": "BENCH_X=1"},
		{"command": "x", "image": "a:1", "framework": "django"},
		{"command": "x", "image": "a:1", "health_path": "nope"},
	} {
		if _, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "w", Params: p}); err == nil {
			t.Errorf("%v: want error", p)
		}
	}
}

func TestEvaluationOwnModel(t *testing.T) {
	l := &evalLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	if err := os.WriteFile(filepath.Join(dir, "agents", "bot.yaml"), []byte("id: bot\nadapter: remote_http\nimage: bench/coding-core-base:1\nremote:\n  url: https://x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"bot","model":""}],"benchmarks":["longmemeval"]}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	exp := l.planned[ev.Children[0].ExperimentID]
	if exp == nil {
		t.Fatalf("children %+v", ev.Children)
	}
	if _, set := exp.Arms["A"].Set["model"]; set || !strings.Contains(exp.Arms["A"].Label, "agent's own model") {
		t.Fatalf("arm A %+v", exp.Arms["A"])
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations", `{"arms":[{"agent":"scripted","model":""}],"benchmarks":["longmemeval"]}`).Response.StatusCode(); got != fasthttp.StatusBadRequest {
		t.Fatalf("blank model on a command agent: %d", got)
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations", `{"mode":"models","arms":[{"agent":"bot","model":""},{"agent":"bot","model":""}],"benchmarks":["longmemeval"]}`).Response.StatusCode(); got != fasthttp.StatusBadRequest {
		t.Fatalf("identical arms: %d", got)
	}
}

func TestEndpointTestRemote(t *testing.T) {
	t.Setenv("X10_TOKEN", "tok-123")
	t.Setenv("X10_SIGN", "0123456789abcdef-sign")
	var gotAuth, gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotAuth = r.Header.Get("X-API-Key")
		ts := r.Header.Get("X-Heimdall-Timestamp")
		var n int64
		for _, c := range ts {
			n = n*10 + int64(c-'0')
		}
		if r.Header.Get("X-Heimdall-Signature") == webhooks.Sign("0123456789abcdef-sign", n, body) {
			gotSig = "ok"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"READY","usage":{"total_tokens":12},"events":[{"type":"tool_call","name":"search"}],"version":"1.2"}`))
	}))
	defer srv.Close()
	r, _ := evalServer(t, Services{})
	body, _ := json.Marshal(EndpointTestRequest{Template: &AgentFromTemplateRequest{Template: "http-url", ID: "bot", Params: map[string]string{
		"url": srv.URL + "/run", "auth_secret": "X10_TOKEN", "auth_header": "X-API-Key", "sign_secret": "X10_SIGN"}}})
	ctx := evalCall(r, "POST", APIPrefix+"/agents/test-endpoint", string(body))
	var res EndpointTestResult
	if err := json.Unmarshal(ctx.Response.Body(), &res); err != nil {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if !res.OK || !res.Matched || res.Answer != "READY" || gotAuth != "tok-123" || gotSig != "ok" || res.Status != 200 {
		t.Fatalf("result %+v auth %q sig %q", res, gotAuth, gotSig)
	}
	kinds := ""
	for _, s := range res.Trace {
		kinds += s.Kind + ","
	}
	if kinds != "request,response,event,answer," || res.Reported["version"] != "1.2" {
		t.Fatalf("trace %s reported %v", kinds, res.Reported)
	}
	if strings.Contains(string(ctx.Response.Body()), "tok-123") {
		t.Fatal("token leaked into the result")
	}
}

func TestEndpointTestFailuresAndHosted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("no"))
	}))
	defer srv.Close()
	call := func(s Services, params map[string]string) EndpointTestResult {
		r, _ := evalServer(t, s)
		body, _ := json.Marshal(EndpointTestRequest{Template: &AgentFromTemplateRequest{Template: "http-url", ID: "bot", Params: params}})
		ctx := evalCall(r, "POST", APIPrefix+"/agents/test-endpoint", string(body))
		var res EndpointTestResult
		_ = json.Unmarshal(ctx.Response.Body(), &res)
		return res
	}
	if res := call(Services{}, map[string]string{"url": srv.URL}); res.OK || !strings.Contains(res.Error, "answered 401") {
		t.Fatalf("401: %+v", res)
	}
	if res := call(Services{}, map[string]string{"url": srv.URL, "auth_secret": "X10_UNSET_SECRET"}); res.OK || !strings.Contains(res.Error, "Settings > Secrets") {
		t.Fatalf("missing secret: %+v", res)
	}
	if res := call(Services{Hosted: true}, map[string]string{"url": srv.URL}); res.OK || !strings.Contains(res.Error, "https") {
		t.Fatalf("hosted http: %+v", res)
	}
	if res := call(Services{Hosted: true}, map[string]string{"url": "https://127.0.0.1/run"}); res.OK || !strings.Contains(res.Error, "private or local") {
		t.Fatalf("hosted loopback: %+v", res)
	}
}
