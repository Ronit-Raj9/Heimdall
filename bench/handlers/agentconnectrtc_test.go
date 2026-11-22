package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/voicertc"
)

func TestConnectRTCForms(t *testing.T) {
	cases := []struct {
		tpl    string
		params map[string]string
		check  func(t *testing.T, yaml string)
	}{
		{TemplateLiveKitAgent, map[string]string{"dispatch": "webhook", "start_url": "https://bots.acme.example/start", "start_token_secret": "BOT_TOKEN",
			"livekit_url": "wss://acme.livekit.cloud", "api_key_secret": "LK_KEY", "api_secret_secret": "LK_SECRET", "timeout_s": "20"}, nil},
		{TemplatePhoneNumber, map[string]string{"number": "+14155550100", "trunk_address": "acme.pstn.twilio.com", "from_number": "+14155550199",
			"trunk_user_secret": "SIP_USER", "trunk_password_secret": "SIP_PASS", "transport": "tls"}, nil},
	}
	for _, c := range cases {
		tpl, ok := findTemplate(c.tpl)
		if !ok {
			t.Fatalf("no template %s", c.tpl)
		}
		files, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{Template: c.tpl, ID: "rtc-" + c.tpl, Params: c.params})
		if err != nil {
			t.Fatalf("%s: %v", c.tpl, err)
		}
		cfg, err := decodeAgent(files[0].Data, "agents/x.yaml")
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.tpl, err, files[0].Data)
		}
		if cfg.Adapter != "voice_rtc" || cfg.VoiceRTC == nil {
			t.Fatalf("%s: %+v", c.tpl, cfg)
		}
		if err := voicertc.Check(cfg.VoiceRTC); err != nil {
			t.Fatalf("%s: rendered block fails its check: %v", c.tpl, err)
		}
		if strings.Contains(string(files[0].Data), "secret_value") {
			t.Fatal("secret values in the yaml")
		}
	}
	tpl, _ := findTemplate(TemplateLiveKitAgent)
	lk, _ := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "a", Params: map[string]string{"dispatch": "webhook", "start_url": "https://b.example/s",
		"livekit_url": "wss://acme.livekit.cloud", "api_key_secret": "K", "api_secret_secret": "S", "timeout_s": "20"}})
	cfg, _ := decodeAgent(lk[0].Data, "agents/a.yaml")
	if l := cfg.VoiceRTC.LiveKit; l.StartURL != "https://b.example/s" || l.APISecretSecret != "S" || cfg.VoiceRTC.TimeoutS != 20 {
		t.Fatalf("livekit block %+v", cfg.VoiceRTC)
	}
	bad := []struct {
		tpl    string
		params map[string]string
		want   string
	}{
		{TemplateLiveKitAgent, map[string]string{"dispatch": "agent"}, "the agent name"},
		{TemplateLiveKitAgent, map[string]string{"dispatch": "auto", "livekit_url": "wss://x.example"}, "the LiveKit API key"},
		{TemplateLiveKitAgent, map[string]string{"dispatch": "auto", "api_key_secret": "not a name"}, "Settings > Secrets name"},
		{TemplatePhoneNumber, map[string]string{"number": "555-0100", "trunk_address": "a.example", "from_number": "+14155550199"}, "E.164"},
		{TemplatePhoneNumber, map[string]string{"number": "+14155550100", "trunk_address": "a.example"}, "the caller ID number"},
		{TemplatePhoneNumber, map[string]string{"number": "+14155550100", "trunk_address": "a.example", "from_number": "+14155550199", "timeout_s": "9000"}, "5 to 300"},
	}
	for _, c := range bad {
		tpl, _ := findTemplate(c.tpl)
		if _, err := renderAgentTemplate(tpl, AgentFromTemplateRequest{ID: "b", Params: c.params}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: %v, want %q", c.tpl, c.params, err, c.want)
		}
	}
}
