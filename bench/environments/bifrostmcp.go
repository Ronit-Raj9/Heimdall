package environments

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B2 MCP through Bifrost (docs/bifrost-features.md). The run's tool proxy
// stays in front of every MCP service and keeps the trusted ledger; for an
// arm with gateway.bifrost.mcp it also joins the evaluation network under a
// per-run alias, so the gateway can reach it, and the gateway serves each
// proxied MCP service to the agent at its own endpoint, /mcp/<slug>, behind
// the run's MCP key (gateways/bifrost/bifrostmcp.go says why it is not the
// model key). Bifrost's mcp_tool_logs rows for that key are read back as a
// second witness that must agree with the ledger.

const (
	mcpLogSettle   = 500 * time.Millisecond // Bifrost's log writer batches rows (logs_store.writer.batch_interval)
	mcpLogPatience = 8 * time.Second        // how long collect waits for Bifrost's rows to reach the ledger's count
)

var _ schemas.BifrostMCPEnvironment = (*dockerWorkspace)(nil)

// toolProxyGatewayAlias is the tool proxy's name on the evaluation network.
func toolProxyGatewayAlias(runID string) string { return "tp-" + runID }

// ServeMCP implements schemas.BifrostMCPEnvironment.
func (e *dockerWorkspace) ServeMCP(ctx context.Context, rc *schemas.RunContext, codeMode bool) (*schemas.BifrostMCPEvidence, error) {
	gw, ok := schemas.GatewayAs[schemas.BifrostMCPGateway](e.cfg.ServiceGateway.Gateway)
	switch {
	case !ok:
		return nil, errors.New("the gateway cannot serve MCP servers (needs Bifrost's MCP gateway)")
	case e.cfg.ServiceGateway.AgentURL == "":
		return nil, errors.New("gateway.agent_url is not set, so the agent cannot be pointed at the gateway's MCP endpoint")
	case e.cfg.EvalNetwork == "":
		return nil, errors.New("there is no evaluation network for the gateway to reach the tool proxy on")
	case rc.Key == nil:
		return nil, errors.New("the run has no virtual key")
	}
	runID := rc.Run.RunID
	alias := toolProxyGatewayAlias(runID)
	var ups []schemas.MCPUpstream
	var idx []int
	for i, h := range rc.Services {
		if h.Spec.Role != schemas.ServiceRoleMCP || h.Spec.Hidden || h.URL == "" {
			continue
		}
		if !utils.ServiceProxied(h.Spec) {
			return nil, fmt.Errorf("service %s is not proxied: MCP through Bifrost keeps the tool proxy as the trusted witness, so the service must go through it", h.Spec.ID)
		}
		u, err := gatewayUpstream(utils.MCPServers([]schemas.ServiceHandle{h})[h.Spec.ID], alias)
		if err != nil {
			return nil, fmt.Errorf("service %s: %w", h.Spec.ID, err)
		}
		ups = append(ups, schemas.MCPUpstream{Service: h.Spec.ID, URL: u})
		idx = append(idx, i)
	}
	ev := &schemas.BifrostMCPEvidence{CodeMode: codeMode}
	if len(ups) == 0 {
		ev.Complete = true
		return ev, nil
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", "--alias", alias, e.cfg.EvalNetwork, toolProxyContainer(runID)); err != nil {
		return nil, fmt.Errorf("connect the tool proxy to the evaluation network for the gateway: %w", err)
	}
	clients, key, err := gw.ServeMCP(ctx, rc, ups, codeMode)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(e.cfg.ServiceGateway.AgentURL, "/")
	for n, i := range idx {
		rc.Services[i].URL = base + "/mcp/" + clients[n].Slug
		rc.Services[i].GatewayAuth = true
		rc.Services[i].GatewayKey = key.Value
	}
	ev.Clients, ev.KeyID = clients, key.ID
	return ev, nil
}

// gatewayUpstream rewrites the agent-side tool proxy URL of a service to the
// same route on the proxy's evaluation network alias.
func gatewayUpstream(agentURL, alias string) (string, error) {
	u, err := url.Parse(agentURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("tool proxy url %q cannot be parsed", agentURL)
	}
	port := u.Port()
	if port == "" {
		port = strconv.Itoa(ToolProxyPort)
	}
	u.Host = alias + ":" + port
	return u.String(), nil
}

// RemoveMCP implements schemas.BifrostMCPEnvironment.
func (e *dockerWorkspace) RemoveMCP(ctx context.Context, rc *schemas.RunContext) error {
	gw, ok := schemas.GatewayAs[schemas.BifrostMCPGateway](e.cfg.ServiceGateway.Gateway)
	if !ok || rc.Run == nil {
		return nil
	}
	return gw.RemoveMCP(context.WithoutCancel(ctx), rc.Run.RunID)
}

// CollectMCP implements schemas.BifrostMCPEnvironment. Bifrost writes its
// MCP rows in batches, so collect waits, within mcpLogPatience, until it
// holds at least as many rows as the tool proxy recorded tool calls, then
// reads once more after the batch interval so a late row is not missed.
func (e *dockerWorkspace) CollectMCP(ctx context.Context, rc *schemas.RunContext, ev *schemas.BifrostMCPEvidence, ledger []schemas.ToolCall) {
	gw, ok := schemas.GatewayAs[schemas.BifrostMCPGateway](e.cfg.ServiceGateway.Gateway)
	if !ok || ev == nil || len(ev.Clients) == 0 {
		return
	}
	want := ledgerToolCalls(ledger)
	deadline := time.Now().Add(mcpLogPatience)
	var calls []schemas.BifrostMCPCall
	complete, reason := false, ""
	for {
		calls, complete, reason = gw.MCPCalls(ctx, ev.KeyID, ev.Clients)
		if !complete || len(calls) >= want || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			ev.Calls, ev.Complete, ev.Reason = calls, false, ctx.Err().Error()
			return
		case <-time.After(mcpLogSettle):
		}
	}
	if complete {
		time.Sleep(mcpLogSettle)
		if again, ok2, _ := gw.MCPCalls(ctx, ev.KeyID, ev.Clients); ok2 && len(again) >= len(calls) {
			calls = again
		}
	}
	ev.Calls, ev.Complete, ev.Reason = calls, complete, reason
}

// ledgerToolCalls counts the MCP tool calls in the tool proxy ledger.
func ledgerToolCalls(ledger []schemas.ToolCall) int {
	n := 0
	for _, c := range ledger {
		if c.Kind == "" && c.Protocol == "mcp" {
			n++
		}
	}
	return n
}
