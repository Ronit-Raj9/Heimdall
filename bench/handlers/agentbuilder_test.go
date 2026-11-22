package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func builderRouter(t *testing.T) (*router.Router, projectfs.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil { // EnsureProject makes it in real orgs
		t.Fatal(err)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	h := &agentBuilder{mode: schemas.Mode("live"), projects: st}
	r.GET(APIPrefix+"/agents/templates", h.templates)
	r.POST(APIPrefix+"/agents/from-template", h.fromTemplate)
	r.POST(APIPrefix+"/agents/config/validate", h.validate)
	r.GET(APIPrefix+"/agents/{id}/config", h.getConfig)
	r.PUT(APIPrefix+"/agents/{id}/config", h.putConfig)
	return r, st
}

func abCall(r *router.Router, method, path string, body any) (int, []byte) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	if body != nil {
		b, _ := json.Marshal(body)
		ctx.Request.SetBody(b)
	}
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

var _ = lib.SendJSON

func TestEveryTemplateRendersAndValidates(t *testing.T) {
	params := map[string]map[string]string{
		"http-url":         {"url": "https://agent.example.com/run"},
		"a2a-card":         {"card_url": "https://agent.example.com/.well-known/agent-card.json"},
		"openai-endpoint":  {"url": "https://agent.example.com/v1", "model": "my-agent", "api_key_secret": "MY_AGENT_KEY"},
		"docker-image":     {"image": "ghcr.io/acme/agent:1", "command": "my-agent {{instruction_file}}"},
		"wrap-app":         {"repo_url": "https://github.com/org/agent.git", "command": "python3 -m app"},
		"vapi-assistant":   {"agent_id": "asst_1", "api_key_secret": "VAPI_KEY"},
		"retell-agent":     {"agent_id": "agent_1", "api_key_secret": "RETELL_KEY"},
		"elevenlabs-agent": {"agent_id": "agent_1"},
		"voice-websocket":  {"url": "wss://voice.example.com/twilio"},
		"livekit-agent":    {"dispatch": "agent", "agent_name": "support-agent"},
		"phone-number":     {"number": "+14155550100", "trunk_address": "acme.pstn.twilio.com", "from_number": "+14155550199"},
	}
	if len(agentTemplates) < 15 {
		t.Fatalf("want at least 15 templates, got %d", len(agentTemplates))
	}
	for _, tpl := range agentTemplates {
		req := AgentFromTemplateRequest{Template: tpl.ID, ID: "x-" + tpl.ID, Params: params[tpl.ID]}
		files, err := renderAgentTemplate(tpl, req)
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		cfg, err := decodeAgent(files[0].Data, "agents/x.yaml")
		if err != nil {
			t.Fatalf("%s: %v\n%s", tpl.ID, err, files[0].Data)
		}
		if cfg.ID != req.ID || cfg.Model != tpl.DefaultModel {
			t.Fatalf("%s: id %s model %s", tpl.ID, cfg.ID, cfg.Model)
		}
		if tpl.Builds && (len(files) < 2 || !strings.HasPrefix(cfg.Build.Dockerfile, "agents/images/"+req.ID+"/")) {
			t.Fatalf("%s: build not rewritten: %+v %d files", tpl.ID, cfg.Build, len(files))
		}
	}
}

func TestFromTemplateProviderRewrite(t *testing.T) {
	tpl, _ := findTemplate("langgraph")
	files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "lg", Model: &AgentModelRef{Provider: "anthropic", Model: "claude-x"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(files[0].Data, "agents/lg.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.Provider != "anthropic" || cfg.Env["AGENT_MODEL"] != "anthropic/{{model}}" || cfg.Model != "claude-x" {
		t.Fatalf("got %+v %v", cfg.Gateway, cfg.Env["AGENT_MODEL"])
	}
	codex, _ := findTemplate("codex")
	if _, err := renderAgentTemplate(codex, AgentFromTemplateRequest{ID: "c", Model: &AgentModelRef{Provider: "anthropic"}}); err == nil {
		t.Fatal("codex on anthropic must be refused")
	}
}

func TestFromTemplateCreateEditRoundTrip(t *testing.T) {
	r, st := builderRouter(t)
	code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "my-codex", Notes: "for tests"})
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if _, _, err := st.Read(context.Background(), projectfs.AreaAgents, "images/my-codex/Dockerfile"); err != nil {
		t.Fatal(err)
	}
	if code, _ := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "my-codex"}); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	if code, _ := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "../x"}); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
	code, body = abCall(r, "GET", APIPrefix+"/agents/my-codex/config", nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var doc AgentConfigDoc
	_ = json.Unmarshal(body, &doc)
	if doc.Fields.Adapter != "command" || !strings.Contains(doc.Fields.Description, "for tests") || !strings.Contains(doc.YAML, "model_providers.bifrost") {
		t.Fatalf("doc: %+v", doc.Fields)
	}
	f := doc.Fields
	f.WallS = 600
	code, body = abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{Fields: &f, IfMatch: doc.SHA256})
	if code != 200 {
		t.Fatalf("put fields: %d %s", code, body)
	}
	var doc2 AgentConfigDoc
	_ = json.Unmarshal(body, &doc2)
	if doc2.Fields.WallS != 600 || !strings.Contains(doc2.YAML, "Codex ignores") {
		t.Fatalf("round trip lost data: %+v", doc2.Fields)
	}
	if code, _ := abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{YAML: doc2.YAML, IfMatch: doc.SHA256}); code != 409 {
		t.Fatalf("stale if_match: %d", code)
	}
	if code, _ := abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{YAML: strings.Replace(doc2.YAML, "id: my-codex", "id: other", 1)}); code != 400 {
		t.Fatalf("rename: %d", code)
	}
	code, body = abCall(r, "POST", APIPrefix+"/agents/config/validate", AgentConfigValidateRequest{YAML: "id: x\nadapter: command\n"})
	var v AgentConfigValidateResponse
	_ = json.Unmarshal(body, &v)
	if code != 200 || v.OK || v.Error == "" {
		t.Fatalf("validate: %d %s", code, body)
	}
}

func TestFromTemplateNoStore(t *testing.T) {
	r := router.New()
	h := &agentBuilder{mode: schemas.Mode("live")}
	r.POST(APIPrefix+"/agents/from-template", h.fromTemplate)
	if code, _ := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "a"}); code != 503 {
		t.Fatalf("want 503, got %d", code)
	}
}
