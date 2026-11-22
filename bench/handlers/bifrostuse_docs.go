package handlers

// How Heimdall uses Bifrost (lane C10, bifrostuse.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/integrations/bifrost", Tag: "integrations", Summary: "The Bifrost features the org's runs use (gateway, virtual keys, budgets, fallbacks, cache, Heimdall plugin, logs) with the gateway's loaded plugins", Response: BifrostUse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/bifrost", Tag: "evaluations", Summary: "The Bifrost features one evaluation's runs go through, with details from its spec", Response: EvaluationBifrostUse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/bifrost/cache", Tag: "evaluations", Summary: "Bifrost cache on vs off report of one evaluation, per benchmark: cache hit rate, latency and cost saved, and success change between the cache-off and cache-on arms", Response: EvaluationCacheAB{}},
	)
}
