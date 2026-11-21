package handlers

// Why agents fail (lane X11) route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/failure-patterns", Tag: "agents",
			Summary:  "Failure patterns per agent version (loops, unverified finishes, ignored errors, false completions) with example runs",
			Response: FailurePatterns{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/failure-patterns", Tag: "evaluations",
			Summary:  "Failure patterns per arm of an evaluation, with example runs",
			Response: FailurePatterns{}},
	)
}
