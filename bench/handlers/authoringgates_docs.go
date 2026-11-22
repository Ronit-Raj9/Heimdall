package handlers

import "github.com/maximhq/bifrost/bench/taskgates"

// Authoring helper (lane Z19): route docs.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/generated", Tag: "suites",
			Summary:  "Generated tasks of a benchmark and their review state; a pending or rejected task counts in no evaluation, gate, release or report card",
			Response: GeneratedTaskList{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/suites/{id}/tasks/{task}/review-state", Tag: "suites",
			Summary: "Accept or reject a generated task (a rejection needs a note); drafting a field again makes it pending once more",
			Auth:    AuthWrite, Request: TaskReviewRequest{}, Response: TaskReviewResult{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/suites/{id}/quality", Tag: "suites",
			Summary:  "Duplicate tasks (exact and near, MinHash, Jaccard at least threshold, default 0.8) and overlap with the built-in library (13-gram); a held-out task found in the library is an error",
			Response: taskgates.Report{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/suites/{id}/held-out", Tag: "suites",
			Summary: "Choose held-out tasks by seed, stratum or date, mark them held out and stamp the canary on their instructions; dry_run previews, and public tasks are held out only with confirm",
			Auth:    AuthWrite, Request: HeldOutSplitRequest{}, Response: HeldOutSplitResult{}},
	)
}
