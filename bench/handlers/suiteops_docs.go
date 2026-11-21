package handlers

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/tasks/{task}/quarantine", Tag: "suites",
			Summary: "Quarantine one task: new plans skip it; suite.lock and existing experiments are unchanged",
			Auth:    AuthWrite, Request: QuarantineRequest{}, Response: QuarantineResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/tasks/{task}/restore", Tag: "suites",
			Summary: "Take one task out of quarantine so new plans include it again",
			Auth:    AuthWrite, Response: QuarantineResult{}},
	)
}
