package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Remote coverage: tools through this bench (docs/remote-execution.md). A
// remote_http agent with remote.tools expose reaches the run's tool proxy
// through a per-run gate published on the bench host, at
// <base>/r/<run id>/s/<service id>/... with the run's bearer token.

// DefaultRemoteToolsListen is where a gate is published when the config sets
// no remote_tools.listen: loopback only, a free port per run.
const DefaultRemoteToolsListen = "127.0.0.1:0"

// RemoteToolsExposed reports whether a run's agent asks for the tool proxy to
// be published to it.
func RemoteToolsExposed(a *schemas.AgentConfig) bool {
	if a != nil && a.A2A != nil { // B1: an a2a agent asks with a2a.tools
		return a.A2A.Tools == schemas.RemoteToolsExpose
	}
	return a != nil && a.Remote != nil && a.Remote.Tools == schemas.RemoteToolsExpose
}

// CheckRemoteAgent refuses a remote block with an unknown tools or artifacts
// value. It is pure.
func CheckRemoteAgent(r *schemas.RemoteAgent) error {
	if r == nil {
		return nil
	}
	switch r.Tools {
	case "", schemas.RemoteToolsOff, schemas.RemoteToolsExpose:
	default:
		return fmt.Errorf("remote.tools %q: want expose or off", r.Tools)
	}
	switch r.Artifacts {
	case "", "none", schemas.RemoteArtifactsInline:
	default:
		return fmt.Errorf("remote.artifacts %q: want inline or none", r.Artifacts)
	}
	return nil
}

// RemoteOutputAllowed says whether a remote agent can run a task with output
// kind kind, and why not when it cannot. Answer always; state only with tools
// exposed (it changes the services through the published tool proxy);
// artifact only when it returns files inline; diff never, since the
// workspace is not beside it. It is pure.
func RemoteOutputAllowed(r *schemas.RemoteAgent, kind string) (bool, string) {
	switch kind {
	case schemas.OutputAnswer:
		return true, ""
	case schemas.OutputState:
		if r != nil && r.Tools == schemas.RemoteToolsExpose {
			return true, ""
		}
		return false, "a state task needs remote.tools expose: a remote agent changes the run's services only through the published tool proxy"
	case schemas.OutputArtifact:
		if r != nil && r.Artifacts == schemas.RemoteArtifactsInline {
			return true, ""
		}
		return false, "an artifact task needs remote.artifacts inline: a remote agent is not beside the workspace, so its files arrive only inline in its response"
	default:
		return false, "a remote agent is not beside the workspace, so it cannot produce a " + kind + " output"
	}
}

// NewRemoteToolsToken mints a run's bearer token: 32 random bytes, hex.
func NewRemoteToolsToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mint remote tools token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SplitRemoteToolsListen splits remote_tools.listen into a host and a port
// (0: a free port). Empty means DefaultRemoteToolsListen. It is pure.
func SplitRemoteToolsListen(listen string) (string, int, error) {
	if listen == "" {
		listen = DefaultRemoteToolsListen
	}
	host, p, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, fmt.Errorf("remote_tools.listen %q: %w", listen, err)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 0 || port > 65535 || host == "" || net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("remote_tools.listen %q: want ip:port (port 0 picks a free one)", listen)
	}
	return host, port, nil
}

// RemoteToolsBaseURL is the base URL a remote agent is told: public with
// "{port}" replaced when set, else http://<host>:<port>. It is pure.
func RemoteToolsBaseURL(public, host string, port int) (string, error) {
	if public != "" {
		out := strings.ReplaceAll(strings.TrimRight(public, "/"), "{port}", strconv.Itoa(port))
		u, err := url.Parse(out)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", fmt.Errorf("remote_tools.public_url %q: want http(s)://host[:port][/path] with no user info", public)
		}
		return out, nil
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// RemoteToolsAccessFrom lists what a run's gate serves, from the started
// services' handles: every proxied service (the proxy's URL) and every
// external API. Hidden services never have a proxy URL, so they never
// appear. It is pure.
func RemoteToolsAccessFrom(runID string, handles []schemas.ServiceHandle) *schemas.RemoteToolsAccess {
	a := &schemas.RemoteToolsAccess{RunID: runID, MCPPaths: map[string]string{}}
	for _, h := range handles {
		if h.Spec.Hidden {
			continue
		}
		switch {
		case h.Spec.Role == schemas.ServiceRoleExternal:
			a.External = append(a.External, h.Spec.ID)
		case strings.Contains(h.URL, "/s/"+h.Spec.ID) && strings.HasPrefix(h.URL, "http://toolproxy:"):
			a.Services = append(a.Services, h.Spec.ID)
			if h.Spec.Role == schemas.ServiceRoleMCP {
				p := DefaultMCPPath
				if h.Spec.MCP != nil && h.Spec.MCP.Path != "" {
					p = h.Spec.MCP.Path
				}
				a.MCP = append(a.MCP, h.Spec.ID)
				a.MCPPaths[h.Spec.ID] = p
			}
		}
	}
	sort.Strings(a.Services)
	sort.Strings(a.MCP)
	sort.Strings(a.External)
	return a
}

// RemoteToolEntry is one entry of the request's tools block.
type RemoteToolEntry struct {
	URL      string `json:"url"`
	Protocol string `json:"protocol"` // mcp | http | external
}

// RemoteToolURLs is the tools block a remote agent is sent, without the
// token: id to URL. MCP services point at their MCP endpoint, other services
// at their base, external APIs at their base. It is pure.
func RemoteToolURLs(a *schemas.RemoteToolsAccess) map[string]RemoteToolEntry {
	if a == nil || a.BaseURL == "" {
		return nil
	}
	base := strings.TrimRight(a.BaseURL, "/") + "/r/" + a.RunID
	out := map[string]RemoteToolEntry{}
	for _, id := range a.Services {
		if p, ok := a.MCPPaths[id]; ok {
			out[id] = RemoteToolEntry{URL: base + "/s/" + id + p, Protocol: "mcp"}
			continue
		}
		out[id] = RemoteToolEntry{URL: base + "/s/" + id, Protocol: "http"}
	}
	for _, id := range a.External {
		out[id] = RemoteToolEntry{URL: base + ExternalPathPrefix + id, Protocol: "external"}
	}
	return out
}
