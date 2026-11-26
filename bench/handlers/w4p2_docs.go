package handlers

// W4 features 2 and 6: the org's Bifrost repositories (gatewayrepos.go).

// gatewayPromptsDoc is GET /gateway/prompts.
type gatewayPromptsDoc struct {
	Prompts []GatewayPrompt `json:"prompts"`
}

// gatewayPromptVersionsDoc is GET /gateway/prompts/{id}/versions.
type gatewayPromptVersionsDoc struct {
	Versions []GatewayPromptVersion `json:"versions"`
}

// gatewaySkillsDoc is GET /gateway/skills.
type gatewaySkillsDoc struct {
	Skills []GatewaySkill `json:"skills"`
}

// gatewaySkillVersionsDoc is GET /gateway/skills/{id}/versions.
type gatewaySkillVersionsDoc struct {
	Versions []GatewaySkillVersion `json:"versions"`
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/prompts", Tag: "integrations",
			Summary: "The prompts in the org's own Bifrost Prompt Repository; 409 when no Bifrost is connected", Response: gatewayPromptsDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/prompts/{id}/versions", Tag: "integrations",
			Summary: "The versions of one prompt in the org's own Bifrost, with their number of messages", Response: gatewayPromptVersionsDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/skills", Tag: "integrations",
			Summary: "The skills in the org's own Bifrost Skills Repository; 409 when no Bifrost is connected", Response: gatewaySkillsDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/skills/{id}/versions", Tag: "integrations",
			Summary: "The versions of one skill in the org's own Bifrost", Response: gatewaySkillVersionsDoc{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/{id}/skills/from-bifrost", Tag: "integrations",
			Summary: "Import one skill version from the org's own Bifrost into the agent's skills, through the skill scan; 422 when the scan blocks it",
			Request: GatewaySkillImportRequest{}, Response: GatewaySkillImport{}},
	)
}
