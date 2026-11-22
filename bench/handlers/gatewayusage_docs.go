package handlers

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: GatewayUsagePath, Tag: "gateway",
			Summary: "Usage rows from the heimdall Bifrost plugin, one per model call (Authorization: Bearer <" + GatewayUsageSecretEnv + ">)",
			Request: GatewayUsageBatch{}, Response: GatewayUsageAccepted{}},
		RouteDoc{Method: "GET", Path: GatewayUsagePath, Tag: "gateway",
			Summary:  "A run's model calls as Bifrost reported them: provider, model, tokens, cost, latency, cache hits, fallbacks, faults",
			Query:    []QueryDoc{{Name: "run_id", Description: "the run"}},
			Response: GatewayUsageRun{}},
	)
}
