package agentplatform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunImage is the image remote and platform agents run their launcher in
// (the same as the new-agent form's endpoint templates).
const RunImage = "bench/coding-core-base:1"

// AgentDefaults are the agent config fields the caller fills like the
// new-agent form does (model, gateway provider).
type AgentDefaults struct {
	Model    string
	Provider string
}

// AgentYAML renders the agent config of an import: a head comment that says
// where it came from, then id, adapter, image, budgets, gateway and the
// adapter block.
func AgentYAML(id string, res *Result, def AgentDefaults) ([]byte, error) {
	if res == nil || res.Block == nil {
		return nil, fmt.Errorf("nothing was imported")
	}
	p, _ := Find(res.Platform)
	cfg := agentDoc{ID: id, Adapter: res.Adapter, Image: RunImage, Model: def.Model, Budgets: agentBudgets{WallS: 900, CPUs: 1, MemGB: 2}}
	if def.Provider != "" {
		cfg.Gateway = &agentGateway{Provider: def.Provider}
	}
	switch res.Adapter {
	case "voice_platform":
		cfg.VoicePlatform = res.Block
	case "remote_http":
		cfg.Remote = res.Block
	default:
		return nil, fmt.Errorf("unknown adapter %q", res.Adapter)
	}
	var body bytes.Buffer
	enc := yaml.NewEncoder(&body)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	var head strings.Builder
	name := oneLine(res.Definition.Name, 120)
	fmt.Fprintf(&head, "# Imported from %s %s (%s) on %s.\n", p.Label, oneLine(res.AgentID, 200), name, dayOf(res.FetchedAt))
	fmt.Fprintf(&head, "# The definition Heimdall read is in agents/%s; import again to refresh it.\n", SidecarPath(id))
	return append([]byte(head.String()), body.Bytes()...), nil
}

// agentDoc keeps the config's keys in the order people read them.
type agentDoc struct {
	ID            string         `yaml:"id"`
	Adapter       string         `yaml:"adapter"`
	Image         string         `yaml:"image"`
	Model         string         `yaml:"model,omitempty"`
	Budgets       agentBudgets   `yaml:"budgets"`
	Gateway       *agentGateway  `yaml:"gateway,omitempty"`
	Remote        map[string]any `yaml:"remote,omitempty"`
	VoicePlatform map[string]any `yaml:"voice_platform,omitempty"`
}

type agentBudgets struct {
	WallS int `yaml:"wall_s"`
	CPUs  int `yaml:"cpus"`
	MemGB int `yaml:"mem_gb"`
}

type agentGateway struct {
	Provider   string `yaml:"provider"`
	MaxRetries int    `yaml:"max_retries"`
}

// SidecarPath is the path, under agents/, of the definition an import read.
func SidecarPath(id string) string { return "imported/" + id + ".json" }

// Sidecar is the JSON file kept next to an imported agent: the platform's
// definition (prompt, first message, tools, voice) for the conversation
// driver, the evaluate wizard and the agent page.
type Sidecar struct {
	Platform        string     `json:"platform"`
	AgentID         string     `json:"agent_id"`
	Preset          string     `json:"preset,omitempty"`
	Definition      Definition `json:"definition"`
	CapabilityHints []string   `json:"capability_hints"`
	Warnings        []string   `json:"warnings"`
	FetchedAt       string     `json:"fetched_at"`
}

// SidecarJSON renders the sidecar of an import.
func SidecarJSON(res *Result) ([]byte, error) {
	b, err := json.MarshalIndent(Sidecar{Platform: res.Platform, AgentID: res.AgentID, Preset: res.Preset, Definition: res.Definition,
		CapabilityHints: res.CapabilityHints, Warnings: res.Warnings, FetchedAt: res.FetchedAt}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

func dayOf(rfc string) string {
	if len(rfc) >= 10 {
		return rfc[:10]
	}
	return rfc
}
