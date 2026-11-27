package handlers

import "github.com/maximhq/bifrost/bench/findings"

// Wave 4 lane I30 (design W4-43): findings and the rerun that proves a fix.
func init() {
	const tag = "findings"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/findings", Tag: tag,
			Summary:  "The organization's findings, newest change first, with a count per state; fixed findings whose tasks failed again are reopened first",
			Query:    []QueryDoc{{Name: "state", Description: "open, expected, fixed or reopened"}, ArchivedQueryDoc},
			Response: FindingsList{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/findings/{id}", Tag: tag, Summary: "One finding with its tasks, runs, quotes and history", Response: findings.Finding{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/findings", Tag: tag,
			Summary: "Track a failure as a finding from an inbox item, a cluster or an investigation group; 201 when new, 200 when it is already tracked",
			Request: FindingAddRequest{}, Status: 201, Response: findings.Finding{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/findings/{id}/expected", Tag: tag, Summary: "Mark a finding as known and accepted (reason of 3 to 500 characters); it never reopens by itself", Request: FindingReasonRequest{}, Response: findings.Finding{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/findings/{id}/reopen", Tag: tag, Summary: "Reopen a fixed or expected finding (reason of 3 to 500 characters)", Request: FindingReasonRequest{}, Response: findings.Finding{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/findings/{id}/rerun", Tag: tag,
			Summary: "Rerun only the finding's tasks with the old agent and the fixed agent, kept apart from benchmark results; 409 when the agent has not changed",
			Request: FindingRerunRequest{}, Status: 202, Response: FindingRerunResult{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/findings/{id}/check", Tag: tag, Summary: "Judge the newest rerun: fixed only when the fixed agent passed every run (at least 3) and the old agent failed one", OptionalBody: true, Response: findings.Finding{}},
	)
}
