package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoicePlatformTemplates(t *testing.T) {
	order := ""
	for _, tpl := range agentTemplates {
		order += tpl.ID + ","
	}
	if !strings.Contains(order, "openai-endpoint,vapi-assistant,retell-agent,elevenlabs-agent,voice-websocket,wrap-app,") {
		t.Fatalf("gallery order %s", order)
	}
	cases := []struct {
		id     string
		params map[string]string
		check  func(t *testing.T, yaml string)
	}{
		{TemplateVapi, map[string]string{"agent_id": "8c1e2c4a-1111", "api_key_secret": "VAPI_PRIVATE_KEY"}, nil},
		{TemplateRetell, map[string]string{"agent_id": "agent_9f", "api_key_secret": "RETELL_API_KEY", "timeout_s": "90"}, nil},
		{TemplateElevenLabs, map[string]string{"agent_id": "agent_01", "url": "https://api.eu.residency.elevenlabs.io"}, nil},
		{TemplateVoiceWS, map[string]string{"url": "wss://voice.example.com/twilio"}, nil},
	}
	for _, c := range cases {
		tpl, ok := findTemplate(c.id)
		if !ok || tpl.Adapter != "voice_platform" || tpl.Category != TemplateCategoryBYO || tpl.Docs != "docs/voice-platforms.md" {
			t.Fatalf("template %s: %+v", c.id, tpl)
		}
		files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "phone", Params: c.params})
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		cfg, err := decodeAgent(files[0].Data, "agents/phone.yaml")
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.id, err, files[0].Data)
		}
		vp := cfg.VoicePlatform
		if cfg.Adapter != "voice_platform" || vp == nil || vp.AgentID != c.params["agent_id"] || vp.APIKeySecret != c.params["api_key_secret"] {
			t.Fatalf("%s: %+v %+v", c.id, cfg, vp)
		}
		switch c.id {
		case TemplateVoiceWS:
			if vp.Platform != "websocket" || vp.Framing != "twilio" || vp.URL != "wss://voice.example.com/twilio" {
				t.Fatalf("websocket %+v", vp)
			}
		case TemplateRetell:
			if vp.Platform != "retell" || vp.TimeoutS != 90 {
				t.Fatalf("retell %+v", vp)
			}
		case TemplateElevenLabs:
			if vp.URL != "https://api.eu.residency.elevenlabs.io" {
				t.Fatalf("elevenlabs %+v", vp)
			}
		}
	}
	bad := map[string]map[string]string{
		TemplateVapi:       {"agent_id": "a1"},
		TemplateRetell:     {"api_key_secret": "K"},
		TemplateElevenLabs: {"agent_id": "a1", "api_key_secret": "has-dash"},
		TemplateVoiceWS:    {"url": "https://voice.example.com"},
	}
	for id, params := range bad {
		tpl, _ := findTemplate(id)
		_, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "phone", Params: params})
		if err == nil || strings.Contains(err.Error(), "voice_platform.") {
			t.Errorf("%s %v: want a plain refusal, got %v", id, params, err)
		}
	}
	tpl, _ := findTemplate(TemplateVoiceWS)
	if _, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "p", Params: map[string]string{"url": "wss://v.example.com", "framing": "pcm"}}); err == nil || !strings.Contains(err.Error(), "the audio format") {
		t.Errorf("bad framing: %v", err)
	}
}

func TestEndpointTestVoicePlatform(t *testing.T) {
	t.Setenv("VAPI_TEST_KEY", "vk-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" || r.Header.Get("Authorization") != "Bearer vk-123" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Invalid Key"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chat_1","output":[{"role":"assistant","content":"READY"}]}`))
	}))
	defer srv.Close()
	call := func(s Services, tpl string, params map[string]string) EndpointTestResult {
		r, _ := evalServer(t, s)
		body, _ := json.Marshal(EndpointTestRequest{Template: &AgentFromTemplateRequest{Template: tpl, ID: "phone", Params: params}})
		ctx := evalCall(r, "POST", APIPrefix+"/agents/test-endpoint", string(body))
		var res EndpointTestResult
		if err := json.Unmarshal(ctx.Response.Body(), &res); err != nil {
			t.Fatalf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		if strings.Contains(string(ctx.Response.Body()), "vk-123") {
			t.Fatal("key leaked into the result")
		}
		return res
	}
	res := call(Services{}, TemplateVapi, map[string]string{"agent_id": "asst_1", "api_key_secret": "VAPI_TEST_KEY", "url": srv.URL})
	if !res.OK || !res.Matched || res.Answer != "READY" || res.Adapter != "voice_platform" {
		t.Fatalf("result %+v", res)
	}
	t.Setenv("VAPI_TEST_KEY", "wrong")
	if res := call(Services{}, TemplateVapi, map[string]string{"agent_id": "asst_1", "api_key_secret": "VAPI_TEST_KEY", "url": srv.URL}); res.OK || !strings.Contains(res.Error, "Vapi answered HTTP 401: Invalid Key") {
		t.Fatalf("401: %+v", res)
	}
	if res := call(Services{}, TemplateVoiceWS, map[string]string{"url": "wss://voice.example.com/ws"}); res.OK || !strings.Contains(res.Error, "only takes calls") {
		t.Fatalf("websocket: %+v", res)
	}
	if res := call(Services{Hosted: true}, TemplateVapi, map[string]string{"agent_id": "asst_1", "api_key_secret": "VAPI_TEST_KEY", "url": srv.URL}); res.OK || !strings.Contains(res.Error, "https") {
		t.Fatalf("hosted http: %+v", res)
	}
}
