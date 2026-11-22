package adapters

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B2 Bifrost features under test (docs/bifrost-features.md): what the agent
// is told when its arm turns on a Bifrost feature. Two things reach it:
//
//   - MCP through Bifrost: each MCP server's URL is the gateway's endpoint
//     for it, and the agent authenticates there with its run key. The key is
//     added as an Authorization header to BENCH_MCP_SERVERS
//     ({"<id>": {"url": ..., "headers": {...}}}) and to the agent's own MCP
//     config file (Codex http_headers, Claude Code, OpenCode and generic JSON
//     headers).
//   - Prompt cache and semantic cache: per-request headers the agent must
//     send on its model requests. They are in BENCH_GATEWAY_HEADERS (a JSON
//     object) and {{gateway.headers_json}}, and for Claude Code also in
//     ANTHROPIC_CUSTOM_HEADERS; an agent that sends no extra headers does not
//     get the feature, which the run's cache metrics then show.
const (
	EnvGatewayHeaders     = "BENCH_GATEWAY_HEADERS"
	envClaudeCustomHeader = "ANTHROPIC_CUSTOM_HEADERS"
)

// BifrostHeaders are the request headers the run's agent is asked to send
// for its arm's gateway.bifrost: x-bf-prompt-cache-auto-inject, and the
// semantic cache key (scoped by experiment, then arm, then task), type, TTL
// and threshold. Nil when the arm sets none.
func BifrostHeaders(rc *schemas.RunContext) map[string]string {
	if rc == nil {
		return nil
	}
	h := map[string]string{}
	for k, v := range rc.FaultHeaders { // C04: heimdall plugin fault plan and run tags
		h[k] = v
	}
	if rc.Agent == nil || rc.Agent.Gateway.Bifrost == nil {
		if len(h) == 0 {
			return nil
		}
		return h
	}
	f := rc.Agent.Gateway.Bifrost
	switch f.PromptCache {
	case schemas.PromptCacheAutoInject:
		h["x-bf-prompt-cache-auto-inject"] = "true"
	case schemas.PromptCacheOff:
		h["x-bf-prompt-cache-auto-inject"] = "false"
	}
	if sc := f.SemanticCache; sc != nil && rc.Run != nil {
		parts := []string{"hm", expID(rc)}
		if sc.Scope != schemas.SemanticCacheScopeExperiment && rc.Slot != nil {
			parts = append(parts, rc.Slot.Arm)
			if sc.Scope == schemas.SemanticCacheScopeTask {
				parts = append(parts, rc.Slot.Task)
			}
		}
		h["x-bf-cache-key"] = strings.Join(parts, "-")
		h["x-bf-cache-type"] = sc.Mode
		if sc.TTL != "" {
			h["x-bf-cache-ttl"] = sc.TTL
		}
		if sc.Mode == schemas.SemanticCacheSemantic && sc.Threshold > 0 {
			h["x-bf-cache-threshold"] = strconv.FormatFloat(sc.Threshold, 'g', -1, 64)
		}
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// bifrostVars adds {{gateway.headers_json}}: the headers as a JSON object,
// "{}" when the arm asks for none, so one config serves every arm.
func bifrostVars(vars map[string]string, rc *schemas.RunContext) {
	vars["gateway.headers_json"] = headersJSON(BifrostHeaders(rc))
}

// addBifrostEnv puts the headers into the plan's environment.
func addBifrostEnv(plan *schemas.RunPlan, rc *schemas.RunContext) {
	h := BifrostHeaders(rc)
	if len(h) == 0 {
		return
	}
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	plan.Env[EnvGatewayHeaders] = headersJSON(h)
	if nativeCLI(rc.Agent) == ParserClaudeCode {
		// Appended to the config's own custom headers (the shipped Claude
		// Code config sends its session headers there); a header the config
		// already names is left as the config wrote it.
		cur := plan.Env[envClaudeCustomHeader]
		lines := []string{}
		if cur != "" {
			lines = append(lines, cur)
		}
		for _, n := range sortedNames(h) {
			if !strings.Contains(strings.ToLower(cur), strings.ToLower(n)+":") {
				lines = append(lines, n+": "+h[n])
			}
		}
		plan.Env[envClaudeCustomHeader] = strings.Join(lines, "\n")
	}
}

// CarriesGatewayHeaders reports whether the agent is known to send the
// run's extra gateway headers on every model request: Claude Code reads
// ANTHROPIC_CUSTOM_HEADERS. Others get BENCH_GATEWAY_HEADERS too, but may
// ignore it, so a fault that must fire is not trusted to them (lane C04).
func CarriesGatewayHeaders(cfg *schemas.AgentConfig) bool {
	return cfg != nil && nativeCLI(cfg) == ParserClaudeCode
}

func headersJSON(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(h) // a map of strings always encodes
	return string(b)
}

func sortedNames(h map[string]string) []string {
	out := make([]string, 0, len(h))
	for n := range h {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// gatewayAuth returns, per MCP server id served through the gateway, the
// header the agent authenticates with: Authorization: Bearer <the run's MCP
// key> (the run key when no separate MCP key was made).
func gatewayAuth(rc *schemas.RunContext) map[string]map[string]string {
	if rc == nil || rc.Key == nil || rc.Key.Value == "" {
		return nil
	}
	var out map[string]map[string]string
	for _, h := range rc.Services {
		if !h.GatewayAuth {
			continue
		}
		if out == nil {
			out = map[string]map[string]string{}
		}
		k := h.GatewayKey
		if k == "" {
			k = rc.Key.Value
		}
		out[h.Spec.ID] = map[string]string{"Authorization": "Bearer " + k}
	}
	return out
}

// mcpServersJSON is utils.MCPServersJSON with the gateway's auth header on
// every server served through the gateway; byte for byte the same when none
// is.
func mcpServersJSON(rc *schemas.RunContext) (string, error) {
	key := ""
	if rc.Key != nil {
		key = rc.Key.Value
	}
	return utils.MCPServersJSONFor(rc.Services, key)
}

// mergeMCPAuth is mergeMCP plus the gateway auth headers of the servers
// served through the gateway: Codex gets an http_headers line in each such
// table, the JSON formats a "headers" object in each such entry. With no
// auth it is mergeMCP.
func mergeMCPAuth(format, existing string, servers map[string]string, auth map[string]map[string]string) (string, error) {
	if len(auth) == 0 {
		return mergeMCP(format, existing, servers)
	}
	ids := make([]string, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if format == MCPFormatCodex {
		out, err := mergeCodexTOML(existing, ids, servers)
		if err != nil {
			return "", err
		}
		for _, id := range ids {
			h, ok := auth[id]
			if !ok {
				continue
			}
			line := "url = " + strconv.Quote(servers[id]) + "\n"
			var hb strings.Builder
			hb.WriteString("http_headers = { ")
			for i, n := range sortedNames(h) {
				if i > 0 {
					hb.WriteString(", ")
				}
				hb.WriteString(strconv.Quote(n) + " = " + strconv.Quote(h[n]))
			}
			hb.WriteString(" }\n")
			at := strings.LastIndex(out, line)
			if at < 0 {
				return "", fmt.Errorf("codex table for %s not found after merge", id)
			}
			out = out[:at+len(line)] + hb.String() + out[at+len(line):]
		}
		return out, nil
	}
	byURL := map[string]map[string]string{}
	for id, h := range auth {
		byURL[servers[id]] = h
	}
	key, entry := "mcpServers", func(u string) map[string]any { return withHeaders(map[string]any{"type": "http", "url": u}, byURL[u]) }
	if format == MCPFormatOpenCode {
		key, entry = "mcp", func(u string) map[string]any {
			return withHeaders(map[string]any{"type": "remote", "url": u, "enabled": true}, byURL[u])
		}
	}
	return mergeJSON(existing, key, ids, servers, entry)
}

func withHeaders(entry map[string]any, h map[string]string) map[string]any {
	if len(h) > 0 {
		entry["headers"] = h
	}
	return entry
}
