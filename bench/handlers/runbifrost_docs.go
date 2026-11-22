package handlers

// Through Bifrost run panel (lane C09, runbifrost.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/bifrost", Tag: "runs", Summary: "What Bifrost did for one run's model calls: providers and models served, fallbacks, cache hits, faults applied, tokens, cost and latency per call, from Bifrost log rows and the heimdall plugin's usage records", Response: RunBifrost{}},
	)
}
