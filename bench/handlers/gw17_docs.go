package handlers

import "github.com/valyala/fasthttp"

// Agent copy and rename (fix wave 2, lane G17): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/{id}/copy", Tag: "agents", Summary: "Copy an agent's settings under a new name; with remove_original it renames the agent (past runs and versions keep the old name)",
			Auth: AuthWrite, Request: AgentCopyRequest{}, Status: fasthttp.StatusCreated, Response: AgentConfigDoc{}},
	)
}
