package bifrost

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 feature 4: MCP tool tests (audit/W4_DESIGN_3_BIFROST.md section 7). An
// arm's gateway.bifrost.mcp.tools narrows the run's MCP key grant per
// service, and its forbidden tools are called by the bench itself through
// Bifrost before the agent starts, expecting a refusal.

// toolGrant is tools_to_execute of the run key's grant for service: ["*"]
// unless the arm's gateway.bifrost.mcp.tools names tools; then the names
// listed for service (["*"] for "service/*"), possibly empty.
func toolGrant(rc *schemas.RunContext, service string) []string {
	if rc == nil || rc.Agent == nil || rc.Agent.Gateway.Bifrost == nil || rc.Agent.Gateway.Bifrost.MCP == nil || len(rc.Agent.Gateway.Bifrost.MCP.Tools) == 0 {
		return []string{"*"}
	}
	out := []string{}
	for _, entry := range rc.Agent.Gateway.Bifrost.MCP.Tools {
		svc, tool, ok := strings.Cut(entry, "/")
		if !ok || svc != service || tool == "" {
			continue
		}
		if tool == "*" {
			return []string{"*"}
		}
		out = append(out, tool)
	}
	return out
}

// CheckForbiddenTool is the filter check of one tool: initialize and
// tools/list at <cfg.URL>/mcp/<slug> with the MCP key, then tools/call with
// {"heimdall_filter_check": marker}. Listed: the list showed the tool.
// Refused: the call answered a JSON-RPC error, an HTTP status >= 400 or a
// result with isError true.
func (g *Gateway) CheckForbiddenTool(ctx context.Context, slug, key, tool, marker string) schemas.BifrostMCPFilterCheck {
	check := schemas.BifrostMCPFilterCheck{Tool: tool, Marker: marker}
	c := mcptest.NewClient(mcptest.Target{
		URL:     strings.TrimRight(g.cfg.URL, "/") + "/mcp/" + slug,
		Headers: map[string]string{"Authorization": "Bearer " + key},
	}, nil)
	defer c.Close(context.WithoutCancel(ctx))
	if _, a, err := c.Initialize(ctx); err != nil {
		// A key without the client's grant gets 403 here: that is a refusal.
		check.Refused = a.Transport == "" && (a.Status >= 400 || a.Error != nil)
		check.Detail = err.Error()
		return check
	}
	tools, _, err := c.ListTools(ctx)
	if err != nil {
		check.Detail = err.Error()
	}
	for _, t := range tools {
		if t.Name == tool {
			check.Listed = true
		}
	}
	a := c.CallTool(ctx, tool, map[string]any{"heimdall_filter_check": marker})
	switch {
	case a.Transport != "":
		check.Detail = "the call got no answer: " + a.Transport
	case a.Status >= 400 || a.Error != nil:
		check.Refused = true
		check.Detail = mcptest.Describe(a)
	default:
		var r struct {
			IsError bool `json:"isError"`
		}
		if json.Unmarshal(a.Result, &r) == nil && r.IsError {
			check.Refused = true
			check.Detail = "the call answered an error result"
		}
	}
	return check
}
