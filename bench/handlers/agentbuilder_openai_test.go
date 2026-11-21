package handlers

import (
	"strings"
	"testing"
)

func TestOpenAIEndpointTemplate(t *testing.T) {
	tpl, ok := findTemplate("openai-endpoint")
	if !ok || tpl.Adapter != "openai_agent" || tpl.Category != TemplateCategoryBYO {
		t.Fatalf("template %+v", tpl)
	}
	files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "mine", Params: map[string]string{
		"url": "https://agent.example.com/v1", "model": "my-agent", "api_key_secret": "MY_AGENT_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeAgent(files[0].Data, "agents/mine.yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, files[0].Data)
	}
	o := cfg.OpenAI
	if cfg.Adapter != "openai_agent" || o == nil || o.URL != "https://agent.example.com/v1" || o.Model != "my-agent" || o.APIKeySecret != "MY_AGENT_KEY" || o.API != "" {
		t.Fatalf("config %+v openai %+v", cfg, o)
	}
	if f := abFieldsOf(cfg, files[0].Data); f.OpenAIURL != o.URL {
		t.Fatalf("fields %+v", f)
	}

	files, err = renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "resp", Params: map[string]string{"url": "http://10.0.0.5:8000/v1/responses"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err = decodeAgent(files[0].Data, "agents/resp.yaml"); err != nil || cfg.OpenAI.API != "responses" || cfg.OpenAI.APIKeySecret != "" {
		t.Fatalf("responses url: %v %+v", err, cfg)
	}

	for _, bad := range []map[string]string{
		{},
		{"url": "ftp://x"},
		{"url": "https://x/v1", "api_key_secret": "has-dash"},
		{"url": "https://x/v1", "model": "two words"},
	} {
		if _, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "bad", Params: bad}); err == nil {
			t.Errorf("params %v accepted", bad)
		}
	}
	if !strings.Contains(tpl.Description, "chat/completions") {
		t.Fatal("description does not say what the endpoint must speak")
	}
}
