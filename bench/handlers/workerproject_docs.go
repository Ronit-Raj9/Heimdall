package handlers

func init() {
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: WorkerProjectPath, Tag: "workers",
		Summary: "The organization's project (suites, agents, experiments, policies) as a gzip tar, for its runners; If-None-Match answers 304",
		Auth:    AuthWorker, Raw: "application/gzip"})
}
