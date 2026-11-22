package handlers

import "github.com/maximhq/bifrost/bench/reportcard"

// Report card area (agent-first lane A04) route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/report", Tag: "evaluations",
			Summary: "The report card of an evaluation: score per benchmark and arm, capability rollup, faults, safety, A/B verdicts, scores by task metadata (slices) and the MCP route (mcp_route, empty is direct)", Response: reportcard.Report{},
			Query: []QueryDoc{{Name: "where", Description: "key:value, repeatable: only the tasks whose metadata has that value (for example tier:enterprise)"}}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/report-card", Tag: "agents",
			Summary:  "An agent's report card from its newest done evaluation",
			Response: AgentReportCard{},
			Query:    []QueryDoc{{Name: "version", Description: "Agent version number"}, {Name: "model", Description: "Model id"}}},
	)
}
