package utils

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// CanonicalArgsSHA256 hashes tool arguments as canonical JSON (keys sorted),
// whether they arrive as a JSON string (Bifrost's mcp_tool_logs arguments
// column, the tool proxy ledger's captured bytes) or as a decoded value, so
// two witnesses of one call hash alike however each serialized it. Empty or
// null arguments hash as "{}", the arguments of a tool called with none. A
// string that is not JSON hashes as that string (B2 Bifrost features).
func CanonicalArgsSHA256(v any) string {
	if s, ok := v.(string); ok {
		var decoded any
		switch {
		case strings.TrimSpace(s) == "":
			decoded = nil
		case json.Unmarshal([]byte(s), &decoded) != nil:
			decoded = s
		}
		v = decoded
	}
	if v == nil {
		v = map[string]any{}
	}
	h, err := HashJSON(v)
	if err != nil {
		return ""
	}
	return h
}

// MCPServersJSONFor is MCPServersJSON with an Authorization header on every
// server the gateway serves (ServiceHandle.GatewayAuth, B2 MCP through
// Bifrost): {"<id>": {"url": ..., "headers": {"Authorization": "Bearer
// <key>"}}}, with the handle's own GatewayKey, else key. With no such server
// it is MCPServersJSON byte for byte.
func MCPServersJSONFor(services []schemas.ServiceHandle, key string) (string, error) {
	auth := map[string]string{}
	for _, h := range services {
		switch {
		case h.GatewayAuth && h.GatewayKey != "":
			auth[h.Spec.ID] = h.GatewayKey
		case h.GatewayAuth && key != "":
			auth[h.Spec.ID] = key
		}
	}
	if len(auth) == 0 {
		return MCPServersJSON(services)
	}
	type server struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	m := map[string]server{}
	for id, u := range MCPServers(services) {
		s := server{URL: u}
		if k := auth[id]; k != "" {
			s.Headers = map[string]string{"Authorization": "Bearer " + k}
		}
		m[id] = s
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode mcp servers: %w", err)
	}
	return string(b), nil
}
