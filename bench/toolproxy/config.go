package toolproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Protocols a route can speak (Route.Protocol).
const (
	ProtocolMCP  = "mcp"  // MCP streamable HTTP: JSON-RPC over POST, JSON or SSE answers
	ProtocolHTTP = "http" // a plain HTTP tool server: every request is one call
)

// DefaultMCPPath is the MCP endpoint path when a route does not name one.
const DefaultMCPPath = "/mcp"

// Routes is the content of routes.json, written by the environment kernel
// for one run: which services the proxy fronts and how.
type Routes struct {
	RunID  string  `json:"run_id"`
	Routes []Route `json:"routes"`
}

// Route is one proxied service. The agent reaches it at
// /s/<Service>/<path> on the proxy, which forwards to Upstream + /<path>.
type Route struct {
	Service  string `json:"service"`            // service id
	Upstream string `json:"upstream"`           // e.g. http://fs:8080
	Protocol string `json:"protocol"`           // mcp | http
	MCPPath  string `json:"mcp_path,omitempty"` // default /mcp
}

var serviceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// LoadRoutes reads and checks routes.json.
func LoadRoutes(path string) (*Routes, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read routes %s: %w", path, err)
	}
	rs, err := ParseRoutes(b)
	if err != nil {
		return nil, fmt.Errorf("routes %s: %w", path, err)
	}
	return rs, nil
}

// ParseRoutes decodes and checks a routes document, filling defaults: the
// protocol defaults to mcp and the MCP path to /mcp. It is pure.
func ParseRoutes(b []byte) (*Routes, error) {
	var rs Routes
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, fmt.Errorf("decode routes: %w", err)
	}
	if len(rs.Routes) == 0 {
		return nil, errors.New("no routes")
	}
	seen := map[string]bool{}
	for i := range rs.Routes {
		r := &rs.Routes[i]
		if !serviceID.MatchString(r.Service) {
			return nil, fmt.Errorf("route %d: invalid service id %q", i+1, r.Service)
		}
		if seen[r.Service] {
			return nil, fmt.Errorf("route %d: duplicate service %q", i+1, r.Service)
		}
		seen[r.Service] = true
		u, err := url.Parse(r.Upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("route %s: upstream %q is not an http(s) URL", r.Service, r.Upstream)
		}
		r.Upstream = strings.TrimRight(r.Upstream, "/")
		switch r.Protocol {
		case "":
			r.Protocol = ProtocolMCP
		case ProtocolMCP, ProtocolHTTP:
		default:
			return nil, fmt.Errorf("route %s: protocol %q is not mcp or http", r.Service, r.Protocol)
		}
		if r.MCPPath == "" {
			r.MCPPath = DefaultMCPPath
		}
		if !strings.HasPrefix(r.MCPPath, "/") {
			r.MCPPath = "/" + r.MCPPath
		}
	}
	return &rs, nil
}
