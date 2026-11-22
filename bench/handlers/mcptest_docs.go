package handlers

import (
	"github.com/maximhq/bifrost/bench/mcptest"
	"github.com/valyala/fasthttp"
)

// MCP server mode (lane Z07): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: MCPServerInspectPath, Tag: "mcp-servers", Summary: "Inspect an MCP server: reliability, error recovery, latency, schema and description quality, poisoning scan; read-only tools are called only when call_read_only is set, others only when named",
			Auth: AuthWrite, Request: MCPServerInspectRequest{}, Response: mcptest.Report{}},
		RouteDoc{Method: "POST", Path: MCPServerBenchmarksPath, Tag: "mcp-servers", Summary: "Inspect the server again and draft a benchmark of it into benchmarks/<id>/: one task per tool, run by reference agents against the live server, pending review; 409 when the id exists",
			Auth: AuthWrite, Request: MCPServerDraftRequest{}, Status: fasthttp.StatusCreated, Response: MCPServerDraftResponse{}},
		RouteDoc{Method: "GET", Path: MCPServerReportPath, Tag: "mcp-servers", Summary: "Per-tool view of a drafted benchmark's runs: how often agents picked each tool, called it with valid arguments and got a result, by agent",
			Query: []QueryDoc{{Name: "experiment", Description: "Only this experiment's runs; default every experiment"}}, Response: MCPServerReport{}},
	)
}
