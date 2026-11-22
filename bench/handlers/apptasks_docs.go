package handlers

import "github.com/valyala/fasthttp"

// Tasks from your API (lane Z18): route docs. The model steps run as the
// apptasks.generate job (POST /jobs, kind apptasks.generate, params draft,
// stage rules|tasks, provider, model, yes, max_usd?, key_id?, per_rule?,
// judged?).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/app-tasks", Tag: "suites",
			Summary: "Start a draft from an OpenAPI 3 file and a policy document: checks that the spec makes an app copy and stores both; no model call",
			Auth:    AuthWrite, Request: AppTasksCreateRequest{}, Status: fasthttp.StatusCreated, Response: AppTasksDraftView{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/app-tasks", Tag: "suites",
			Summary: "List the drafts of tasks from an API and policy, newest first",
			Auth:    AuthNone, Response: AppTasksList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/app-tasks/{id}", Tag: "suites",
			Summary: "One draft: the app copy's operations, the policy rules, the drafted tasks with their checks and proof, dropped tasks with the reason, model calls and spend (members only: it holds the hidden checks)",
			Auth:    AuthNone, Response: AppTasksDraftView{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/app-tasks/{id}/rules", Tag: "suites",
			Summary: "Approve the policy rules (edited by a person) once; clears tasks drafted from older rules",
			Auth:    AuthWrite, Request: AppTasksRulesRequest{}, Response: AppTasksDraftView{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/app-tasks/{id}/write", Tag: "suites",
			Summary: "Write the drafted tasks into the benchmark as pending review (a new benchmark gets the app copy and grader checks)",
			Auth:    AuthWrite, Request: AppTasksWriteRequest{}, Status: fasthttp.StatusCreated, Response: AppTasksWriteResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/app-tasks/{id}/review", Tag: "suites",
			Summary: "Accept a pending task (marks it reviewed) or reject it (removes it from the benchmark)",
			Auth:    AuthWrite, Request: AppTasksReviewRequest{}, Response: AppTasksReviewResult{}},
	)
}
