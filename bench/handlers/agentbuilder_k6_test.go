package handlers

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agents"
)

// Every built-in agent file and every template must come back from the form
// writer with the same parsed config: the form sends back what it read.
func TestFormWriterRoundTripsEveryAgentAndTemplate(t *testing.T) {
	check := func(name string, doc []byte) {
		t.Helper()
		cfg, err := decodeAgent(doc, "agents/x.yaml")
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		out, err := abApplyFields(doc, abFieldsOf(cfg, doc))
		if err != nil {
			t.Fatalf("%s: apply: %v", name, err)
		}
		cfg2, err := decodeAgent(out, "agents/x.yaml")
		if err != nil {
			t.Fatalf("%s: decode after form save: %v\n%s", name, err, out)
		}
		if !reflect.DeepEqual(cfg, cfg2) {
			t.Fatalf("%s: config changed by form save\nbefore %+v\nafter  %+v", name, cfg, cfg2)
		}
	}
	ents, err := fs.Glob(agents.Files, "*.yaml")
	if err != nil || len(ents) == 0 {
		t.Fatalf("glob: %v %d", err, len(ents))
	}
	for _, e := range ents {
		b, err := agents.Files.ReadFile(e)
		if err != nil {
			t.Fatal(err)
		}
		check(e, b)
	}
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
	for _, tpl := range agentTemplates {
		files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{Template: tpl.ID, ID: "x-" + tpl.ID, Params: params[tpl.ID]})
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		check("template "+tpl.ID, files[0].Data)
	}
}

// Env values YAML would retype stay strings when the form writes them.
func TestFormWriterKeepsEnvStrings(t *testing.T) {
	doc := []byte("id: a\nadapter: command\nimage: x:1\nargv: [run]\nmodel: m\nenv:\n  A: x\n")
	cfg, err := decodeAgent(doc, "agents/a.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f := abFieldsOf(cfg, doc)
	vals := []string{"True", "FALSE", "yes", "No", "on", "~", "null", "Null", "12", "0x1F", "1e3", ".5", "-7", ".inf", "2026-01-02", "@x", "*a", "&a", "!t", "%p", "#c", "- a", "a: b", "{x}", "[1]", "'q", "\"q", "|", ">", "`b", "", " lead", "plain text"}
	f.Env = map[string]string{}
	for i, v := range vals {
		f.Env["K"+strings.Repeat("X", i)] = v
	}
	out, err := abApplyFields(doc, f)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := decodeAgent(out, "agents/a.yaml")
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for k, v := range f.Env {
		if cfg2.Env[k] != v {
			t.Fatalf("%s: got %q want %q\n%s", k, cfg2.Env[k], v, out)
		}
	}
}

// The web path: create from the mini-SWE-agent template, then save the Setup
// form unchanged and with one edit; both must be accepted.
func TestFormSaveMiniSWETemplateAgent(t *testing.T) {
	if _, ok := findTemplate("mini-swe-agent"); !ok {
		t.Skip("no mini-swe-agent template")
	}
	r, _ := builderRouter(t)
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "mini-swe-agent", ID: "zz-mini"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body := abCall(r, "GET", APIPrefix+"/agents/zz-mini/config", nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var doc AgentConfigDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	f := doc.Fields
	f.WallS = 900
	code, body = abCall(r, "PUT", APIPrefix+"/agents/zz-mini/config", AgentConfigPut{Fields: &f, IfMatch: doc.SHA256})
	if code != 200 {
		t.Fatalf("form save: %d %s", code, body)
	}
}
