package environments

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// filterChecker is a gateway that can run the W4 feature 4 tool filter
// check (gateways/bifrost/mcpfilter.go).
type filterChecker interface {
	CheckForbiddenTool(ctx context.Context, slug, key, tool, marker string) schemas.BifrostMCPFilterCheck
}

// checkForbidden calls each of the arm's forbidden tools through the
// gateway with the run's MCP key, before the agent starts, with the run id
// as the marker. A gateway without the check gives one check per entry
// that says so; an entry whose service has no client is reported, not
// called.
func checkForbidden(ctx context.Context, gw any, rc *schemas.RunContext, clients []schemas.BifrostMCPClient, key *schemas.RunKey) []schemas.BifrostMCPFilterCheck {
	if rc == nil || rc.Agent == nil || rc.Agent.Gateway.Bifrost == nil || rc.Agent.Gateway.Bifrost.MCP == nil || len(rc.Agent.Gateway.Bifrost.MCP.Forbidden) == 0 {
		return nil
	}
	fc, ok := gw.(filterChecker)
	if !ok {
		if g, isGW := gw.(schemas.Gateway); isGW {
			fc, ok = schemas.GatewayAs[filterChecker](g)
		}
	}
	marker := ""
	if rc.Run != nil {
		marker = rc.Run.RunID
	}
	keyValue := ""
	if key != nil {
		keyValue = key.Value
	}
	var out []schemas.BifrostMCPFilterCheck
	for _, entry := range rc.Agent.Gateway.Bifrost.MCP.Forbidden {
		svc, tool, _ := strings.Cut(entry, "/")
		base := schemas.BifrostMCPFilterCheck{Service: svc, Tool: tool, Marker: marker}
		if !ok {
			base.Detail = "this gateway cannot run the filter check"
			out = append(out, base)
			continue
		}
		slug := ""
		for _, c := range clients {
			if c.Service == svc {
				slug = c.Slug
				break
			}
		}
		if slug == "" {
			base.Detail = "the run has no Bifrost MCP client for service " + svc
			out = append(out, base)
			continue
		}
		c := fc.CheckForbiddenTool(ctx, slug, keyValue, tool, marker)
		c.Service = svc
		out = append(out, c)
	}
	return out
}
