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

	"github.com/valyala/fasthttp"
)

func TestAgentsCreateFromYAMLAndTemplate(t *testing.T) {
	r, dir := evalServer(t, Services{})
	doc := "id: support-bot\nadapter: remote_http\nremote:\n  url: https://agent.example.com/run\n"
	body, _ := json.Marshal(AgentCreateRequest{YAML: doc})
	ctx := evalCall(r, "POST", APIPrefix+"/agents", string(body))
	if ctx.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	got, err := os.ReadFile(filepath.Join(dir, "agents", "support-bot.yaml"))
	if err != nil || string(got) != doc {
		t.Fatalf("file %q %v", got, err)
	}
	if c := evalCall(r, "POST", APIPrefix+"/agents", string(body)); c.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("second create: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	// Agents as code: the same agent through PUT /agents/{id}/config (v1 PUT /agents/{id}).
	put, _ := json.Marshal(AgentConfigPut{YAML: strings.Replace(doc, "/run", "/v2/run", 1)})
	if c := evalCall(r, "PUT", APIPrefix+"/agents/support-bot/config", string(put)); c.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("update: %d %s", c.Response.StatusCode(), c.Response.Body())
	}

	dry, _ := json.Marshal(AgentCreateRequest{YAML: strings.Replace(doc, "support-bot", "dry-bot", 1), AgentFromTemplateRequest: AgentFromTemplateRequest{DryRun: true}})
	if c := evalCall(r, "POST", APIPrefix+"/agents", string(dry)); c.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("dry run: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "dry-bot.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote the file: %v", err)
	}

	tpl, _ := json.Marshal(AgentCreateRequest{AgentFromTemplateRequest: AgentFromTemplateRequest{Template: "http-url", ID: "from-tpl",
		Params: map[string]string{"url": "https://agent.example.com/answer"}}})
	if c := evalCall(r, "POST", APIPrefix+"/agents", string(tpl)); c.Response.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("template: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "from-tpl.yaml")); err != nil {
		t.Fatal(err)
	}

	for name, req := range map[string]AgentCreateRequest{
		"empty":    {},
		"both":     {YAML: doc, AgentFromTemplateRequest: AgentFromTemplateRequest{Template: "http-url"}},
		"mismatch": {YAML: strings.Replace(doc, "support-bot", "x1", 1), AgentFromTemplateRequest: AgentFromTemplateRequest{ID: "x2"}},
		"reserved": {YAML: strings.Replace(doc, "support-bot", "templates", 1)},
		"invalid":  {YAML: "id: bad\nadapter: nope-not-an-adapter\n"},
	} {
		b, _ := json.Marshal(req)
		if c := evalCall(r, "POST", APIPrefix+"/agents", string(b)); c.Response.StatusCode() != fasthttp.StatusBadRequest {
			t.Errorf("%s: %d %s", name, c.Response.StatusCode(), c.Response.Body())
		}
	}
}

// OAuth 2, Entra, SigV4 and Google sign-in are covered in remotemap's tests
// (they need an https token URL); this is the saved-agent route end to end.
func TestAgentsTestSavedMappedAgent(t *testing.T) {
	t.Setenv("Y10_KEY", "key-secret-value")
	t.Setenv("Y10_TOKEN", "static-token-value")
	var gotBody, gotKey, gotAuth string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotKey, gotAuth = string(b), r.Header.Get("X-Api-Key"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"messages":[{"role":"assistant","text":"READY"}]}}`))
	}))
	defer agent.Close()

	r, dir := evalServer(t, Services{})
	yaml := `id: mapped
adapter: remote_http
remote:
  url: ` + agent.URL + `/chat
  mapping:
    body: '{"query": "{{instruction}}", "conversation": "{{session_id}}"}'
    answer_path: output.messages[-1].text
    headers:
      X-Api-Key: "{{secret.Y10_KEY}}"
  auth_secret: Y10_TOKEN
`
	if err := os.WriteFile(filepath.Join(dir, "agents", "mapped.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := evalCall(r, "POST", APIPrefix+"/agents/mapped/test", "")
	var res EndpointTestResult
	if err := json.Unmarshal(ctx.Response.Body(), &res); err != nil {
		t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if !res.OK || res.Answer != "READY" {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(gotBody, `"query":`) || !strings.Contains(gotBody, EndpointTestInstruction[:10]) || gotKey != "key-secret-value" || gotAuth != "Bearer static-token-value" {
		t.Fatalf("agent saw body %q key %q auth %q", gotBody, gotKey, gotAuth)
	}
	if len(res.Requests) == 0 || !strings.Contains(res.RawResponse, "READY") || !strings.Contains(res.Reads, "output.messages[-1].text") {
		t.Fatalf("requests %+v raw %q reads %q", res.Requests, res.RawResponse, res.Reads)
	}
	out := string(ctx.Response.Body())
	for _, secret := range []string{"key-secret-value", "static-token-value"} {
		if strings.Contains(out, secret) {
			t.Fatalf("%s leaked into the result: %s", secret, out)
		}
	}

	if c := evalCall(r, "POST", APIPrefix+"/agents/nope/test", ""); c.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("missing agent: %d", c.Response.StatusCode())
	}
	if c := evalCall(r, "POST", APIPrefix+"/agents/mapped/test", `{"agent":"x"}`); c.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("body: %d", c.Response.StatusCode())
	}
}

func TestEndpointPresetsAndPreview(t *testing.T) {
	r, _ := evalServer(t, Services{})
	ctx := evalCall(r, "GET", APIPrefix+"/agents/endpoint-presets", "")
	var list EndpointPresetList
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil || len(list.Presets) < 10 {
		t.Fatalf("presets %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ids := map[string]bool{}
	for _, p := range list.Presets {
		ids[p.ID] = true
	}
	for _, want := range []string{"langgraph", "adk", "mastra", "n8n", "openai_responses", "bedrock_agent", "agentforce", "copilot_studio", "vercel_ai", "chat_completions"} {
		if !ids[want] {
			t.Errorf("preset %s missing", want)
		}
	}

	preview := func(params map[string]string) EndpointPreview {
		b, _ := json.Marshal(EndpointTestRequest{Template: &AgentFromTemplateRequest{Template: "http-url", ID: "bot", Params: params}})
		c := evalCall(r, "POST", APIPrefix+"/agents/endpoint-preview", string(b))
		if c.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("preview %d %s", c.Response.StatusCode(), c.Response.Body())
		}
		var p EndpointPreview
		_ = json.Unmarshal(c.Response.Body(), &p)
		return p
	}
	p := preview(map[string]string{"preset": "langgraph", "preset_assistant_id": "support", "url": "https://d.us.langgraph.app/runs/wait"})
	if !p.OK || len(p.Requests) == 0 || !strings.Contains(p.Requests[len(p.Requests)-1].Body, `"assistant_id":"support"`) && !strings.Contains(p.Requests[len(p.Requests)-1].Body, `"assistant_id": "support"`) {
		t.Fatalf("langgraph preview %+v", p)
	}
	if !strings.Contains(p.Reads, "messages[-1].content") {
		t.Fatalf("reads %q", p.Reads)
	}
	sp := preview(map[string]string{"url": "https://x.example/run", "headers": "X-Api-Key: {{secret.UNSET_Y10_KEY}}", "answer_path": "data.reply"})
	if !sp.OK || !strings.Contains(sp.Reads, "data.reply") {
		t.Fatalf("secret header preview %+v", sp)
	}
	shown := false
	for _, h := range sp.Requests[0].Headers {
		shown = shown || (h[0] == "X-Api-Key" && strings.Contains(h[1], "UNSET_Y10_KEY"))
	}
	if !shown {
		t.Fatalf("secret header not shown by name: %+v", sp.Requests[0].Headers)
	}
	if p := preview(map[string]string{"url": "https://x.example/run", "answer_path": "a.[["}); p.OK || p.Error == "" {
		t.Fatalf("bad path preview %+v", p)
	}
	if p := preview(map[string]string{}); p.OK || p.Error == "" {
		t.Fatalf("empty form preview %+v", p)
	}
}
