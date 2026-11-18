package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// MCP config formats (AgentMCP.Format).
const (
	MCPFormatCodex    = "codex_toml"    // [mcp_servers.<id>] url = "..." in Codex's config.toml
	MCPFormatClaude   = "claude_json"   // "mcpServers": {"<id>": {"type": "http", "url": "..."}} in Claude Code's .claude.json
	MCPFormatOpenCode = "opencode_json" // "mcp": {"<id>": {"type": "remote", "url": "...", "enabled": true}} in OpenCode's config
	MCPFormatGeneric  = "generic_json"  // {"mcpServers": {"<id>": {"type": "http", "url": "..."}}} at mcp.config_path
	MCPFormatEnv      = "env"           // nothing written: BENCH_MCP_SERVERS and the template variables only
)

// tomlBareKey is what a TOML key may be without quotes.
var tomlBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// mcpVars adds the MCP template variables: {{mcp.servers_json}}, always (an
// empty object when the run has no MCP servers, so one config serves every
// task), and {{mcp.<id>.url}} for each server.
func mcpVars(vars map[string]string, rc *schemas.RunContext) error {
	js, err := mcpServersJSON(rc) // B2 MCP through Bifrost: the run key as an auth header for gateway-served servers (bifrostfeatures.go)
	if err != nil {
		return err
	}
	vars["mcp.servers_json"] = js
	for id, u := range utils.MCPServers(rc.Services) {
		vars["mcp."+id+".url"] = u
	}
	return nil
}

// addMCP tells the agent about the run's MCP servers (every started service
// of role mcp the agent can see, at the URL the engine resolved, normally the
// tool proxy's): BENCH_MCP_SERVERS, and the servers merged into the agent's
// own config file (mcpFormat, mcpConfigPath). A config file the agent's
// files already write is merged into, never replaced; a server id that file
// already defines is an error. A run without MCP servers leaves the plan
// untouched.
func addMCP(plan *schemas.RunPlan, rc *schemas.RunContext) error {
	servers := utils.MCPServers(rc.Services)
	if len(servers) == 0 {
		return nil
	}
	js, err := mcpServersJSON(rc) // B2 MCP through Bifrost (bifrostfeatures.go)
	if err != nil {
		return err
	}
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	plan.Env[utils.EnvMCPServers] = js
	format, err := mcpFormat(rc.Agent)
	if err != nil {
		return err
	}
	if format == MCPFormatEnv {
		return nil
	}
	p, err := mcpConfigPath(rc.Agent, format)
	if err != nil {
		return err
	}
	if plan.Files == nil {
		plan.Files = map[string]string{}
	}
	merged, err := mergeMCPAuth(format, plan.Files[p], servers, gatewayAuth(rc)) // B2 MCP through Bifrost: auth headers in the native config (bifrostfeatures.go)
	if err != nil {
		return fmt.Errorf("mcp config %s: %w", p, err)
	}
	plan.Files[p] = merged
	return nil
}

// mcpFormat is mcp.format when set, else the CLI's own format, else env.
func mcpFormat(cfg *schemas.AgentConfig) (string, error) {
	if cfg.MCP != nil && cfg.MCP.Format != "" {
		switch cfg.MCP.Format {
		case MCPFormatCodex, MCPFormatClaude, MCPFormatOpenCode, MCPFormatGeneric, MCPFormatEnv:
			return cfg.MCP.Format, nil
		}
		return "", fmt.Errorf("mcp.format %q is not one of codex_toml, claude_json, opencode_json, generic_json, env", cfg.MCP.Format)
	}
	switch nativeCLI(cfg) {
	case ParserCodex:
		return MCPFormatCodex, nil
	case ParserClaudeCode:
		return MCPFormatClaude, nil
	case ParserOpenCode:
		return MCPFormatOpenCode, nil
	}
	return MCPFormatEnv, nil
}

// mcpConfigPath is mcp.config_path when set, else where the format's CLI
// reads user-level MCP servers: Codex $CODEX_HOME/config.toml, Claude Code
// $CLAUDE_CONFIG_DIR/.claude.json (user scope, no approval prompt), OpenCode
// the file OPENCODE_CONFIG names, else its global opencode.json. generic_json
// has no native path and needs mcp.config_path.
func mcpConfigPath(cfg *schemas.AgentConfig, format string) (string, error) {
	p := ""
	if cfg.MCP != nil {
		p = strings.TrimSpace(cfg.MCP.ConfigPath)
	}
	if p == "" {
		switch format {
		case MCPFormatCodex:
			p = path.Join(envDir(cfg, "CODEX_HOME", agentHome+"/.codex"), "config.toml")
		case MCPFormatClaude:
			p = path.Join(envDir(cfg, "CLAUDE_CONFIG_DIR", agentHome+"/.claude"), ".claude.json")
		case MCPFormatOpenCode:
			p = envDir(cfg, "OPENCODE_CONFIG", "")
			if p == "" {
				p = path.Join(envDir(cfg, "XDG_CONFIG_HOME", agentHome+"/.config"), "opencode", "opencode.json")
			}
		default:
			return "", fmt.Errorf("mcp.format %s needs mcp.config_path", format)
		}
	}
	if !path.IsAbs(p) {
		return "", fmt.Errorf("mcp.config_path %q must be an absolute container path", p)
	}
	p = path.Clean(p)
	if p == Workspace || strings.HasPrefix(p, Workspace+"/") {
		return "", fmt.Errorf("mcp.config_path %q is inside the workspace", p)
	}
	return p, nil
}

// mergeMCP adds servers (id -> URL) to existing config content in format.
func mergeMCP(format, existing string, servers map[string]string) (string, error) {
	ids := make([]string, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if format == MCPFormatCodex {
		return mergeCodexTOML(existing, ids, servers)
	}
	key, entry := "mcpServers", func(u string) map[string]any { return map[string]any{"type": "http", "url": u} }
	if format == MCPFormatOpenCode {
		key, entry = "mcp", func(u string) map[string]any { return map[string]any{"type": "remote", "url": u, "enabled": true} }
	}
	return mergeJSON(existing, key, ids, servers, entry)
}

// mergeCodexTOML appends one [mcp_servers.<id>] table per server. Appending
// whole tables keeps every line of the existing config (provider, sandbox and
// feature settings) as written; Codex connects to a table with a url over
// streamable HTTP.
func mergeCodexTOML(existing string, ids []string, servers map[string]string) (string, error) {
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	for _, id := range ids {
		key := id
		if !tomlBareKey.MatchString(id) {
			key = strconv.Quote(id)
		}
		header := "[mcp_servers." + key + "]"
		if strings.Contains(existing, header) {
			return "", fmt.Errorf("already defines %s", header)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s\nurl = %s\n", header, strconv.Quote(servers[id]))
	}
	return b.String(), nil
}

// mergeJSON sets obj[key][id] = entry(url) for every server in an existing
// JSON object (or a new one), keeping every other field. The result is
// indented, with sorted keys, no HTML escaping and a final newline.
func mergeJSON(existing, key string, ids []string, servers map[string]string, entry func(string) map[string]any) (string, error) {
	obj := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		dec := json.NewDecoder(strings.NewReader(existing))
		dec.UseNumber()
		if err := dec.Decode(&obj); err != nil {
			return "", fmt.Errorf("existing content is not a JSON object: %w", err)
		}
	}
	section := map[string]any{}
	if raw, ok := obj[key]; ok && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("%q is not an object", key)
		}
		section = m
	}
	for _, id := range ids {
		if _, dup := section[id]; dup {
			return "", fmt.Errorf("already defines %s.%s", key, id)
		}
		section[id] = entry(servers[id])
	}
	obj[key] = section
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return "", fmt.Errorf("encode: %w", err)
	}
	return buf.String(), nil
}
