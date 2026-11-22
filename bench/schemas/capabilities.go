package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capability is one thing an agent can do that a benchmark may need
// (agent-first contract, section 1). An agent config lists what it can do
// in capabilities:; a benchmark's needs come from suite.Requires.
type Capability string

// The capabilities an agent can declare.
const (
	CapShell    Capability = "shell"    // runs commands in a container workspace
	CapTools    Capability = "tools"    // calls tools (function calls, HTTP tools)
	CapMCP      Capability = "mcp"      // reaches tools over the Model Context Protocol
	CapSessions Capability = "sessions" // keeps state across several sessions or turns
	CapBrowser  Capability = "browser"  // operates a web browser
	CapDesktop  Capability = "desktop"  // operates a desktop through screenshots, mouse and keyboard
	CapVoice    Capability = "voice"    // takes and makes spoken calls
	CapGPU      Capability = "gpu"      // uses a GPU
)

// AllCapabilities lists every known capability in a fixed order.
var AllCapabilities = []Capability{CapShell, CapTools, CapMCP, CapSessions, CapBrowser, CapDesktop, CapVoice, CapGPU}

// Known reports whether c is a capability this build knows.
func (c Capability) Known() bool {
	for _, k := range AllCapabilities {
		if c == k {
			return true
		}
	}
	return false
}

// CapabilitySet is a list of capabilities. Order and duplicates do not
// matter; Normalize sorts and dedupes it.
type CapabilitySet []Capability

// Has reports whether the set holds c.
func (s CapabilitySet) Has(c Capability) bool {
	for _, x := range s {
		if x == c {
			return true
		}
	}
	return false
}

// Missing lists the capabilities of need the set lacks, in need's order.
func (s CapabilitySet) Missing(need CapabilitySet) []Capability {
	var out []Capability
	for _, c := range need.Normalize() {
		if !s.Has(c) {
			out = append(out, c)
		}
	}
	return out
}

// Normalize returns the set lowercased, trimmed, deduped and in the order of
// AllCapabilities (unknown names last, sorted). It never returns nil.
func (s CapabilitySet) Normalize() CapabilitySet {
	seen := map[Capability]bool{}
	for _, c := range s {
		c = Capability(strings.ToLower(strings.TrimSpace(string(c))))
		if c != "" {
			seen[c] = true
		}
	}
	out := CapabilitySet{}
	for _, c := range AllCapabilities {
		if seen[c] {
			out = append(out, c)
			delete(seen, c)
		}
	}
	var rest []string
	for c := range seen {
		rest = append(rest, string(c))
	}
	sort.Strings(rest)
	for _, c := range rest {
		out = append(out, Capability(c))
	}
	return out
}

// Strings returns the set as plain strings (never nil).
func (s CapabilitySet) Strings() []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, string(c))
	}
	return out
}

// capabilityListHint is the error for the old object form of an agent's
// capabilities: block, which now lives under adapter_capabilities:.
const capabilityListHint = "capabilities is a list such as [shell, tools]; adapter support flags (supports_*, fault_hooks) go under adapter_capabilities"

// UnmarshalYAML accepts a list of capability names and refuses the old
// adapter-support object with a hint.
func (s *CapabilitySet) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		return fmt.Errorf("line %d: %s", node.Line, capabilityListHint)
	}
	var names []string
	if err := node.Decode(&names); err != nil {
		return fmt.Errorf("capabilities: %w", err)
	}
	*s = toSet(names)
	return nil
}

// UnmarshalJSON accepts a list of capability names. An object (a manifest
// frozen before capabilities were a list) decodes to an empty set instead of
// failing, so old manifests stay readable.
func (s *CapabilitySet) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*s = nil
		return nil
	}
	if b[0] == '{' {
		*s = nil
		return nil
	}
	var names []string
	if err := json.Unmarshal(b, &names); err != nil {
		return fmt.Errorf("capabilities: %w", err)
	}
	*s = toSet(names)
	return nil
}

func toSet(names []string) CapabilitySet {
	if names == nil {
		return nil
	}
	out := make(CapabilitySet, 0, len(names))
	for _, n := range names {
		out = append(out, Capability(n))
	}
	return out
}

// Agent kinds the defaults key on (DefaultCapabilities).
var (
	cliCodingAgents = []string{"codex", "claude-code", "claude", "opencode", "gemini-cli", "gemini", "mini-swe-agent", "mini-swe", "aider", "goose", "cline"}
	computerUseHint = []string{"computer-use", "computer_use", "cua", "desktop"}
	voiceHint       = []string{"voice", "realtime", "speech"}
	sessionAgents   = []string{"openai-agents", "openai_agent", "openai-agent"}
)

// DefaultCapabilities is what an agent config can do when it lists no
// capabilities: of its own, by adapter and kind (contract section 1): CLI
// coding agents shell, tools, mcp, sessions (Heimdall's memory benchmarks
// run them across sessions through the per-session notes and memory
// folder, as they always have); remote_http, a2a and OpenAI Agents SDK
// agents tools, sessions; voice agents voice, sessions; computer-use agents
// desktop, browser, shell; other command agents (custom python, plain
// command) shell, tools. Scripted agents are test doubles (noop, oracle)
// and can run anything. A config with an mcp: block adds mcp, and one with
// a memory: block adds sessions.
func DefaultCapabilities(cfg *AgentConfig) CapabilitySet {
	if cfg == nil {
		return CapabilitySet{}
	}
	var set CapabilitySet
	names := strings.ToLower(cfg.ID + " " + cfg.Image)
	if len(cfg.Argv) > 0 {
		names += " " + strings.ToLower(cfg.Argv[0])
	}
	switch cfg.Adapter {
	case "scripted":
		set = append(set, AllCapabilities...)
	case "remote_http", "a2a", "openai_agent":
		set = CapabilitySet{CapTools, CapSessions}
	case "voice_platform": // a hosted voice agent: calls, and text turns through the conversation driver
		set = CapabilitySet{CapVoice, CapSessions}
	default:
		switch {
		case containsAny(names, voiceHint):
			set = CapabilitySet{CapVoice, CapSessions}
		case containsAny(names, computerUseHint):
			set = CapabilitySet{CapDesktop, CapBrowser, CapShell}
		case containsAny(names, sessionAgents):
			set = CapabilitySet{CapTools, CapSessions}
		case containsAny(names, cliCodingAgents):
			set = CapabilitySet{CapShell, CapTools, CapMCP, CapSessions}
		default:
			set = CapabilitySet{CapShell, CapTools}
		}
	}
	if cfg.MCP != nil {
		set = append(set, CapMCP)
	}
	if cfg.Memory != nil {
		set = append(set, CapSessions)
	}
	return set.Normalize()
}

// EffectiveCapabilities is what the agent can do: its own capabilities:
// list when it has one, else DefaultCapabilities.
func EffectiveCapabilities(cfg *AgentConfig) CapabilitySet {
	if cfg == nil {
		return CapabilitySet{}
	}
	if len(cfg.Capabilities) > 0 {
		return cfg.Capabilities.Normalize()
	}
	return DefaultCapabilities(cfg)
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
