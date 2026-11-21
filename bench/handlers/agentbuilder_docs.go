package handlers

import "github.com/valyala/fasthttp"

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/templates", Tag: "agents", Summary: "Agent templates the web builder offers", Response: AgentTemplateList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/from-template", Tag: "agents", Summary: "Create agents/<id>.yaml and its image folder from a template",
			Auth: AuthWrite, Request: AgentFromTemplateRequest{}, Status: fasthttp.StatusCreated, Response: AgentFromTemplateResponse{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/config/validate", Tag: "agents", Summary: "Validate agent YAML (optionally after applying form fields); writes nothing",
			Request: AgentConfigValidateRequest{}, Response: AgentConfigValidateResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/config", Tag: "agents", Summary: "One agent's YAML and form fields", Response: AgentConfigDoc{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/agents/{id}/config", Tag: "agents", Summary: "Save an agent's YAML or form fields (If-Match by sha256)",
			Auth: AuthWrite, Request: AgentConfigPut{}, Response: AgentConfigDoc{}},
	)
}
