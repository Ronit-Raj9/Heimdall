package handlers

import "github.com/valyala/fasthttp"

// Own benchmarks from the website (lane X13): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/rows", Tag: "suites",
			Summary: "Add answer tasks to a question-and-answer suite from CSV, JSON Lines or support tickets; dry_run previews and writes nothing",
			Auth:    AuthWrite, Request: RowImportRequest{}, Status: fasthttp.StatusCreated, Response: RowImportResult{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/grader", Tag: "suites",
			Summary: "Choose a question-and-answer suite's grader (answer_match or judge) and pin its judge model",
			Auth:    AuthWrite, Request: SuiteGraderRequest{}, Response: SuiteGraderResult{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/tasks/{task}/split", Tag: "suites",
			Summary: "Mark a task held out (private split) or public",
			Auth:    AuthWrite, Request: TaskSplitRequest{}, Response: TaskSplitResult{}},
	)
}

// Scenario rows (lane Y14): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/scenario-rows", Tag: "suites",
			Summary: "Add support tasks with a simulated customer and a declarative checks.yaml from CSV or JSON Lines scenario rows; dry_run previews and writes nothing",
			Auth:    AuthWrite, Request: ScenarioRowsRequest{}, Status: fasthttp.StatusCreated, Response: ScenarioRowsResult{}},
	)
}
