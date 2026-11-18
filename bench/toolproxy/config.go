package toolproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Protocols a route can speak (Route.Protocol).
const (
	ProtocolMCP  = "mcp"  // MCP streamable HTTP: JSON-RPC over POST, JSON or SSE answers
	ProtocolHTTP = "http" // a plain HTTP tool server: every request is one call
	// ProtocolExternal is a real HTTP API outside the bench (external.go).
	// The agent reaches it at /x/<id>/<path>, never at /s/.
	ProtocolExternal = "external"
)

// DefaultMCPPath is the MCP endpoint path when a route does not name one.
const DefaultMCPPath = "/mcp"

// Routes is the content of routes.json, written by the environment kernel
// for one run: which services the proxy fronts and how.
type Routes struct {
	RunID  string  `json:"run_id"`
	Routes []Route `json:"routes"`
	// Members maps a team member's route token to its member id (team runs,
	// docs/multi-agent.md): /m/<token>/s/<id>/ reaches the same services and
	// stamps the member on every row (members.go).
	Members map[string]string `json:"members,omitempty"`
}

// Route is one proxied service. The agent reaches it at
// /s/<Service>/<path> on the proxy, which forwards to Upstream + /<path>.
type Route struct {
	Service  string `json:"service"`            // service id
	Upstream string `json:"upstream"`           // e.g. http://fs:8080
	Protocol string `json:"protocol"`           // mcp | http
	MCPPath  string `json:"mcp_path,omitempty"` // default /mcp
	// External holds the allowlist, credential, mode and caps of an
	// external route (Protocol external); nil for every other route.
	External *ExternalRoute `json:"external,omitempty"`
	// Agent is the team member a request came in for (members.go); set per
	// request on a copy of the route, never read from routes.json.
	Agent string `json:"-"`
}

// ExternalRoute is how the proxy fronts one external API
// (schemas.ExternalAPI, docs/external-apis.md). The environment kernel
// resolves the mode and the paths; zero caps mean the utils defaults.
type ExternalRoute struct {
	Mode       string                  `json:"mode"` // live | record | replay
	Allow      []schemas.ExternalAllow `json:"allow,omitempty"`
	Headers    map[string]string       `json:"headers,omitempty"`
	AuthEnv    string                  `json:"auth_env,omitempty"` // variable in the proxy's own environment; read only in live and record mode
	AuthHeader string                  `json:"auth_header,omitempty"`
	AuthScheme string                  `json:"auth_scheme,omitempty"`
	Cassette   string                  `json:"cassette,omitempty"`  // replay: the cassette inside the proxy container
	RecordTo   string                  `json:"record_to,omitempty"` // record: the cassette the proxy writes
	// Connect is the TCP address dialed in place of the upstream host (the
	// run's egress forwarder); the Host header and the TLS server name stay
	// the upstream's.
	Connect  string `json:"connect,omitempty"`
	MaxCalls int    `json:"max_calls,omitempty"`
	MaxBytes int64  `json:"max_bytes,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
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
		case ProtocolExternal:
			if err := checkExternalRoute(r); err != nil {
				return nil, fmt.Errorf("route %s: %w", r.Service, err)
			}
		default:
			return nil, fmt.Errorf("route %s: protocol %q is not mcp, http or external", r.Service, r.Protocol)
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

// checkExternalRoute checks the external block of an external route: a known
// mode, the cassette a replay reads or the file a record writes, allowlist
// prefixes that start with /, no negative cap and an upstream without a
// query. It is pure.
func checkExternalRoute(r *Route) error {
	x := r.External
	if x == nil {
		return errors.New("external route has no external block")
	}
	if u, err := url.Parse(r.Upstream); err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("external upstream %q must be http(s)://host[:port][/path]", r.Upstream)
	}
	switch x.Mode {
	case schemas.ExternalLive:
	case schemas.ExternalRecord:
		if x.RecordTo == "" {
			return errors.New("record mode needs record_to")
		}
	case schemas.ExternalReplay:
		if x.Cassette == "" {
			return errors.New("replay mode needs a cassette")
		}
	default:
		return fmt.Errorf("external mode %q is not live, record or replay", x.Mode)
	}
	for i, a := range x.Allow {
		if !strings.HasPrefix(a.PathPrefix, "/") {
			return fmt.Errorf("allow %d: path_prefix %q must start with /", i+1, a.PathPrefix)
		}
	}
	if x.MaxCalls < 0 || x.MaxBytes < 0 || x.TimeoutS < 0 {
		return errors.New("max_calls, max_bytes and timeout_s must not be negative")
	}
	return nil
}
