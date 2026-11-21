package handlers

import "github.com/valyala/fasthttp"

// Agent ops area (web parity lane L09) route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/skills", Tag: "agents",
			Summary: "The skill folders of an agent (agents/<id>/skills/<name>/) and whether its config installs them", Response: AgentSkillsResponse{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/{id}/skills", Tag: "agents", Auth: AuthWrite,
			Summary:    "Upload a skill as a zip (it must hold SKILL.md); it lands confined in agents/<id>/skills/<name>/ and the config's skills.extra lists the folder",
			RawRequest: "application/zip", Status: fasthttp.StatusCreated, Response: AgentSkillsResponse{},
			Query: []QueryDoc{{Name: "name", Description: "Skill name (one path segment)"}, {Name: "replace", Description: "true replaces a skill of the same name"}}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/agents/{id}/skills/{name}", Tag: "agents", Auth: AuthWrite,
			Summary: "Delete one skill folder of an agent", Response: AgentSkillsResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/mcp", Tag: "agents",
			Summary: "An agent's MCP tools: its mcp delivery block and the MCP servers file agents/<id>/mcp.yaml", Response: AgentMCPResponse{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/agents/{id}/mcp", Tag: "agents", Auth: AuthWrite,
			Summary: "Save an agent's MCP delivery block and MCP servers file", Request: AgentMCPRequest{}, Response: AgentMCPResponse{}},
	)
}
