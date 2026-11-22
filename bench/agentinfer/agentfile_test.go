package agentinfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodFile = `version: 1
id: support-bot
name: Support bot
framework: langgraph
model: {provider: openai, model: gpt-5.4-mini}
connect:
  template: http-url
  url: https://support.example.com/chat
  auth_secret: SUPPORT_BOT_TOKEN
  tools: true
  timeout_s: 300
  tool_allow: [crm, tickets]
capabilities: [tools, sessions]
tools: [lookup_order, refund]
evaluate: {benchmarks: [support-basics], preset: first_look}
`

func TestAgentFileToRequest(t *testing.T) {
	f, err := ParseAgentFile([]byte(goodFile))
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.ToRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Template != "http-url" || req.ID != "support-bot" || req.Model.Model != "gpt-5.4-mini" || req.Model.Provider != "openai" {
		t.Fatalf("%+v", req)
	}
	want := map[string]string{"url": "https://support.example.com/chat", "auth_secret": "SUPPORT_BOT_TOKEN", "tools": "true", "timeout_s": "300", "tool_allow": "crm, tickets"}
	for k, v := range want {
		if req.Params[k] != v {
			t.Fatalf("param %s = %q, want %q", k, req.Params[k], v)
		}
	}
	if len(req.Params) != len(want) {
		t.Fatalf("params %v", req.Params)
	}
}

func TestAgentFileRefusals(t *testing.T) {
	cases := map[string]string{
		"version: 2\nid: a\nconnect: {template: http-url}\n":                                   "version: 2",
		"id: Bad ID\nconnect: {template: http-url}\n":                                          "id:",
		"id: a\n":                                                                              "connect:",
		"id: a\nconnect: {template: http-url}\nconfig: {adapter: command}\n":                   "not both",
		"id: a\nconnect: {template: http-url, token: abc}\n":                                   "never holds them",
		"id: a\nconnect: {template: http-url, auth_secret: sk-abcdefghijklmnopqrstuvwx}\n":     "not a secret name",
		"id: a\nconnect: {template: http-url, url: 'https://x.io/?k=sk-abcdefghijklmnopqrstuvwx'}\n": "looks like a key",
		"id: a\nconfig: {adapter: command, env: {OPENAI_API_KEY: sk-abcdefghijklmnopqrstuvwx}}\n":   "looks like a key",
		"id: a\nconnect: {template: http-url}\nunknown: 1\n":                                   "field unknown not found",
		"id: a\nconnect: {template: http-url}\nevaluate: {preset: huge}\n":                     "evaluate.preset",
	}
	for in, want := range cases {
		_, err := ParseAgentFile([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", in, err, want)
		}
	}
}

func TestAgentFileConfigAndLoad(t *testing.T) {
	root := t.TempDir()
	if _, _, err := LoadAgentFile(root); err != ErrNoAgentFile {
		t.Fatalf("missing: %v", err)
	}
	os.MkdirAll(filepath.Join(root, ".heimdall"), 0o755)
	os.WriteFile(filepath.Join(root, ".heimdall", "agent.yml"), []byte("id: mine\nconfig:\n  adapter: command\n  image: bench/agent-mine:1\n  argv: [run]\n"), 0o644)
	f, p, err := LoadAgentFile(root)
	if err != nil || !strings.HasSuffix(p, "agent.yml") {
		t.Fatal(err, p)
	}
	y, err := f.ConfigYAML()
	if err != nil || !strings.Contains(y, "id: mine") || !strings.Contains(y, "adapter: command") {
		t.Fatalf("%v %s", err, y)
	}
	if _, err := f.ToRequest(); err == nil {
		t.Fatal("config file gave a template request")
	}
}

func TestFileFromDraftRoundTrip(t *testing.T) {
	body := otlpJSON("support-svc", "openinference.instrumentation.langchain", supportBot(t1, "c-1")...)
	d, err := InferFromOTLP(body, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	f := FileFromDraft(d)
	b, err := f.Marshal(d.Questions...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# TODO: Does support.example.com need a token?") {
		t.Fatalf("%s", b)
	}
	back, err := ParseAgentFile(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	req, _ := back.ToRequest()
	if req.Template != TemplateHTTP || req.Params["url"] != "https://support.example.com/chat" || strings.Join(back.Tools, ",") != "lookup_order,refund" {
		t.Fatalf("%+v %+v", req, back)
	}
}
