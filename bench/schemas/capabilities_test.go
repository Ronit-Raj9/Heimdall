package schemas

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultCapabilities(t *testing.T) {
	cases := []struct {
		cfg  AgentConfig
		want CapabilitySet
	}{
		{AgentConfig{ID: "codex", Adapter: "command", Image: "bench/agent-codex:1"}, CapabilitySet{CapShell, CapTools, CapMCP, CapSessions}},
		{AgentConfig{ID: "claude-code", Adapter: "command"}, CapabilitySet{CapShell, CapTools, CapMCP, CapSessions}},
		{AgentConfig{ID: "mini-swe-agent", Adapter: "command"}, CapabilitySet{CapShell, CapTools, CapMCP, CapSessions}},
		{AgentConfig{ID: "bot", Adapter: "remote_http"}, CapabilitySet{CapTools, CapSessions}},
		{AgentConfig{ID: "clerk", Adapter: "a2a"}, CapabilitySet{CapTools, CapSessions}},
		{AgentConfig{ID: "support-sdk", Adapter: "openai_agent"}, CapabilitySet{CapTools, CapSessions}},
		{AgentConfig{ID: "fw-openai-agents", Adapter: "command"}, CapabilitySet{CapTools, CapSessions}},
		{AgentConfig{ID: "voice-realtime", Adapter: "command"}, CapabilitySet{CapSessions, CapVoice}},
		{AgentConfig{ID: "my-computer-use", Adapter: "command"}, CapabilitySet{CapShell, CapBrowser, CapDesktop}},
		{AgentConfig{ID: "custom-python", Adapter: "command"}, CapabilitySet{CapShell, CapTools}},
		{AgentConfig{ID: "mine", Adapter: "command", Memory: &AgentMemory{}, MCP: &AgentMCP{}}, CapabilitySet{CapShell, CapTools, CapMCP, CapSessions}},
		{AgentConfig{ID: "oracle", Adapter: "scripted"}, CapabilitySet(AllCapabilities)},
	}
	for _, c := range cases {
		if got := DefaultCapabilities(&c.cfg); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.cfg.ID, got, c.want)
		}
	}
	own := AgentConfig{ID: "codex", Adapter: "command", Capabilities: CapabilitySet{"Tools", "shell", "tools"}}
	if got := EffectiveCapabilities(&own); !reflect.DeepEqual(got, CapabilitySet{CapShell, CapTools}) {
		t.Fatalf("own list: %v", got)
	}
}

func TestCapabilitySetDecode(t *testing.T) {
	var cfg AgentConfig
	if err := yaml.Unmarshal([]byte("id: a\nadapter: command\ncapabilities: [shell, gpu]\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Capabilities, CapabilitySet{CapShell, CapGPU}) {
		t.Fatalf("yaml list: %v", cfg.Capabilities)
	}
	err := yaml.Unmarshal([]byte("id: a\ncapabilities:\n  supports_streaming: true\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "adapter_capabilities") {
		t.Fatalf("old object form: %v", err)
	}
	var old AgentConfig
	if err := json.Unmarshal([]byte(`{"id":"a","capabilities":{"supports_streaming":true}}`), &old); err != nil || old.Capabilities != nil {
		t.Fatalf("old manifest json: %v %v", err, old.Capabilities)
	}
	b, _ := json.Marshal(AgentConfig{ID: "a", Capabilities: CapabilitySet{CapTools}})
	if !strings.Contains(string(b), `"capabilities":["tools"]`) {
		t.Fatalf("json = %s", b)
	}
	if b, _ := json.Marshal(AgentConfig{ID: "a"}); strings.Contains(string(b), "capabilities") {
		t.Fatalf("empty set must be omitted (config hashes): %s", b)
	}
	if got := (CapabilitySet{CapShell}).Missing(CapabilitySet{CapGPU, CapShell, CapTools}); !reflect.DeepEqual(got, []Capability{CapTools, CapGPU}) {
		t.Fatalf("missing = %v", got)
	}
}
