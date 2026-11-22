package handlers

// Why agents fail (lane X11) route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/failure-patterns", Tag: "agents",
			Summary: "Failure patterns per agent version (loops, unverified finishes, ignored errors, false completions) with example runs",
			Query: []QueryDoc{
				{Name: "version", Description: "Only this saved agent version (a number such as 1); default every version"},
				{Name: "benchmark", Description: "Only runs on this benchmark (suite id); default every benchmark"},
				{Name: "include_practice", Description: "true also reads runs on practice benchmarks (the made-up Acme examples and coding-core); default false"},
			},
			Response: FailurePatterns{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/evaluations/{id}/failure-patterns", Tag: "evaluations",
			Summary:  "Failure patterns per arm of an evaluation, with example runs",
			Response: FailurePatterns{}},
	)
}
