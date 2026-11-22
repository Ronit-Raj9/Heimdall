package tenancy

// MCP server mode (lane Z07). Inspecting reaches the org's server with its
// secret, and drafting writes a benchmark, so both are member routes.
func init() {
	OrgPolicy[key("POST", api+"/mcp-servers/inspect")] = org(member)
	OrgPolicy[key("POST", api+"/mcp-servers/benchmarks")] = org(member)
	OrgPolicy[key("GET", api+"/mcp-servers/benchmarks/{id}/report")] = org(viewer)
}
