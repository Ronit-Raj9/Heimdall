package handlers

import "github.com/valyala/fasthttp"

// Own benchmarks from the website (lane X13): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/rows", Tag: "suites",
			Summary: "Add answer tasks to a question-and-answer suite from CSV, JSON Lines or support tickets, with a column mapping for nested records, chat conversations, attachments and metadata; dry_run previews and writes nothing; more than 500 rows (or async) import in the background and answer 202",
			Auth:    AuthWrite, Request: RowImportRequest{}, Status: fasthttp.StatusCreated, Response: RowImportResult{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/rows/imports/{import}", Tag: "suites",
			Summary: "Progress of a background rows import: tasks written so far, problems, and done, failed or interrupted",
			Auth:    AuthNone, Response: RowImportStatus{}},
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

// Stateful mocks from OpenAPI (lane Y14): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/mocks", Tag: "suites",
			Summary: "Write services/<id>/ of a benchmark from an OpenAPI 3 file: a mock serving the same routes over HTTP and as MCP tools on seeded state it exports for checks.yaml; dry_run previews and writes nothing",
			Auth:    AuthWrite, Request: SuiteMockRequest{}, Status: fasthttp.StatusCreated, Response: SuiteMockResult{}},
	)
}
