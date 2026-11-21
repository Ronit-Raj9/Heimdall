package handlers

import "github.com/valyala/fasthttp"

// Agent versions area (agent-first contract, section 2).
func init() {
	p := APIPrefix + "/agents/{id}/versions"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p, Tag: "agents",
			Summary:  "The agent's saved versions, newest last, with the runs made with each",
			Response: AgentVersionList{}},
		RouteDoc{Method: "GET", Path: p + "/{n}", Tag: "agents",
			Summary:  "One frozen version of the agent config, as YAML",
			Response: AgentVersionDoc{}},
		RouteDoc{Method: "POST", Path: p, Tag: "agents",
			Summary: "Snapshot the current agent config as a new version (201), or return the latest when the config is unchanged (200)",
			Auth:    AuthWrite, Request: AgentVersionCreate{}, Status: fasthttp.StatusCreated, Response: AgentVersionCreated{}},
	)
}
