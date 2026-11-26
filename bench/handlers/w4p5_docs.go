package handlers

// W4 Bifrost features 11 and 14 (gatewayagents.go, bifrostsetting.go).

// gatewayAgentsDoc is GET /gateway/agents.
type gatewayAgentsDoc struct {
	Agents []GatewayAgent `json:"agents"`
}

// gatewayAgentImportDoc is the answer of POST /gateway/agents/{name}/import.
type gatewayAgentImportDoc struct {
	AgentID string `json:"agent_id"`
	YAML    string `json:"yaml"`
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/agents", Tag: "integrations",
			Summary: "The agents registered in the org's own Bifrost; 409 until a Bifrost is connected", Response: gatewayAgentsDoc{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/gateway/agents/{name}/import", Tag: "integrations",
			Summary: "Build the agent file for one registered agent; nothing is saved, post the file to the agents route to create it",
			Request: gatewayAgentImportRequest{}, Response: gatewayAgentImportDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/experiments/{id}/bifrost-setting", Tag: "integrations",
			Summary: "The Bifrost setting to paste for this comparison's result, or null with the reason; Heimdall never applies it", Response: BifrostSettingAnswer{}},
	)
}
